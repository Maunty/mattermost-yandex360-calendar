package main

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// Ticket 09: Connections that break, and recover.
//
// The distinction this ticket rests on is between the provider refusing a
// credential and the provider being unreachable. Conflating them would
// disconnect every user at once during an outage.

func TestTwoAuthenticationFailuresRetireAConnection(t *testing.T) {
	now := moment(2026, 9, 24, 9, 0)
	h := connectedHarness(t, now)
	h.calendar("events-1000001")
	h.plugin.RunPoll(now)
	h.clearPosts()

	// Access is revoked at Yandex.
	h.provider.Token = "a-token-this-plugin-does-not-have"

	next := h.pollAgain(now)
	connection, _ := h.plugin.store.Connection(testUserID)
	if !connection.Active {
		t.Fatal("one failure retired the Connection; an outage would disconnect everybody")
	}
	if len(h.messages()) != 0 {
		t.Errorf("the person was told about a single failure:\n%s", allText(h.messages()))
	}

	h.pollAgain(next)
	connection, _ = h.plugin.store.Connection(testUserID)
	if connection.Active {
		t.Error("two consecutive refusals did not retire the Connection")
	}
}

func TestARetiredConnectionIsExplainedOnceWithAWayBack(t *testing.T) {
	now := moment(2026, 9, 24, 9, 0)
	h := connectedHarness(t, now)
	h.calendar("events-1000001")
	h.plugin.RunPoll(now)
	h.provider.Token = "revoked"
	h.clearPosts()

	next := h.pollAgain(now)
	next = h.pollAgain(next)
	h.pollAgain(next)

	messages := h.messages()
	if len(messages) != 1 {
		t.Fatalf("got %d messages about a retired Connection, want exactly one:\n%s",
			len(messages), allText(messages))
	}
	if !strings.Contains(messages[0].Message, "/yacal connect") {
		t.Errorf("the message offers no way back:\n%s", messages[0].Message)
	}
	if strings.Contains(messages[0].Message, "401") || strings.Contains(messages[0].Message, "caldav") {
		t.Errorf("the person was shown technical detail:\n%s", messages[0].Message)
	}
}

func TestARetiredConnectionStopsAllMessages(t *testing.T) {
	yesterday := moment(2026, 9, 23, 20, 0)
	h := connectedHarness(t, yesterday)
	calendar := h.calendar("events-1000001")
	calendar.Put("a", timedEvent("a", "Standup", moment(2026, 9, 24, 10, 0), moment(2026, 9, 24, 10, 30)))
	h.plugin.RunPoll(yesterday)

	h.provider.Token = "revoked"
	next := h.pollAgain(yesterday)
	h.pollAgain(next)
	h.clearPosts()

	h.plugin.RunDelivery(moment(2026, 9, 24, 8, 0))
	h.plugin.RunDelivery(moment(2026, 9, 24, 9, 50))

	if messages := h.messages(); len(messages) != 0 {
		t.Errorf("a retired Connection still produced %d messages:\n%s", len(messages), allText(messages))
	}
}

func TestReconnectingRestoresMessagesWithNoOtherAction(t *testing.T) {
	yesterday := moment(2026, 9, 23, 20, 0)
	h := connectedHarness(t, yesterday)
	calendar := h.calendar("events-1000001")
	calendar.Put("a", timedEvent("a", "Standup", moment(2026, 9, 24, 10, 0), moment(2026, 9, 24, 10, 30)))
	h.plugin.RunPoll(yesterday)

	h.provider.Token = "revoked"
	next := h.pollAgain(yesterday)
	h.pollAgain(next)
	h.clearPosts()

	// The person reconnects, and Yandex issues a token that works again.
	h.provider.Token = h.yandex.accessToken
	h.connect(moment(2026, 9, 24, 7, 0))

	h.plugin.RunPoll(moment(2026, 9, 24, 7, 30))
	h.plugin.RunDelivery(moment(2026, 9, 24, 8, 0))
	h.plugin.RunDelivery(moment(2026, 9, 24, 9, 50))

	if summaries := h.summaries(); len(summaries) != 1 {
		t.Errorf("the summary did not come back after reconnecting: got %d", len(summaries))
	}
	if reminders := h.reminders(); len(reminders) != 1 {
		t.Errorf("reminders did not come back after reconnecting: got %d", len(reminders))
	}
}

