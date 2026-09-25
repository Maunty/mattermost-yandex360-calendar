package main

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/mattermost/mattermost/server/public/pluginapi/cluster"
)

// botUsername is the account both message types come from.
const botUsername = "yandex-calendar"

// The scheduled jobs, named so that anything reasoning about their stored
// state — including tests — cannot drift from what is actually scheduled.
const (
	pollJobKey     = "yandex-calendar-poll"
	deliveryJobKey = "yandex-calendar-deliver"
)

// tickInterval is how often the scheduled jobs wake up. It is not the poll
// interval: a tick decides which people are due to be polled and which
// messages have come due, both of which are cheap when the answer is none.
const tickInterval = time.Minute

// Plugin is the whole plugin. Everything it needs is assembled in OnActivate
// and released in OnDeactivate, so that deactivating and reactivating works
// without restarting the server.
type Plugin struct {
	plugin.MattermostPlugin

	configurationLock sync.RWMutex
	configuration     *configuration

	client *pluginapi.Client
	store  *Store
	botID  string

	httpClient *http.Client

	// Provider endpoints. Fields rather than constants so that a test can
	// point the entire flow — consent, token, identity and calendar — at a
	// stand-in without any of the code under test knowing.
	authorizeURL   string
	tokenURL       string
	userInfoURL    string
	calendarAPIURL string
	caldavURL      string

	// readEvents is where Events come from. It is the REST API unless it is
	// set otherwise; CalDAV is kept behind the same seam as a fallback, and
	// switching to it is this one assignment (ADR 0002).
	readEvents eventSource

	jobsLock sync.Mutex
	jobs     []*cluster.Job
}

