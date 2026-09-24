package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/mattermost/mattermost/server/public/pluginapi"

	"github.com/Maunty/mattermost-ya-calendar/server/internal/fakecaldav"
)

// The tests in this package run against two substitutions and nothing else:
// a fake CalDAV server below the wire, and the plugin API mock above. Between
// them everything is the real implementation — discovery, collection
// filtering, change-tag comparison, query construction, iCalendar parsing,
// recurrence expansion, scheduling and rendering — and every assertion is
// about the messages that came out.
//
// The clock is a value. Job entry points take the moment to run at, so a test
// says "at 09:50 on this day" rather than mocking time.

const (
	testUserID  = "user1234567890123456789012"
	testBotID   = "bot12345678901234567890123"
	testChannel = "channel12345678901234567890"
)

// testAPI is the standard plugin test mock with the parts these tests rely on
// given real behaviour: a key-value store that honours atomic writes and
// expiry, user lookups, and posts that are recorded rather than sent.
type testAPI struct {
	*plugintest.API

	mu        sync.Mutex
	kv        map[string]kvEntry
	posts     []*model.Post
	ephemeral []*model.Post
	logs      []string

	user     *model.User
	siteURL  string
	failNext map[string]int
}

type kvEntry struct {
	value     []byte
	expiresAt time.Time
}

func newTestAPI() *testAPI {
	return &testAPI{
		API:      &plugintest.API{},
		kv:       map[string]kvEntry{},
		siteURL:  "https://mattermost.example.com",
		failNext: map[string]int{},
		user: &model.User{
			Id:       testUserID,
			Username: "sam",
			Timezone: map[string]string{
				"useAutomaticTimezone": "false",
				"manualTimezone":       "Europe/Moscow",
				"automaticTimezone":    "",
			},
		},
	}
}

func (a *testAPI) setTimezone(name string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.user.Timezone["manualTimezone"] = name
}

// Key-value store, with the semantics the real one has.

func (a *testAPI) KVGet(key string) ([]byte, *model.AppError) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.get(key), nil
}

func (a *testAPI) get(key string) []byte {
	entry, ok := a.kv[key]
	if !ok {
		return nil
	}
	if !entry.expiresAt.IsZero() && time.Now().After(entry.expiresAt) {
		delete(a.kv, key)
		return nil
	}
	return entry.value
}

func (a *testAPI) KVSetWithOptions(key string, value []byte, options model.PluginKVSetOptions) (bool, *model.AppError) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if options.Atomic && !bytes.Equal(a.get(key), options.OldValue) {
		return false, nil
	}
	if value == nil {
		delete(a.kv, key)
		return true, nil
	}

	entry := kvEntry{value: value}
	if options.ExpireInSeconds > 0 {
		entry.expiresAt = time.Now().Add(time.Duration(options.ExpireInSeconds) * time.Second)
	}
	a.kv[key] = entry
	return true, nil
}

func (a *testAPI) KVDelete(key string) *model.AppError {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.kv, key)
	return nil
}

func (a *testAPI) KVList(page, perPage int) ([]string, *model.AppError) {
	a.mu.Lock()
	defer a.mu.Unlock()
	keys := make([]string, 0, len(a.kv))
	for key := range a.kv {
		keys = append(keys, key)
	}
	start := page * perPage
	if start >= len(keys) {
		return nil, nil
	}
	end := min(start+perPage, len(keys))
	return keys[start:end], nil
}

// Users, channels and posts.

func (a *testAPI) GetUser(userID string) (*model.User, *model.AppError) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if userID != a.user.Id {
		return &model.User{Id: userID}, nil
	}
	return a.user, nil
}

func (a *testAPI) GetDirectChannel(userID1, userID2 string) (*model.Channel, *model.AppError) {
	return &model.Channel{Id: "dm_" + userID1 + "_" + userID2, Type: model.ChannelTypeDirect}, nil
}

func (a *testAPI) CreatePost(post *model.Post) (*model.Post, *model.AppError) {
	a.mu.Lock()
	defer a.mu.Unlock()

	// The real API answers with the post it stored, which is a different
	// object from the one it was handed. Handing the same pointer back
	// deadlocks the caller.
	stored := post.Clone()
	stored.Id = model.NewId()
	a.posts = append(a.posts, stored)
	return stored, nil
}

func (a *testAPI) SendEphemeralPost(userID string, post *model.Post) *model.Post {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.ephemeral = append(a.ephemeral, post)
	return post
}

func (a *testAPI) EnsureBotUser(bot *model.Bot) (string, error) { return testBotID, nil }

// GetServerVersion is what the client library checks features against. The
// plugin declares a minimum in the 11 series, so that is what it runs on.
func (a *testAPI) GetServerVersion() string { return "11.0.0" }

func (a *testAPI) RegisterCommand(command *model.Command) error { return nil }