func TestAProviderOutageNeverRetiresAConnection(t *testing.T) {
	now := moment(2026, 9, 24, 9, 0)
	h := connectedHarness(t, now)
	h.calendar("events-1000001")
	h.plugin.RunPoll(now)
	h.clearPosts()

	// Yandex is unwell for several poll cycles.
	h.provider.Fail(http.StatusServiceUnavailable, 0)
	at := now
	for range 6 {
		at = h.pollAgain(at)
	}

	connection, err := h.plugin.store.Connection(testUserID)
	if err != nil {
		t.Fatalf("Connection: %v", err)
	}
	if !connection.Active {
		t.Error("an outage retired the Connection; every user would have to reconnect by hand")
	}
	if messages := h.messages(); len(messages) != 0 {
		t.Errorf("the person was bothered during an outage:\n%s", allText(messages))
	}
}

func TestMessagesResumeByThemselvesAfterAnOutage(t *testing.T) {
	yesterday := moment(2026, 9, 23, 20, 0)
	h := connectedHarness(t, yesterday)
	calendar := h.calendar("events-1000001")
	calendar.Put("a", timedEvent("a", "Standup", moment(2026, 9, 24, 10, 0), moment(2026, 9, 24, 10, 30)))

	h.provider.Fail(http.StatusBadGateway, 0)
	at := yesterday
	for range 4 {
		at = h.pollAgain(at)
	}
	h.clearPosts()

	h.provider.Recover()
	h.plugin.RunPoll(moment(2026, 9, 24, 9, 0))
	h.plugin.RunDelivery(moment(2026, 9, 24, 9, 50))

	if reminders := h.reminders(); len(reminders) != 1 {
		t.Errorf("messages did not resume by themselves after the outage: got %d", len(reminders))
	}
}

func TestATransientFailureDoesNotCountTowardsRetirement(t *testing.T) {
	now := moment(2026, 9, 24, 9, 0)
	h := connectedHarness(t, now)
	h.calendar("events-1000001")
	h.plugin.RunPoll(now)

	// One refusal, then an outage, then another refusal. The two refusals are
	// not consecutive attempts at the same thing, but they are the only two
	// answers that say the credential is wrong.
	h.provider.Token = "revoked"
	next := h.pollAgain(now)

	h.provider.Fail(http.StatusServiceUnavailable, 1)
	next = h.pollAgain(next)

	connection, _ := h.plugin.store.Connection(testUserID)
	if !connection.Active {
		t.Fatal("an outage in the middle retired the Connection")
	}

	h.pollAgain(next)
	connection, _ = h.plugin.store.Connection(testUserID)
	if connection.Active {
		t.Error("two refusals did not retire the Connection")
	}
}

func TestASuccessfulReadClearsThePreviousFailure(t *testing.T) {
	now := moment(2026, 9, 24, 9, 0)
	h := connectedHarness(t, now)
	h.calendar("events-1000001")
	h.plugin.RunPoll(now)

	h.provider.Token = "revoked"
	next := h.pollAgain(now)

	h.provider.Token = h.yandex.accessToken
	next = h.pollAgain(next)

	h.provider.Token = "revoked"
	h.pollAgain(next)

	connection, _ := h.plugin.store.Connection(testUserID)
	if !connection.Active {
		t.Error("a refusal before a success and one after were treated as consecutive")
	}
}

func TestARefreshRefusedForGoodRetiresTheConnection(t *testing.T) {
	connectedAt := moment(2026, 9, 24, 9, 0)
	h := newHarness(t)
	h.yandex.expiresIn = 60 // the access token dies within the hour
	h.connect(connectedAt)
	h.calendar("events-1000001")

	h.yandex.failTokenWith(http.StatusBadRequest, "invalid_grant")

	later := connectedAt.Add(time.Hour)
	h.plugin.RunPoll(later)
	next := later.Add(pollInterval + pollJitter + time.Minute)
	h.plugin.RunPoll(next)

	connection, _ := h.plugin.store.Connection(testUserID)
	if connection.Active {
		t.Error("a refresh refused as invalid_grant did not retire the Connection")
	}
}

