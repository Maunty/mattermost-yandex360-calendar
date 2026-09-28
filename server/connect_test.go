package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Ticket 03: connecting and disconnecting a Yandex account.

func TestConnectOffersALinkToYandexConsent(t *testing.T) {
	h := newHarness(t)

	response := h.command("/yacal connect", moment(2026, 9, 24, 9, 0))

	link := linkFrom(t, response.Text)
	if !strings.HasPrefix(link, h.yandex.URL+"/authorize") {
		t.Fatalf("the link does not go to Yandex's consent screen: %s", link)
	}
	if got := queryValue(t, link, "response_type"); got != "code" {
		t.Errorf("response_type: %q", got)
	}
	if got := queryValue(t, link, "client_id"); got != "client-id" {
		t.Errorf("client_id: %q", got)
	}
	if got := queryValue(t, link, "redirect_uri"); !strings.HasSuffix(got, "/plugins/yandex-calendar/oauth/complete") {
		t.Errorf("redirect_uri: %q", got)
	}
}

func TestTheScopeRequestedIsReadOnly(t *testing.T) {
	// Probing showed the REST API reads everything the plugin needs with
	// calendar:events.read alone, identity included, and refuses calendar:all
	// outright. So the consent asks to read Events and nothing more.
	h := newHarness(t)

	response := h.command("/yacal connect", moment(2026, 9, 24, 9, 0))

	if got := queryValue(t, linkFrom(t, response.Text), "scope"); got != "calendar:events.read" {
		t.Errorf("scope: got %q, want calendar:events.read", got)
	}
}

func TestConnectSaysTheAccessIsReadOnly(t *testing.T) {
	// The consent screen now asks only to read, and the message beside the
	// link must not tell people otherwise.
	h := newHarness(t)

	response := h.command("/yacal connect", moment(2026, 9, 24, 9, 0))

	if !strings.Contains(response.Text, "read-only") {
		t.Errorf("the connect message does not say the access is read-only:\n%s", response.Text)
	}
	if strings.Contains(response.Text, "change your calendar") {
		t.Errorf("the connect message still warns about a write permission:\n%s", response.Text)
	}
}