func (a *testAPI) UnregisterCommand(teamID, trigger string) error { return nil }

func (a *testAPI) GetConfig() *model.Config {
	a.mu.Lock()
	siteURL := a.siteURL
	a.mu.Unlock()
	config := &model.Config{}
	config.SetDefaults()
	config.ServiceSettings.SiteURL = &siteURL
	return config
}

func (a *testAPI) LoadPluginConfiguration(dest any) error { return nil }

// Logging is captured rather than mocked, so a test can assert that something
// was logged instead of shown to a person.

func (a *testAPI) LogDebug(msg string, keyValuePairs ...any) { a.log("debug", msg, keyValuePairs) }
func (a *testAPI) LogInfo(msg string, keyValuePairs ...any)  { a.log("info", msg, keyValuePairs) }
func (a *testAPI) LogWarn(msg string, keyValuePairs ...any)  { a.log("warn", msg, keyValuePairs) }
func (a *testAPI) LogError(msg string, keyValuePairs ...any) { a.log("error", msg, keyValuePairs) }

func (a *testAPI) log(level, msg string, keyValuePairs []any) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.logs = append(a.logs, fmt.Sprintf("%s: %s %v", level, msg, keyValuePairs))
}

// fakeYandex stands in for the OAuth and identity endpoints.
type fakeYandex struct {
	*httptest.Server

	mu            sync.Mutex
	login         string
	accountID     string
	accessToken   string
	refreshToken  string
	expiresIn     int64
	tokenStatus   int
	tokenError    string
	exchanges     int
	refreshes     int
	lastGrantType string
}

func newFakeYandex(t *testing.T) *fakeYandex {
	y := &fakeYandex{
		login:        "sam@yandex.ru",
		accountID:    "1000123456",
		accessToken:  "test-access-token",
		refreshToken: "test-refresh-token",
		expiresIn:    365 * 24 * 3600,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/token", y.handleToken)
	mux.HandleFunc("/info", y.handleInfo)
	y.Server = httptest.NewServer(mux)
	t.Cleanup(y.Close)
	return y
}

func (y *fakeYandex) handleToken(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()

	y.mu.Lock()
	defer y.mu.Unlock()

	y.lastGrantType = r.Form.Get("grant_type")
	switch y.lastGrantType {
	case "authorization_code":
		y.exchanges++
	case "refresh_token":
		y.refreshes++
	}

	if y.tokenStatus != 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(y.tokenStatus)
		fmt.Fprintf(w, `{"error":%q,"error_description":"no"}`, y.tokenError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"access_token":  y.accessToken,
		"refresh_token": y.refreshToken,
		"expires_in":    y.expiresIn,
		"token_type":    "bearer",
	})
}

func (y *fakeYandex) handleInfo(w http.ResponseWriter, r *http.Request) {
	y.mu.Lock()
	defer y.mu.Unlock()

	if r.Header.Get("Authorization") != "OAuth "+y.accessToken {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"id": y.accountID, "login": y.login})
}

func (y *fakeYandex) failTokenWith(status int, code string) {
	y.mu.Lock()
	defer y.mu.Unlock()
	y.tokenStatus, y.tokenError = status, code
}

func (y *fakeYandex) recoverToken() {
	y.mu.Lock()
	defer y.mu.Unlock()
	y.tokenStatus, y.tokenError = 0, ""
}

// harness is one plugin, one person, one Yandex account and one calendar
// server, wired together the way they are in production.
type harness struct {
	t      *testing.T
	plugin *Plugin
	api    *testAPI
	caldav *fakecaldav.Server
	yandex *fakeYandex
}

func newHarness(t *testing.T) *harness {
	t.Helper()

	api := newTestAPI()
	yandex := newFakeYandex(t)
	calendarServer := fakecaldav.New(t)

	p := &Plugin{
		authorizeURL: yandex.URL + "/authorize",
		tokenURL:     yandex.URL + "/token",
		userInfoURL:  yandex.URL + "/info",
		caldavURL:    calendarServer.URL,
		httpClient:   calendarServer.Client(),
		botID:        testBotID,
	}
	p.SetAPI(api)
	p.client = pluginapi.NewClient(api, nil)
	p.store = NewStore(&p.client.KV, func() string { return p.getConfiguration().EncryptionKey })
	p.setConfiguration(&configuration{
		ClientID:            "client-id",
		ClientSecret:        "client-secret",
		ReminderLeadMinutes: 10,
		EncryptionKey:       "an-encryption-key-for-tests",
	})

	h := &harness{t: t, plugin: p, api: api, caldav: calendarServer, yandex: yandex}
	h.holdBackScheduledJobs()
	t.Cleanup(func() { _ = p.OnDeactivate() })
	return h
}

