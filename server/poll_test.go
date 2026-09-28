package main

import (
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/maunty/mattermost-yandex360-calendar/server/internal/fakecalendarapi"
)

// Ticket 06: background polling. Nothing is delivered here; what is checked is
// that the cache is right and that a tick costs what it should.

func (h *harness) cached(t *testing.T) []string {
	t.Helper()
	state, err := h.plugin.store.SyncState(testUserID)
	if err != nil {
		t.Fatalf("SyncState: %v", err)
	}
	titles := make([]string, 0, len(state.Occurrences))
	for _, occurrence := range state.Occurrences {
		titles = append(titles, occurrence.Title+" at "+occurrence.Start.In(testZone).Format("2006-01-02 15:04"))
	}
	return titles
}

// boundedInTime reports whether a read names both ends of its window. Without
// an end, the API pages through everything from the start onwards.
func boundedInTime(request fakecalendarapi.Request) bool {
	query, err := url.ParseQuery(request.Query)
	return err == nil && query.Get("from") != "" && query.Get("to") != ""
}

func (h *harness) requestCount() int { return len(h.provider.Requests()) }

// pollAgain moves past the person's next due time and polls.
func (h *harness) pollAgain(after time.Time) time.Time {
	next := after.Add(pollInterval + pollJitter + time.Minute)
	h.plugin.RunPoll(next)
	return next
}

func TestPollingFillsTheCacheWithoutAnybodyAskingForIt(t *testing.T) {
	now := moment(2026, 9, 24, 9, 0)
	h := connectedHarness(t, now)
	calendar := h.calendar("events-1000001")
	calendar.Put("standup", timedEvent("standup", "Standup",
		moment(2026, 9, 24, 10, 0), moment(2026, 9, 24, 10, 30)))

	h.plugin.RunPoll(now)

	cached := h.cached(t)
	if len(cached) != 1 || !strings.Contains(cached[0], "Standup") {
		t.Fatalf("the cache holds %v", cached)
	}
	if messages := h.messages(); len(messages) != 0 {
		t.Errorf("polling sent %d messages; it should send none", len(messages))
	}
}

func TestATickCostsOneRequestHoweverManyCalendarsThereAre(t *testing.T) {
	now := moment(2026, 9, 24, 9, 0)
	h := connectedHarness(t, now)
	work := h.calendar("events-1000001")
	work.Put("a", timedEvent("a", "Work meeting", moment(2026, 9, 24, 11, 0), moment(2026, 9, 24, 12, 0)))
	personal := h.calendar("events-9000001")
	personal.Put("b", timedEvent("b", "Dentist", moment(2026, 9, 24, 15, 0), moment(2026, 9, 24, 16, 0)))
	h.provider.AddTaskList("todos-1000002", "Не забыть")

	h.plugin.RunPoll(now)

	h.provider.Reset()
	h.pollAgain(now)

	if count := h.requestCount(); count != 1 {
		t.Fatalf("a tick cost %d requests, want 1:\n%+v", count, h.provider.Requests())
	}
}

func TestAChangeInAnyCalendarReachesTheCacheOnTheNextTick(t *testing.T) {
	now := moment(2026, 9, 24, 9, 0)
	h := connectedHarness(t, now)
	work := h.calendar("events-1000001")
	work.Put("a", timedEvent("a", "Work meeting", moment(2026, 9, 24, 11, 0), moment(2026, 9, 24, 12, 0)))
	personal := h.calendar("events-9000001")
	personal.Put("b", timedEvent("b", "Dentist", moment(2026, 9, 24, 15, 0), moment(2026, 9, 24, 16, 0)))

	h.plugin.RunPoll(now)

	// Something changes in one calendar only.
	work.Put("c", timedEvent("c", "New meeting", moment(2026, 9, 24, 17, 0), moment(2026, 9, 24, 17, 30)))
	h.pollAgain(now)

	cached := strings.Join(h.cached(t), "|")
	if !strings.Contains(cached, "New meeting") {
		t.Errorf("the new meeting did not reach the cache: %v", cached)
	}
	if !strings.Contains(cached, "Dentist") {
		t.Errorf("the unchanged calendar's events were dropped from the cache: %v", cached)
	}
}