func TestARefreshThatFailsBecauseYandexIsUnwellIsRetried(t *testing.T) {
	connectedAt := moment(2026, 9, 24, 9, 0)
	h := newHarness(t)
	h.yandex.expiresIn = 60
	h.connect(connectedAt)
	h.calendar("events-1000001")

	h.yandex.failTokenWith(http.StatusServiceUnavailable, "")

	at := connectedAt.Add(time.Hour)
	for range 4 {
		h.plugin.RunPoll(at)
		at = at.Add(pollInterval + pollJitter + time.Minute)
	}

	connection, _ := h.plugin.store.Connection(testUserID)
	if !connection.Active {
		t.Fatal("a token endpoint having a bad afternoon retired the Connection")
	}

	h.yandex.recoverToken()
	h.yandex.accessToken = "a-renewed-token"
	h.provider.Token = "a-renewed-token"
	h.plugin.RunPoll(at)

	state, _ := h.plugin.store.SyncState(testUserID)
	if state.LastSuccessAt.IsZero() {
		t.Error("the plugin did not recover once the token endpoint came back")
	}
}

func TestOnePersonsFailureDoesNotStopAnother(t *testing.T) {
	now := moment(2026, 9, 24, 9, 0)
	h := connectedHarness(t, now)
	h.calendar("events-1000001")

	// A second person is in the index but their stored Connection is
	// unreadable, which is the worst case: it fails before any request.
	other := "other12345678901234567890x"
	if err := h.plugin.store.SaveConnection(&Connection{
		MattermostUserID: other, YandexLogin: "other@yandex.ru", Active: true,
		AccessToken: "x", RefreshToken: "y",
	}); err != nil {
		t.Fatalf("SaveConnection: %v", err)
	}
	h.api.mu.Lock()
	h.api.kv[connectionKeyPrefix+other] = kvEntry{value: []byte(`"not-decryptable"`)}
	h.api.mu.Unlock()

	h.plugin.RunPoll(now)

	state, err := h.plugin.store.SyncState(testUserID)
	if err != nil {
		t.Fatalf("SyncState: %v", err)
	}
	if state.LastSuccessAt.IsZero() {
		t.Error("one person's broken Connection stopped another person being polled")
	}
}

func TestOneUnreadableEventDoesNotStopTheRest(t *testing.T) {
	yesterday := moment(2026, 9, 23, 20, 0)
	h := connectedHarness(t, yesterday)
	calendar := h.calendar("events-1000001")
	calendar.PutItem("broken", unreadableItem())
	calendar.Put("fine", timedEvent("fine", "Standup", moment(2026, 9, 24, 10, 0), moment(2026, 9, 24, 10, 30)))

	h.plugin.RunPoll(yesterday)
	h.plugin.RunDelivery(moment(2026, 9, 24, 9, 50))

	if reminders := h.reminders(); len(reminders) != 1 {
		t.Errorf("one bad entry cost this person their reminders: got %d", len(reminders))
	}
}

func TestAPersonIsNeverSentTechnicalErrorText(t *testing.T) {
	now := moment(2026, 9, 24, 9, 0)
	h := connectedHarness(t, now)
	h.calendar("events-1000001")
	h.provider.Fail(http.StatusInternalServerError, 0)
	h.clearPosts()

	shown := h.command("/yacal today", now).Text

	for _, forbidden := range []string{"500", "caldav", "PROPFIND", "http://", "127.0.0.1"} {
		if strings.Contains(shown, forbidden) {
			t.Errorf("a person was shown %q:\n%s", forbidden, shown)
		}
	}
	if !strings.Contains(shown, "Yandex") {
		t.Errorf("the message does not say what happened:\n%s", shown)
	}
}

func TestAnOutageDuringTodayDoesNotSuggestReconnecting(t *testing.T) {
	now := moment(2026, 9, 24, 9, 0)
	h := connectedHarness(t, now)
	h.calendar("events-1000001")
	h.provider.Fail(http.StatusServiceUnavailable, 0)

	shown := h.command("/yacal today", now).Text

	if strings.Contains(shown, "/yacal connect") {
		t.Errorf("an outage told the person to reconnect:\n%s", shown)
	}
}

func TestARevokedConnectionDuringTodayDoesSuggestReconnecting(t *testing.T) {
	now := moment(2026, 9, 24, 9, 0)
	h := connectedHarness(t, now)
	h.calendar("events-1000001")
	h.provider.Token = "revoked"

	shown := h.command("/yacal today", now).Text

	if !strings.Contains(shown, "/yacal connect") {
		t.Errorf("a revoked Connection did not offer a way back:\n%s", shown)
	}
}