// holdBackScheduledJobs stops the background jobs from firing during a test.
//
// A job that has never run starts the moment it is scheduled, which is right
// in production and useless here: it would run against the real wall clock
// while the test drives the plugin at the times it chose, and whatever it did
// would land in the middle of the assertions. Recording that both jobs
// finished a moment ago puts their next run a full interval away, past the end
// of any test, using the scheduler's own mechanism rather than a flag in the
// plugin. Tests drive RunPoll and RunDelivery directly instead.
func (h *harness) holdBackScheduledJobs() {
	h.t.Helper()
	metadata, err := json.Marshal(map[string]any{"LastFinished": time.Now()})
	if err != nil {
		h.t.Fatalf("marshalling job metadata: %v", err)
	}

	h.api.mu.Lock()
	defer h.api.mu.Unlock()
	for _, key := range []string{pollJobKey, deliveryJobKey} {
		h.api.kv["cron_"+key] = kvEntry{value: metadata}
	}
}

// connect puts the person through the real consent flow, callback and all.
func (h *harness) connect(now time.Time) {
	h.t.Helper()

	response := h.command("/yacal connect", now)
	link := linkFrom(h.t, response.Text)

	state := queryValue(h.t, link, "state")
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet,
		"/oauth/complete?code=an-authorization-code&state="+state, nil)
	request.Header.Set("Mattermost-User-ID", testUserID)
	h.plugin.handleOAuthComplete(recorder, request, now)

	if recorder.Code != http.StatusOK {
		h.t.Fatalf("the consent callback answered %d: %s", recorder.Code, recorder.Body.String())
	}
	h.clearPosts()
}

// calendar adds a collection of events shaped the way a real account's is.
func (h *harness) calendar(name string) *fakecaldav.Collection {
	return h.caldav.AddCalendar(name, name)
}

func (h *harness) command(command string, now time.Time) *model.CommandResponse {
	h.t.Helper()
	return h.plugin.executeCommand(h.t.Context(), &model.CommandArgs{
		Command:   command,
		UserId:    testUserID,
		ChannelId: testChannel,
	}, now)
}

// messages are the direct messages the bot sent.
func (h *harness) messages() []*model.Post {
	h.api.mu.Lock()
	defer h.api.mu.Unlock()
	out := make([]*model.Post, len(h.api.posts))
	copy(out, h.api.posts)
	return out
}

func (h *harness) ephemeralPosts() []*model.Post {
	h.api.mu.Lock()
	defer h.api.mu.Unlock()
	out := make([]*model.Post, len(h.api.ephemeral))
	copy(out, h.api.ephemeral)
	return out
}

// reminders are the direct messages that are Reminders. A Reminder carries an
// attachment and a Daily Summary does not, which is how they are told apart
// without either test knowing how the other is worded.
func (h *harness) reminders() []*model.Post {
	var out []*model.Post
	for _, post := range h.messages() {
		if post.Type == model.PostTypeMessageAttachment {
			out = append(out, post)
		}
	}
	return out
}

// summaries are the direct messages that are not Reminders.
func (h *harness) summaries() []*model.Post {
	var out []*model.Post
	for _, post := range h.messages() {
		if post.Type != model.PostTypeMessageAttachment {
			out = append(out, post)
		}
	}
	return out
}

func (h *harness) clearPosts() {
	h.api.mu.Lock()
	defer h.api.mu.Unlock()
	h.api.posts, h.api.ephemeral = nil, nil
}

func (h *harness) logged() []string {
	h.api.mu.Lock()
	defer h.api.mu.Unlock()
	out := make([]string, len(h.api.logs))
	copy(out, h.api.logs)
	return out
}

// text is everything one post says, message and attachment alike, so that an
// assertion does not have to know which of the two a detail landed in.
func text(post *model.Post) string {
	var b strings.Builder
	b.WriteString(post.Message)

	if attachments := post.GetProp("attachments"); attachments != nil {
		encoded, _ := json.Marshal(attachments)
		b.WriteString(" ")
		b.Write(encoded)
	}
	return b.String()
}

func allText(posts []*model.Post) string {
	var b strings.Builder
	for _, post := range posts {
		b.WriteString(text(post))
		b.WriteString("\n")
	}
	return b.String()
}

func linkFrom(t *testing.T, message string) string {
	t.Helper()
	open := strings.Index(message, "](")
	if open < 0 {
		t.Fatalf("no link in: %s", message)
	}
	rest := message[open+2:]
	close := strings.Index(rest, ")")
	if close < 0 {
		t.Fatalf("unterminated link in: %s", message)
	}
	return rest[:close]
}

func queryValue(t *testing.T, rawURL, name string) string {
	t.Helper()
	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("unusable URL %q: %v", rawURL, err)
	}
	value := parsed.Query().Get(name)
	if value == "" {
		t.Fatalf("no %s in %q", name, rawURL)
	}
	return value
}