func TestApprovingConsentConnectsTheAccountAndNamesIt(t *testing.T) {
	h := newHarness(t)
	now := moment(2026, 9, 24, 9, 0)

	response := h.command("/yacal connect", now)
	state := queryValue(t, linkFrom(t, response.Text), "state")

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/oauth/complete?code=abc&state="+state, nil)
	request.Header.Set("Mattermost-User-ID", testUserID)
	h.plugin.handleOAuthComplete(recorder, request, now)

	if recorder.Code != http.StatusOK {
		t.Fatalf("callback answered %d: %s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "sam@yandex.ru") {
		t.Errorf("the page does not say which account connected:\n%s", recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "window.close") {
		t.Error("the page does not try to close itself")
	}
	if !strings.Contains(recorder.Body.String(), "close this window") {
		t.Error("the page does not degrade to telling the person to close it, so a client that " +
			"refuses to close a window would leave them looking at a blank page")
	}

	messages := h.messages()
	if len(messages) != 1 {
		t.Fatalf("got %d messages, want one confirming the connection", len(messages))
	}
	if !strings.Contains(messages[0].Message, "sam@yandex.ru") {
		t.Errorf("the confirmation does not name the account: %s", messages[0].Message)
	}
}

func TestTokensAreEncryptedAtRest(t *testing.T) {
	h := newHarness(t)
	h.connect(moment(2026, 9, 24, 9, 0))

	h.api.mu.Lock()
	defer h.api.mu.Unlock()
	for key, entry := range h.api.kv {
		if strings.Contains(string(entry.value), "test-access-token") ||
			strings.Contains(string(entry.value), "test-refresh-token") {
			t.Fatalf("the token is stored in the clear under %q", key)
		}
	}
}

func TestTheConnectionIsReadableBackFromStorage(t *testing.T) {
	h := newHarness(t)
	h.connect(moment(2026, 9, 24, 9, 0))

	connection, err := h.plugin.store.Connection(testUserID)
	if err != nil {
		t.Fatalf("Connection: %v", err)
	}
	if connection.YandexLogin != "sam@yandex.ru" || connection.AccessToken != "test-access-token" {
		t.Errorf("stored Connection: %+v", connection)
	}
	if !connection.Active {
		t.Error("a Connection that was just made is not active")
	}
}

func TestAStateValueWorksOnlyOnce(t *testing.T) {
	h := newHarness(t)
	now := moment(2026, 9, 24, 9, 0)
	state := queryValue(t, linkFrom(t, h.command("/yacal connect", now).Text), "state")

	first := httptest.NewRecorder()
	h.plugin.handleOAuthComplete(first, httptest.NewRequest(http.MethodGet, "/oauth/complete?code=abc&state="+state, nil), now)
	if first.Code != http.StatusOK {
		t.Fatalf("the first callback answered %d", first.Code)
	}

	second := httptest.NewRecorder()
	h.plugin.handleOAuthComplete(second, httptest.NewRequest(http.MethodGet, "/oauth/complete?code=abc&state="+state, nil), now)
	if second.Code == http.StatusOK {
		t.Error("a state value was accepted a second time, so a callback can be replayed")
	}
}

func TestACallbackWithAnUnknownStateConnectsNothing(t *testing.T) {
	h := newHarness(t)

	for _, state := range []string{"", "not-a-state-this-plugin-issued"} {
		recorder := httptest.NewRecorder()
		h.plugin.handleOAuthComplete(recorder,
			httptest.NewRequest(http.MethodGet, "/oauth/complete?code=abc&state="+state, nil),
			moment(2026, 9, 24, 9, 0))

		if recorder.Code == http.StatusOK {
			t.Errorf("state %q was accepted", state)
		}
	}
	if _, err := h.plugin.store.Connection(testUserID); !isNotConnected(err) {
		t.Error("a Connection was established by a callback with no valid state")
	}
}

func TestAnExpiredStateValueConnectsNothing(t *testing.T) {
	h := newHarness(t)
	now := moment(2026, 9, 24, 9, 0)
	state := queryValue(t, linkFrom(t, h.command("/yacal connect", now).Text), "state")

	// The one-time store expires on its own; reaching in and removing the
	// entry is the same thing happening.
	h.api.mu.Lock()
	delete(h.api.kv, oauthStateKeyPrefix+state)
	h.api.mu.Unlock()

	recorder := httptest.NewRecorder()
	h.plugin.handleOAuthComplete(recorder,
		httptest.NewRequest(http.MethodGet, "/oauth/complete?code=abc&state="+state, nil), now)

	if recorder.Code == http.StatusOK {
		t.Error("an expired state value was accepted")
	}
}

func TestACallbackFromADifferentMattermostAccountIsRejected(t *testing.T) {
	h := newHarness(t)
	now := moment(2026, 9, 24, 9, 0)
	state := queryValue(t, linkFrom(t, h.command("/yacal connect", now).Text), "state")

	request := httptest.NewRequest(http.MethodGet, "/oauth/complete?code=abc&state="+state, nil)
	request.Header.Set("Mattermost-User-ID", "someone12345678901234567890")
	recorder := httptest.NewRecorder()
	h.plugin.handleOAuthComplete(recorder, request, now)

	if recorder.Code == http.StatusOK {
		t.Error("a callback was accepted for a different Mattermost account than started the flow")
	}
}

func TestDecliningConsentIsNotTreatedAsAFailure(t *testing.T) {
	h := newHarness(t)

	recorder := httptest.NewRecorder()
	h.plugin.handleOAuthComplete(recorder,
		httptest.NewRequest(http.MethodGet, "/oauth/complete?error=access_denied&state=x", nil),
		moment(2026, 9, 24, 9, 0))

	if recorder.Code != http.StatusOK {
		t.Errorf("declining answered %d, which reads as something going wrong", recorder.Code)
	}
	if !strings.Contains(recorder.Body.String(), "Not connected") {
		t.Errorf("the page does not say nothing was connected:\n%s", recorder.Body.String())
	}
}

func TestConnectingWhileAlreadyConnectedSaysSo(t *testing.T) {
	h := newHarness(t)
	now := moment(2026, 9, 24, 9, 0)
	h.connect(now)

	response := h.command("/yacal connect", now)

	if !strings.Contains(response.Text, "already connected") {
		t.Errorf("running connect twice did not say so:\n%s", response.Text)
	}
	if strings.Contains(response.Text, "/authorize") {
		t.Error("running connect twice started a second consent flow")
	}
}

func TestDisconnectForgetsEverythingAndSaysSo(t *testing.T) {
	h := newHarness(t)
	now := moment(2026, 9, 24, 9, 0)
	h.connect(now)

	response := h.command("/yacal disconnect", now)

	if !strings.Contains(response.Text, "disconnected") {
		t.Errorf("disconnect did not confirm: %s", response.Text)
	}
	if _, err := h.plugin.store.Connection(testUserID); !isNotConnected(err) {
		t.Errorf("the Connection survived disconnect: %v", err)
	}

	h.api.mu.Lock()
	defer h.api.mu.Unlock()
	for key, entry := range h.api.kv {
		if strings.HasPrefix(key, connectionKeyPrefix) && len(entry.value) > 0 {
			t.Errorf("a stored Connection is left behind under %q", key)
		}
	}
}

func TestDisconnectStopsAllMessages(t *testing.T) {
	h := newHarness(t)
	now := moment(2026, 9, 24, 9, 0)
	h.connect(now)
	calendar := h.calendar("events-1000001")
	calendar.Put("soon", timedEvent("soon", "Standup", moment(2026, 9, 24, 10, 0), moment(2026, 9, 24, 10, 30)))
	h.plugin.RunPoll(now)

	h.command("/yacal disconnect", now)
	h.clearPosts()
	h.plugin.RunPoll(moment(2026, 9, 24, 9, 50))
	h.plugin.RunDelivery(moment(2026, 9, 24, 9, 50))

	if messages := h.messages(); len(messages) != 0 {
		t.Errorf("a disconnected person still got %d messages:\n%s", len(messages), allText(messages))
	}
}

func TestACommandWithoutAConnectionExplainsHowToConnect(t *testing.T) {
	h := newHarness(t)
	now := moment(2026, 9, 24, 9, 0)

	for _, command := range []string{"/yacal today", "/yacal settings", "/yacal summary off", "/yacal disconnect"} {
		response := h.command(command, now)
		if !strings.Contains(response.Text, "/yacal connect") {
			t.Errorf("%s did not say how to connect: %s", command, response.Text)
		}
	}
}

func TestConnectedUsersAreIndexedForTheBackgroundJobs(t *testing.T) {
	h := newHarness(t)
	h.connect(moment(2026, 9, 24, 9, 0))

	userIDs, err := h.plugin.store.ConnectedUserIDs()
	if err != nil {
		t.Fatalf("ConnectedUserIDs: %v", err)
	}
	if len(userIDs) != 1 || userIDs[0] != testUserID {
		t.Fatalf("the index holds %v", userIDs)
	}

	h.command("/yacal disconnect", moment(2026, 9, 24, 9, 0))
	userIDs, _ = h.plugin.store.ConnectedUserIDs()
	if len(userIDs) != 0 {
		t.Errorf("a disconnected person is still in the index: %v", userIDs)
	}
}

func TestAConnectionSurvivesDeactivationAndReactivation(t *testing.T) {
	h := newHarness(t)
	now := moment(2026, 9, 24, 9, 0)
	h.connect(now)

	if err := h.plugin.OnDeactivate(); err != nil {
		t.Fatalf("OnDeactivate: %v", err)
	}
	if err := h.plugin.OnActivate(); err != nil {
		t.Fatalf("OnActivate: %v", err)
	}

	connection, err := h.plugin.store.Connection(testUserID)
	if err != nil {
		t.Fatalf("the Connection did not survive: %v", err)
	}
	if connection.YandexLogin != "sam@yandex.ru" {
		t.Errorf("stored Connection: %+v", connection)
	}
}

func TestAnExpiredAccessTokenIsRenewedWithoutTheUser(t *testing.T) {
	h := newHarness(t)
	connectedAt := moment(2026, 9, 24, 9, 0)
	h.yandex.expiresIn = 3600
	h.connect(connectedAt)
	h.calendar("events-1000001")

	// An hour later the access token has expired.
	later := connectedAt.Add(2 * time.Hour)
	h.yandex.accessToken = "a-renewed-access-token"
	h.provider.Token = "a-renewed-access-token"

	response := h.command("/yacal today", later)

	if strings.Contains(response.Text, "reconnect") {
		t.Fatalf("the person was asked to reconnect instead of the token being renewed: %s", response.Text)
	}
	if h.yandex.refreshes != 1 {
		t.Errorf("the token was refreshed %d times, want once", h.yandex.refreshes)
	}
	connection, _ := h.plugin.store.Connection(testUserID)
	if connection.AccessToken != "a-renewed-access-token" {
		t.Errorf("the renewed token was not stored: %q", connection.AccessToken)
	}
}