func TestEveryPageOfEventsIsRead(t *testing.T) {
	now := moment(2026, 9, 24, 9, 0)
	h := connectedHarness(t, now)
	h.provider.PageSize = 2
	calendar := h.calendar("events-1000001")
	for i := range 5 {
		uid := fmt.Sprintf("m%d", i)
		calendar.Put(uid, timedEvent(uid, fmt.Sprintf("Meeting %d", i),
			moment(2026, 9, 24, 10+i, 0), moment(2026, 9, 24, 10+i, 30)))
	}

	h.plugin.RunPoll(now)

	if cached := h.cached(t); len(cached) != 5 {
		t.Errorf("the cache holds %d of 5 Events: %v", len(cached), cached)
	}
}

func TestDifferentPeopleAreNotAllPolledInTheSameSecond(t *testing.T) {
	// Events cluster on the hour, so people whose polls coincide would have
	// their messages come due together.
	now := moment(2026, 9, 24, 9, 0)
	seen := map[time.Duration]bool{}
	for _, userID := range []string{
		"aaaaaaaaaaaaaaaaaaaaaaaaaa", "bbbbbbbbbbbbbbbbbbbbbbbbbb",
		"cccccccccccccccccccccccccc", "dddddddddddddddddddddddddd",
	} {
		seen[nextPollAt(userID, now).Sub(now)] = true
	}
	if len(seen) < 3 {
		t.Errorf("four people produced only %d distinct poll times", len(seen))
	}
}

func TestTheSameUserAlwaysGetsTheSameOffset(t *testing.T) {
	now := moment(2026, 9, 24, 9, 0)
	first := nextPollAt(testUserID, now)
	second := nextPollAt(testUserID, now)
	if !first.Equal(second) {
		t.Errorf("the offset moved between calls: %v then %v", first, second)
	}
}

func TestTheCachedWindowReachesBeyondTomorrow(t *testing.T) {
	now := moment(2026, 9, 24, 9, 0)
	h := connectedHarness(t, now)
	calendar := h.calendar("events-1000001")
	calendar.Put("tomorrow", timedEvent("tomorrow", "Tomorrow's meeting",
		moment(2026, 9, 25, 10, 0), moment(2026, 9, 25, 11, 0)))

	h.plugin.RunPoll(now)

	state, _ := h.plugin.store.SyncState(testUserID)
	if !state.CachedThrough.After(now.Add(24 * time.Hour)) {
		t.Errorf("the cache reaches only to %v, which does not cover tomorrow's summary", state.CachedThrough)
	}
	if !strings.Contains(strings.Join(h.cached(t), "|"), "Tomorrow's meeting") {
		t.Errorf("tomorrow's meeting is not cached: %v", h.cached(t))
	}
}

func TestNoPollReadsTheWholeCalendar(t *testing.T) {
	now := moment(2026, 9, 24, 9, 0)
	h := connectedHarness(t, now)
	calendar := h.calendar("events-1000001")
	calendar.Put("ancient", timedEvent("ancient", "A meeting from 2017",
		moment(2017, 3, 1, 10, 0), moment(2017, 3, 1, 11, 0)))

	h.plugin.RunPoll(now)

	for _, request := range h.provider.Requests() {
		if !boundedInTime(request) {
			t.Errorf("a read without both ends of a window was issued: %s", request.Query)
		}
	}
	if strings.Contains(strings.Join(h.cached(t), "|"), "2017") {
		t.Errorf("years of history reached the cache: %v", h.cached(t))
	}
}

func TestSomebodyWithNoConnectionIsNeverPolled(t *testing.T) {
	h := newHarness(t)
	h.calendar("events-1000001")

	h.plugin.RunPoll(moment(2026, 9, 24, 9, 0))

	if count := h.requestCount(); count != 0 {
		t.Errorf("a person with no Connection drew %d requests", count)
	}
}

func TestRestartingResumesPollingWithoutRedoingTheWork(t *testing.T) {
	now := moment(2026, 9, 24, 9, 0)
	h := connectedHarness(t, now)
	calendar := h.calendar("events-1000001")
	calendar.Put("a", timedEvent("a", "Standup", moment(2026, 9, 24, 11, 0), moment(2026, 9, 24, 11, 30)))
	h.plugin.RunPoll(now)

	if err := h.plugin.OnDeactivate(); err != nil {
		t.Fatalf("OnDeactivate: %v", err)
	}
	if err := h.plugin.OnActivate(); err != nil {
		t.Fatalf("OnActivate: %v", err)
	}

	h.provider.Reset()
	h.pollAgain(now)

	if count := h.requestCount(); count != 1 {
		t.Errorf("the first tick after a restart cost %d requests, want 1", count)
	}
	if !strings.Contains(strings.Join(h.cached(t), "|"), "Standup") {
		t.Errorf("the cache did not survive a restart: %v", h.cached(t))
	}
}
