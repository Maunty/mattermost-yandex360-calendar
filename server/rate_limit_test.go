package main

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// Yandex limits each person to a few requests a second. Being told to slow
// down is not a fault in their Connection, and holds back only their reads.

func TestARateLimitedPersonIsReadAgainOnTheNextRun(t *testing.T) {
	now := moment(2026, 9, 24, 9, 0)
	h := connectedHarness(t, now)
	h.calendar("events-1000001")

	h.provider.Fail(http.StatusTooManyRequests, 1)
	h.plugin.RunPoll(now)
	h.plugin.RunPoll(now.Add(time.Minute))

	if shown := h.command("/yacal settings", now.Add(time.Minute)).Text; !strings.Contains(shown, "24 Sep, 09:01") {
		t.Errorf("a rate-limited read was not retried a minute later:\n%s", shown)
	}
}

func TestRetryAfterHoldsBackReading(t *testing.T) {
	now := moment(2026, 9, 24, 9, 0)
	h := connectedHarness(t, now)
	h.calendar("events-1000001")
	h.provider.RetryAfter = "300"
	h.provider.Fail(http.StatusTooManyRequests, 1)
	h.plugin.RunPoll(now)
	h.provider.Reset()

	for minute := 1; minute < 5; minute++ {
		h.plugin.RunPoll(now.Add(time.Duration(minute) * time.Minute))
	}
	if requests := h.provider.Requests(); len(requests) != 0 {
		t.Fatalf("read Yandex %d times while it had asked for five minutes' quiet", len(requests))
	}

	h.plugin.RunPoll(now.Add(5 * time.Minute))
	if requests := h.provider.Requests(); len(requests) == 0 {
		t.Error("reading did not resume once the wait was over")
	}
}

func TestBeingRateLimitedNeverRetiresAConnection(t *testing.T) {
	now := moment(2026, 9, 24, 9, 0)
	h := connectedHarness(t, now)
	h.calendar("events-1000001")

	h.provider.Fail(http.StatusTooManyRequests, 0)
	for minute := range 10 {
		h.plugin.RunPoll(now.Add(time.Duration(minute) * time.Minute))
	}

	connection, err := h.plugin.store.Connection(testUserID)
	if err != nil {
		t.Fatalf("Connection: %v", err)
	}
	if !connection.Active {
		t.Error("being rate limited retired the Connection")
	}
	if messages := h.messages(); len(messages) != 0 {
		t.Errorf("being rate limited sent the person a message:\n%s", allText(messages))
	}
}

func TestRemindersStillArriveFromTheCacheWhileRateLimited(t *testing.T) {
	start := moment(2026, 9, 24, 10, 0)
	h := connectedHarness(t, start.Add(-time.Hour))
	h.calendar("events-1000001").Put("standup", timedEvent("standup", "Standup", start, start.Add(30*time.Minute)))
	h.plugin.RunPoll(start.Add(-time.Hour))

	h.provider.Fail(http.StatusTooManyRequests, 0)
	h.tick(start.Add(-10 * time.Minute))

	if messages := h.reminders(); len(messages) != 1 {
		t.Errorf("got %d reminders while rate limited, want one from the cache", len(messages))
	}
}
