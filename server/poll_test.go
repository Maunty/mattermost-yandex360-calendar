package main

import (
	"strings"
	"testing"
	"time"
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

func (h *harness) requestCount() int { return len(h.caldav.Requests()) }

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

func TestATickOverUnchangedCalendarsCostsOneRequest(t *testing.T) {
	now := moment(2026, 9, 24, 9, 0)
	h := connectedHarness(t, now)
	work := h.calendar("events-1000001")
	work.Put("a", timedEvent("a", "Work meeting", moment(2026, 9, 24, 11, 0), moment(2026, 9, 24, 12, 0)))
	personal := h.calendar("events-9000001")
	personal.Put("b", timedEvent("b", "Dentist", moment(2026, 9, 24, 15, 0), moment(2026, 9, 24, 16, 0)))
	h.caldav.AddTaskList("todos-1000002", "Не забыть")

	h.plugin.RunPoll(now) // the first poll discovers and reads everything

	h.caldav.Reset()
	h.pollAgain(now)

	requests := h.caldav.Requests()
	if len(requests) != 1 {
		t.Fatalf("a tick over unchanged calendars cost %d requests, want 1:\n%+v", len(requests), requests)
	}
	if requests[0].Method != "PROPFIND" {
		t.Errorf("the one request was %s, want the depth-one read of the calendar home", requests[0].Method)
	}
}

func TestOnlyTheCollectionWhoseChangeTagMovedIsQueried(t *testing.T) {
	now := moment(2026, 9, 24, 9, 0)
	h := connectedHarness(t, now)
	work := h.calendar("events-1000001")
	work.Put("a", timedEvent("a", "Work meeting", moment(2026, 9, 24, 11, 0), moment(2026, 9, 24, 12, 0)))
	personal := h.calendar("events-9000001")
	personal.Put("b", timedEvent("b", "Dentist", moment(2026, 9, 24, 15, 0), moment(2026, 9, 24, 16, 0)))

	h.plugin.RunPoll(now)

	// Something changes in one calendar only.
	work.Put("c", timedEvent("c", "New meeting", moment(2026, 9, 24, 17, 0), moment(2026, 9, 24, 17, 30)))

	h.caldav.Reset()
	h.pollAgain(now)

	var queried []string
	for _, request := range h.caldav.Requests() {
		if request.Method == "REPORT" {
			queried = append(queried, request.Path)
		}
	}
	if len(queried) != 1 {
		t.Fatalf("%d collections were queried, want only the one that changed: %v", len(queried), queried)
	}
	if !strings.Contains(queried[0], "events-1000001") {
		t.Errorf("the wrong collection was queried: %s", queried[0])
	}
	if !strings.Contains(strings.Join(h.cached(t), "|"), "New meeting") {
		t.Errorf("the new meeting did not reach the cache: %v", h.cached(t))
	}
	if !strings.Contains(strings.Join(h.cached(t), "|"), "Dentist") {
		t.Errorf("the unchanged calendar's events were dropped from the cache: %v", h.cached(t))
	}
}

func TestTheCacheFollowsAnEventBeingMovedAtTheProvider(t *testing.T) {
	now := moment(2026, 9, 24, 9, 0)
	h := connectedHarness(t, now)
	calendar := h.calendar("events-1000001")
	calendar.Put("m", timedEvent("m", "Review", moment(2026, 9, 24, 14, 0), moment(2026, 9, 24, 15, 0)))
	h.plugin.RunPoll(now)

	calendar.Put("m", timedEvent("m", "Review", moment(2026, 9, 24, 16, 0), moment(2026, 9, 24, 17, 0)))
	h.pollAgain(now)

	cached := strings.Join(h.cached(t), "|")
	if !strings.Contains(cached, "16:00") {
		t.Errorf("the cache did not follow the move: %s", cached)
	}
	if strings.Contains(cached, "14:00") {
		t.Errorf("the cache still holds the old time: %s", cached)
	}
}

func TestTheCacheFollowsAnEventBeingDeletedAtTheProvider(t *testing.T) {
	now := moment(2026, 9, 24, 9, 0)
	h := connectedHarness(t, now)
	calendar := h.calendar("events-1000001")
	calendar.Put("m", timedEvent("m", "Review", moment(2026, 9, 24, 14, 0), moment(2026, 9, 24, 15, 0)))
	h.plugin.RunPoll(now)

	calendar.Remove("m")
	h.pollAgain(now)

	if cached := h.cached(t); len(cached) != 0 {
		t.Errorf("a deleted event is still cached: %v", cached)
	}
}

func TestACalendarRemovedAtTheProviderLeavesTheCache(t *testing.T) {
	now := moment(2026, 9, 24, 9, 0)
	h := connectedHarness(t, now)
	work := h.calendar("events-1000001")
	work.Put("a", timedEvent("a", "Work meeting", moment(2026, 9, 24, 11, 0), moment(2026, 9, 24, 12, 0)))
	h.plugin.RunPoll(now)

	// The collection list no longer holds it. A fresh fake with no calendars
	// is the same thing from the plugin's point of view.
	work.Remove("a")
	h.pollAgain(now)

	if cached := h.cached(t); len(cached) != 0 {
		t.Errorf("the cache still holds %v", cached)
	}
}

func TestPollingIsSpacedOutAndJittered(t *testing.T) {
	now := moment(2026, 9, 24, 9, 0)
	h := connectedHarness(t, now)
	h.calendar("events-1000001")
	h.plugin.RunPoll(now)

	state, err := h.plugin.store.SyncState(testUserID)
	if err != nil {
		t.Fatalf("SyncState: %v", err)
	}
	gap := state.NextPollAt.Sub(now)
	if gap < pollInterval-pollJitter || gap > pollInterval+pollJitter {
		t.Errorf("the next poll is %v away, want ten minutes give or take four", gap)
	}

	// A tick before that time costs nothing at all.
	h.caldav.Reset()
	h.plugin.RunPoll(now.Add(time.Minute))
	if count := h.requestCount(); count != 0 {
		t.Errorf("a tick before the due time cost %d requests", count)
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

func TestNoPollReadsAWholeCollection(t *testing.T) {
	now := moment(2026, 9, 24, 9, 0)
	h := connectedHarness(t, now)
	calendar := h.calendar("events-1000001")
	calendar.Put("ancient", timedEvent("ancient", "A meeting from 2017",
		moment(2017, 3, 1, 10, 0), moment(2017, 3, 1, 11, 0)))

	h.plugin.RunPoll(now)

	for _, request := range h.caldav.Requests() {
		if request.Method == "REPORT" && !strings.Contains(request.Body, "time-range") {
			t.Errorf("a query without a time filter was issued:\n%s", request.Body)
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

	h.caldav.Reset()
	h.pollAgain(now)

	if count := h.requestCount(); count != 1 {
		t.Errorf("the first tick after a restart cost %d requests, want 1: the change tags "+
			"and the cache should have survived", count)
	}
	if !strings.Contains(strings.Join(h.cached(t), "|"), "Standup") {
		t.Errorf("the cache did not survive a restart: %v", h.cached(t))
	}
}

func TestDiscoveryIsNotRepeatedOnEveryTick(t *testing.T) {
	now := moment(2026, 9, 24, 9, 0)
	h := connectedHarness(t, now)
	h.calendar("events-1000001")
	h.plugin.RunPoll(now)

	h.caldav.Reset()
	next := h.pollAgain(now)
	h.pollAgain(next)

	for _, request := range h.caldav.Requests() {
		if strings.Contains(request.Path, "principals") {
			t.Errorf("the principal was looked up again on a later tick: %+v", request)
		}
	}
}