// OnActivate wires the plugin up. An administrator who has not yet supplied
// the Yandex credentials still gets a working install: the bot exists, the
// command answers, and it says what is missing.
func (p *Plugin) OnActivate() error {
	p.client = pluginapi.NewClient(p.API, p.Driver)
	p.store = NewStore(&p.client.KV, func() string { return p.getConfiguration().EncryptionKey })

	if p.httpClient == nil {
		p.httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	p.applyEndpointDefaults()

	botID, err := p.client.Bot.EnsureBot(&model.Bot{
		Username:    botUsername,
		DisplayName: "Yandex Calendar",
		Description: "Reminds you about your Yandex Calendar events. It only ever reads your calendar.",
	})
	if err != nil {
		return fmt.Errorf("failed to create the plugin's bot account: %w", err)
	}
	p.botID = botID

	if err := p.client.SlashCommand.Register(commandDefinition()); err != nil {
		return fmt.Errorf("failed to register the /%s command: %w", commandTrigger, err)
	}

	return p.startJobs()
}

func (p *Plugin) applyEndpointDefaults() {
	if p.authorizeURL == "" {
		p.authorizeURL = defaultAuthorizeURL
	}
	if p.tokenURL == "" {
		p.tokenURL = defaultTokenURL
	}
	if p.userInfoURL == "" {
		p.userInfoURL = defaultUserInfoURL
	}
	if p.calendarAPIURL == "" {
		p.calendarAPIURL = defaultCalendarAPI
	}
	if p.caldavURL == "" {
		p.caldavURL = defaultCalDAVURL
	}
}

// startJobs schedules the two background jobs cluster-wide, so that a
// multi-node installation polls each person once and sends each message once.
func (p *Plugin) startJobs() error {
	p.jobsLock.Lock()
	defer p.jobsLock.Unlock()

	// A job that has never run starts immediately rather than waiting out its
	// first interval, so activating the plugin polls straight away instead of
	// leaving everyone a minute behind. Only one node does it: the job holds a
	// cluster-wide lock while it runs.
	scheduled := []struct {
		key string
		run func(time.Time)
	}{
		{pollJobKey, p.RunPoll},
		{deliveryJobKey, p.RunDelivery},
	}

	for _, definition := range scheduled {
		run := definition.run
		job, err := cluster.Schedule(p.API, definition.key, cluster.MakeWaitForInterval(tickInterval), func() {
			run(time.Now())
		})
		if err != nil {
			return fmt.Errorf("failed to schedule %s: %w", definition.key, err)
		}
		p.jobs = append(p.jobs, job)
	}
	return nil
}

// OnDeactivate releases everything the plugin holds. Connections and settings
// survive in the key-value store, which is what makes reactivating invisible
// to the people using it.
func (p *Plugin) OnDeactivate() error {
	p.jobsLock.Lock()
	jobs := p.jobs
	p.jobs = nil
	p.jobsLock.Unlock()

	var firstErr error
	for _, job := range jobs {
		if err := job.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if p.httpClient != nil {
		p.httpClient.CloseIdleConnections()
	}
	return firstErr
}

func (p *Plugin) oauth() *oauthClient {
	configuration := p.getConfiguration()
	return &oauthClient{
		httpClient:   p.httpClient,
		authorizeURL: p.authorizeURL,
		tokenURL:     p.tokenURL,
		userInfoURL:  p.userInfoURL,
		clientID:     configuration.ClientID,
		clientSecret: configuration.ClientSecret,
	}
}

// authorizer supplies one person's Authorization header, renewing their
// access token first if it is close to expiry. The renewal happens per request
// so that a token which dies between two requests of the same poll is replaced
// without the caller knowing.
func (p *Plugin) authorizer(connection *Connection, now time.Time) func(context.Context) (string, error) {
	return func(ctx context.Context) (string, error) {
		token, err := p.accessToken(ctx, connection, now)
		if err != nil {
			return "", err
		}
		return "OAuth " + token, nil
	}
}

// accessToken returns a usable access token, refreshing it if it has expired
// or is about to. A refresh that fails because the grant is dead is reported
// as an authentication failure; one that fails because Yandex is unwell is
// reported as transient, and the distinction is what stops an outage from
// disconnecting everybody.
func (p *Plugin) accessToken(ctx context.Context, connection *Connection, now time.Time) (string, error) {
	if connection.ExpiresAt.IsZero() || now.Before(connection.ExpiresAt.Add(-refreshSkew)) {
		return connection.AccessToken, nil
	}
	if connection.RefreshToken == "" {
		return "", &oauthError{StatusCode: http.StatusUnauthorized, Code: "invalid_grant",
			Description: "the Connection has no refresh token"}
	}

	token, err := p.oauth().Refresh(ctx, connection.RefreshToken)
	if err != nil {
		return "", err
	}

	applyToken(connection, token, now)
	if err := p.store.SaveConnection(connection); err != nil {
		return "", err
	}
	return connection.AccessToken, nil
}

// applyToken copies a token response onto a Connection. Yandex returns a fresh
// refresh token with each renewal, but a response that omits one leaves the
// existing one in place rather than discarding it.
func applyToken(connection *Connection, token *tokenResponse, now time.Time) {
	connection.AccessToken = token.AccessToken
	if token.RefreshToken != "" {
		connection.RefreshToken = token.RefreshToken
	}
	if token.ExpiresIn > 0 {
		connection.ExpiresAt = now.Add(time.Duration(token.ExpiresIn) * time.Second)
	} else {
		connection.ExpiresAt = time.Time{}
	}
}

// dm sends a direct message from the plugin's bot to one person. Both message
// types go this way, which is what keeps somebody's Events visible only to
// them.
func (p *Plugin) dm(userID string, post *model.Post) error {
	post.UserId = p.botID
	return p.client.Post.DM(p.botID, userID, post)
}

// userLocation is the timezone a person reads times in. Falling back to UTC is
// better than failing: a Reminder in the wrong timezone still names the right
// Event, and the fallback only applies to an account with no preference set.
func (p *Plugin) userLocation(userID string) *time.Location {
	user, err := p.client.User.Get(userID)
	if err != nil {
		return time.UTC
	}
	if loc := user.GetTimezoneLocation(); loc != nil {
		return loc
	}
	return time.UTC
}
