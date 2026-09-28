package main

import (
	"strings"
	"testing"
	"time"

	"github.com/maunty/mattermost-yandex360-calendar/server/internal/fakecaldav"
)

// The CalDAV fallback (ADR 0002). It is not wired in, so these tests wire it
// in by hand, the same way switching back would, and check the things only the
// CalDAV path does: discovery, leaving out the task list, and expanding
// recurring Events on this side of the wire.

// calDAVHarness is a connected harness reading from a fake CalDAV server
// instead of the REST API.
func calDAVHarness(t *testing.T, now time.Time) (*harness, *fakecaldav.Server) {
	t.Helper()
	h := connectedHarness(t, now)
	server := fakecaldav.New(t)
	h.plugin.caldavURL = server.URL
	h.plugin.readEvents = h.plugin.readFromCalDAV
	return h, server
}

func TestCalDAVReadsEveryCalendarButNotTheTaskList(t *testing.T) {
	now := moment(2026, 9, 24, 8, 0)
	h, server := calDAVHarness(t, now)
	work := server.AddCalendar("events-1000001", "Мои события")
	work.Put("w", timedEvent("w", "Work meeting", moment(2026, 9, 24, 9, 0), moment(2026, 9, 24, 10, 0)))
	personal := server.AddCalendar("events-9000001", "Личное")
	personal.Put("p", timedEvent("p", "Dentist", moment(2026, 9, 24, 11, 0), moment(2026, 9, 24, 12, 0)))
	todos := server.AddTaskList("todos-1000002", "Не забыть")
	todos.Put("t", task("t", "Buy milk"))

	shown := h.todayText(now)

	if !strings.Contains(shown, "Work meeting") || !strings.Contains(shown, "Dentist") {
		t.Errorf("events from both calendars were not shown:\n%s", shown)
	}
	if strings.Contains(shown, "Buy milk") {
		t.Errorf("a task was listed as a meeting:\n%s", shown)
	}
}

func TestCalDAVRemindsAMovedOccurrenceAtItsNewTime(t *testing.T) {
	originalStart := moment(2026, 9, 24, 10, 0)
	h, server := calDAVHarness(t, moment(2026, 9, 24, 8, 0))
	calendar := server.AddCalendar("events-1000001", "Мои события")
	calendar.Put("weekly", seriesWithOverride("weekly", "Weekly sync",
		moment(2026, 8, 6, 10, 0), moment(2026, 8, 6, 11, 0), "FREQ=WEEKLY;BYDAY=TH",
		movedOccurrence("weekly", "Weekly sync", originalStart,
			moment(2026, 9, 24, 15, 0), moment(2026, 9, 24, 16, 0))))

	h.plugin.RunPoll(moment(2026, 9, 24, 8, 0))
	h.plugin.RunDelivery(originalStart.Add(-10 * time.Minute))
	if messages := h.reminders(); len(messages) != 0 {
		t.Fatalf("a reminder went out at the old time:\n%s", allText(messages))
	}

	h.plugin.RunDelivery(moment(2026, 9, 24, 14, 50))
	if messages := h.reminders(); len(messages) != 1 || !strings.Contains(text(messages[0]), "15:00") {
		t.Errorf("want one reminder naming the new time, got:\n%s", allText(messages))
	}
}

func TestCalDAVSkipsAnEventWithAnUnreadableRule(t *testing.T) {
	yesterday := moment(2026, 9, 23, 20, 0)
	h, server := calDAVHarness(t, yesterday)
	calendar := server.AddCalendar("events-1000001", "Мои события")
	calendar.Put("weird", recurringEvent("weird", "Nonsense rule",
		moment(2026, 9, 24, 9, 0), moment(2026, 9, 24, 9, 30), "FREQ=NEVER;INTERVAL=banana"))
	calendar.Put("fine", timedEvent("fine", "Standup", moment(2026, 9, 24, 10, 0), moment(2026, 9, 24, 10, 30)))

	h.plugin.RunPoll(yesterday)
	h.plugin.RunDelivery(moment(2026, 9, 24, 9, 50))

	if reminders := h.reminders(); len(reminders) != 1 {
		t.Errorf("an unreadable rule cost this person their other reminders: got %d", len(reminders))
	}
	if !strings.Contains(strings.Join(h.logged(), "\n"), "recurrence could not be expanded") {
		t.Errorf("the skipped rule was not logged: %v", h.logged())
	}
}

func TestCalDAVDiscoveryIsNotRepeatedOnEveryTick(t *testing.T) {
	now := moment(2026, 9, 24, 9, 0)
	h, server := calDAVHarness(t, now)
	server.AddCalendar("events-1000001", "Мои события")
	h.plugin.RunPoll(now)

	server.Reset()
	next := h.pollAgain(now)
	h.pollAgain(next)

	for _, request := range server.Requests() {
		if strings.Contains(request.Path, "principals") {
			t.Errorf("the principal was looked up again on a later tick: %+v", request)
		}
	}
}
