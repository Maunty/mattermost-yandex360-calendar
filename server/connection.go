package main

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
	"time"
)

// What people are told. Nobody is ever shown a status code, a stack trace or
// the word "CalDAV": these messages say what happened and what to do about it.
const (
	notConnectedMessage = "Your Yandex Calendar is not connected yet. Run `/yacal connect` to connect it."
	reconnectMessage    = "I can no longer read your Yandex Calendar — the access you granted has been withdrawn or has expired. Run `/yacal connect` to reconnect."
	providerDownMessage = "I could not reach Yandex just now. Your connection is fine and I will try again shortly."
)

// authFailuresBeforeInactive is how many consecutive refusals retire a
// Connection. One is not enough: a single odd answer during a provider outage
// would otherwise disconnect everybody at once.
const authFailuresBeforeInactive = 2

func isNotConnected(err error) bool { return errors.Is(err, errNotConnected) }

// connectionProblemMessage turns whatever went wrong reading a Connection into
// something worth reading.
func (p *Plugin) connectionProblemMessage(err error) string {
	if isNotConnected(err) {
		return notConnectedMessage
	}
	// An unreadable Connection is almost always a regenerated encryption key.
	p.client.Log.Warn("Could not read a Connection", "error", err.Error())
	return "I could not read your connection details. Run `/yacal connect` to reconnect."
}

// readFailureMessage explains a failed read without handing over the
// technical detail, which belongs in the server log and nowhere else.
func (p *Plugin) readFailureMessage(err error) string {
	if isAuthFailure(err) {
		return reconnectMessage
	}
	return providerDownMessage
}

// connectLink builds the URL that starts a consent flow, storing a one-time
// state value first. The state is the defence against a forged callback, and
// it is not optional.
func (p *Plugin) connectLink(userID string) (string, error) {
	state, err := randomState()
	if err != nil {
		return "", err
	}
	if err := p.store.StoreOAuthState(state, userID, oauthStateTTL); err != nil {
		return "", err
	}
	return p.oauth().AuthorizeURL(state, p.redirectURI()), nil
}

func randomState() (string, error) {
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

// redirectURI is where Yandex sends the person back to. It has to match what
// is registered on the Yandex application exactly.
func (p *Plugin) redirectURI() string {
	return strings.TrimRight(p.siteURL(), "/") + "/plugins/" + manifestID + "/oauth/complete"
}

func (p *Plugin) siteURL() string {
	config := p.client.Configuration.GetConfig()
	if config != nil && config.ServiceSettings.SiteURL != nil {
		return *config.ServiceSettings.SiteURL
	}
	return ""
}

// recordSuccess notes that the provider answered, which revives a Connection
// that had been refused and clears the failure count, because "two consecutive
// failures" means consecutive.
//
// It touches only the Connection. When the calendar was last read belongs to
// the sync state, and is recorded by whoever holds it: the poll sets it on the
// state it is already working with, and a read on demand calls
// noteCalendarRead.
func (p *Plugin) recordSuccess(connection *Connection) {
	changed := false
	if connection.AuthFailures != 0 {
		connection.AuthFailures, changed = 0, true
	}
	if !connection.Active {
		connection.Active, connection.InactiveNoticeSent, changed = true, false, true
	}
	if !changed {
		return
	}
	if err := p.store.SaveConnection(connection); err != nil {
		p.client.Log.Error("Could not save a Connection after a successful read", "error", err.Error())
	}
}

// noteCalendarRead records when a person's calendar was last read, so they can
// tell an empty day from a Connection that has quietly stopped working. The
// poll does not use this: it already holds the state it is about to save.
func (p *Plugin) noteCalendarRead(userID string, now time.Time) {
	state, err := p.store.SyncState(userID)
	if err != nil {
		p.client.Log.Warn("Could not read sync state", "user_id", userID, "error", err.Error())
		return
	}
	state.LastSuccessAt = now
	if err := p.store.SaveSyncState(userID, state); err != nil {
		p.client.Log.Warn("Could not record when a calendar was last read", "error", err.Error())
	}
}

// recordFailure decides what a failed read means for a Connection. Only the
// provider refusing the credential itself counts against it; being unreachable
// or briefly broken does not, and must not, because otherwise an outage costs
// every user their Connection.
func (p *Plugin) recordFailure(connection *Connection, err error) {
	if !isAuthFailure(err) {
		return
	}

	connection.AuthFailures++
	if connection.AuthFailures >= authFailuresBeforeInactive {
		connection.Active = false
	}
	if saveErr := p.store.SaveConnection(connection); saveErr != nil {
		p.client.Log.Error("Could not save a Connection after a failed read", "error", saveErr.Error())
		return
	}

	if !connection.Active && !connection.InactiveNoticeSent {
		p.notifyInactive(connection)
	}
}

// notifyInactive tells somebody their Connection is dead, once, with a way to
// fix it. Finding out from the plugin is the whole point: the alternative is
// finding out from a missed meeting.
func (p *Plugin) notifyInactive(connection *Connection) {
	message := fmt.Sprintf(
		"I have stopped reading your Yandex Calendar as **%s**: Yandex is no longer accepting the access you granted, "+
			"which usually means it was withdrawn or has expired.\n\nRun `/yacal connect` to reconnect. "+
			"Until then I will not send you reminders or a daily summary.",
		sanitise(connection.YandexLogin))

	if err := p.dm(connection.MattermostUserID, &model.Post{Message: message}); err != nil {
		p.client.Log.Error("Could not tell a user their Connection is inactive",
			"user_id", connection.MattermostUserID, "error", err.Error())
		return
	}

	connection.InactiveNoticeSent = true
	if err := p.store.SaveConnection(connection); err != nil {
		p.client.Log.Error("Could not record that a reconnect notice was sent", "error", err.Error())
	}
}

// completionPage is what the consent window shows before closing itself. It
// closes on success and, when a client will not let it, says so plainly rather
// than appearing to hang.
func completionPage(heading, detail string) string {
	return fmt.Sprintf(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>Yandex Calendar</title>
<style>
  body { font-family: -apple-system, "Segoe UI", Roboto, sans-serif; margin: 0;
         display: flex; align-items: center; justify-content: center; height: 100vh;
         background: #f7f8fa; color: #1f2430; }
  main { max-width: 32rem; padding: 2rem; text-align: center; }
  h1 { font-size: 1.25rem; margin: 0 0 0.75rem; }
  p { margin: 0 0 0.5rem; line-height: 1.5; }
  .quiet { color: #5a6270; font-size: 0.9rem; }
</style>
</head>
<body>
<main>
  <h1>%s</h1>
  <p>%s</p>
  <p class="quiet" id="closing">You can close this window and go back to Mattermost.</p>
</main>
<script>
  // Some clients refuse to close a window they did not open. Try, and leave
  // the message above standing when it does not work.
  setTimeout(function () { try { window.close(); } catch (e) {} }, 1200);
</script>
</body>
</html>`, escapeHTML(heading), escapeHTML(detail))
}

func escapeHTML(s string) string {
	return strings.NewReplacer(
		"&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&#39;",
	).Replace(s)
}

// safeQueryValue reads a query parameter without trusting its contents.
func safeQueryValue(values url.Values, name string) string {
	return strings.TrimSpace(values.Get(name))
}
