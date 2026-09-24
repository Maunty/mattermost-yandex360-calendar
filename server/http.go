package main

import (
	"fmt"
	"net/http"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
)

// manifestID must match the id in plugin.json: it is half of the URL Yandex
// redirects back to.
const manifestID = "yandex-calendar"

func (p *Plugin) ServeHTTP(_ *plugin.Context, w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/oauth/complete":
		p.handleOAuthComplete(w, r, time.Now())
	default:
		http.NotFound(w, r)
	}
}

func (p *Plugin) handleOAuthComplete(w http.ResponseWriter, r *http.Request, now time.Time) {
	query := r.URL.Query()

	if reason := safeQueryValue(query, "error"); reason != "" {
		// The person declined, or Yandex refused. Either way this is not an
		// error to be debugged, it is an answer.
		writePage(w, http.StatusOK, completionPage(
			"Not connected",
			"Yandex did not grant access, so nothing has been connected. You can run /yacal connect again whenever you like."))
		return
	}

	// The state value proves this callback belongs to a flow this plugin
	// started, for this person. It is read once and destroyed, so a replayed,
	// forged, expired or already-used callback connects nothing.
	userID, err := p.store.ConsumeOAuthState(safeQueryValue(query, "state"))
	if err != nil {
		p.client.Log.Warn("Rejected an OAuth callback", "error", err.Error())
		writePage(w, http.StatusUnauthorized, completionPage(
			"Not connected",
			"This sign-in link is no longer valid. Please run /yacal connect in Mattermost and use the new link."))
		return
	}

	// When the browser is signed in to Mattermost, it must be signed in as the
	// person who started the flow.
	if sessionUserID := r.Header.Get("Mattermost-User-ID"); sessionUserID != "" && sessionUserID != userID {
		p.client.Log.Warn("An OAuth callback arrived for a different user than started it")
		writePage(w, http.StatusUnauthorized, completionPage(
			"Not connected",
			"This sign-in belongs to a different Mattermost account. Please run /yacal connect again."))
		return
	}

	code := safeQueryValue(query, "code")
	if code == "" {
		writePage(w, http.StatusBadRequest, completionPage(
			"Not connected",
			"Yandex did not send back an authorization code. Please run /yacal connect again."))
		return
	}

	oauth := p.oauth()
	token, err := oauth.Exchange(r.Context(), code, p.redirectURI())
	if err != nil {
		p.client.Log.Error("Could not exchange an authorization code", "error", err.Error())
		writePage(w, http.StatusBadGateway, completionPage(
			"Not connected",
			"Yandex would not complete the sign-in. Please run /yacal connect again in a moment."))
		return
	}

	account, err := oauth.Account(r.Context(), token.AccessToken)
	if err != nil {
		p.client.Log.Error("Could not read the connected account", "error", err.Error())
		writePage(w, http.StatusBadGateway, completionPage(
			"Not connected",
			"Yandex would not say which account this is. Please run /yacal connect again in a moment."))
		return
	}

	connection := &Connection{
		MattermostUserID: userID,
		YandexLogin:      account.Login,
		YandexUserID:     account.ID,
		ConnectedAt:      now,
		Active:           true,
	}
	applyToken(connection, token, now)

	if err := p.store.SaveConnection(connection); err != nil {
		p.client.Log.Error("Could not save a Connection", "error", err.Error())
		writePage(w, http.StatusInternalServerError, completionPage(
			"Not connected",
			"Something went wrong storing the connection. Please run /yacal connect again in a moment."))
		return
	}

	p.welcome(connection)

	writePage(w, http.StatusOK, completionPage(
		"Connected",
		fmt.Sprintf("Your Yandex Calendar is connected as %s.", account.Login)))
}

// welcome tells the person which account connected, so that somebody with
// several Yandex accounts can see at once whether they picked the right one.
func (p *Plugin) welcome(connection *Connection) {
	settings, err := p.store.Settings(connection.MattermostUserID)
	if err != nil {
		settings = Settings{}
	}
	lead := humaniseLead(p.getConfiguration().ReminderLead())

	message := fmt.Sprintf(
		"Your Yandex Calendar is connected as **%s**.\n\n"+
			"I will send you a reminder %s before each event, and a summary of your day at %s in your own timezone.\n"+
			"Run `/yacal settings` to change either, `/yacal today` to see today, and `/yacal disconnect` to stop.\n\n"+
			"I only ever read your calendar.",
		sanitise(connection.YandexLogin), lead, settings.SummaryTimeText())

	if err := p.dm(connection.MattermostUserID, &model.Post{Message: message}); err != nil {
		p.client.Log.Warn("Could not send the welcome message", "error", err.Error())
	}
}

func writePage(w http.ResponseWriter, status int, html string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(html))
}
