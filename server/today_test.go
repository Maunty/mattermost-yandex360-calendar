package main

import (
	"strings"
	"testing"
	"time"
)

// Tickets 04 and 05: today's Events on demand, recurring ones included.

// todayText runs /yacal today and returns what the person was shown.
func (h *harness) todayText(now time.Time) string {
	h.t.Helper()
	h.clearPosts()
	response := h.command("/yacal today", now)

	posts := h.ephemeralPosts()
	if len(posts) == 0 {
		return response.Text
	}
	return allText(posts)
}

func connectedHarness(t *testing.T, now time.Time) *harness {
	t.Helper()
	h := newHarness(t)
	h.connect(now)
	return h
}

func TestTodayListsTheDaysEvents(t *testing.T) {
	now := moment(2026, 9, 24, 9, 0)
	h := connectedHarness(t, now)
	calendar := h.calendar("events-1000001")
	calendar.Put("standup", timedEvent("standup", "Standup",
		moment(2026, 9, 24, 10, 0), moment(2026, 9, 24, 10, 30), "LOCATION:Room 3"))
	calendar.Put("review", timedEvent("review", "Design review",
		moment(2026, 9, 24, 14, 0), moment(2026, 9, 24, 15, 0)))

	shown := h.todayText(now)

	for _, want := range []string{"Standup", "10:00", "10:30", "Room 3", "Design review", "14:00"} {
		if !strings.Contains(shown, want) {
			t.Errorf("today's list does not mention %q:\n%s", want, shown)
		}
	}
}

func TestTodayIsOrderedByStartTime(t *testing.T) {
	now := moment(2026, 9, 24, 8, 0)
	h := connectedHarness(t, now)
	calendar := h.calendar("events-1000001")
	calendar.Put("late", timedEvent("late", "Afternoon thing", moment(2026, 9, 24, 16, 0), moment(2026, 9, 24, 17, 0)))
	calendar.Put("early", timedEvent("early", "Morning thing", moment(2026, 9, 24, 9, 0), moment(2026, 9, 24, 10, 0)))

	shown := h.todayText(now)

	if strings.Index(shown, "Morning thing") > strings.Index(shown, "Afternoon thing") {
		t.Errorf("the day is not in start order:\n%s", shown)
	}
}

func TestTodayReadsEveryCalendarButNotTheTaskList(t *testing.T) {
	now := moment(2026, 9, 24, 8, 0)
	h := connectedHarness(t, now)

	work := h.calendar("events-1000001")
	work.Put("w", timedEvent("w", "Work meeting", moment(2026, 9, 24, 9, 0), moment(2026, 9, 24, 10, 0)))
	personal := h.calendar("events-9000001")
	personal.Put("p", timedEvent("p", "Dentist", moment(2026, 9, 24, 11, 0), moment(2026, 9, 24, 12, 0)))
	todos := h.caldav.AddTaskList("todos-1000002", "Не забыть")
	todos.Put("t", task("t", "Buy milk"))

	shown := h.todayText(now)

	if !strings.Contains(shown, "Work meeting") || !strings.Contains(shown, "Dentist") {
		t.Errorf("events from both calendars were not shown:\n%s", shown)
	}
	if strings.Contains(shown, "Buy milk") {
		t.Errorf("a task was listed as a meeting:\n%s", shown)
	}
}

func TestTodaySaysTheDayIsClearWhenItIs(t *testing.T) {
	now := moment(2026, 9, 24, 8, 0)
	h := connectedHarness(t, now)
	h.calendar("events-1000001")

	shown := h.todayText(now)

	if !strings.Contains(strings.ToLower(shown), "clear") {
		t.Errorf("an empty day did not say so:\n%s", shown)
	}
}

func TestTodayLeavesEventDescriptionsOut(t *testing.T) {
	now := moment(2026, 9, 24, 8, 0)
	h := connectedHarness(t, now)
	calendar := h.calendar("events-1000001")
	calendar.Put("private", timedEvent("private", "One to one",
		moment(2026, 9, 24, 9, 0), moment(2026, 9, 24, 10, 0),
		"DESCRIPTION:Discuss the pay review and the reorganisation"))

	shown := h.todayText(now)

	if strings.Contains(shown, "pay review") {
		t.Errorf("an event description was reproduced in a post:\n%s", shown)
	}
	if !strings.Contains(shown, "One to one") {
		t.Errorf("the event itself was not shown:\n%s", shown)
	}
}

func TestProviderTextIsEscapedBeforeItReachesAPost(t *testing.T) {
	now := moment(2026, 9, 24, 8, 0)
	h := connectedHarness(t, now)
	calendar := h.calendar("events-1000001")
	calendar.Put("odd", timedEvent("odd", "Review [click here](http://example.invalid) *now*",
		moment(2026, 9, 24, 9, 0), moment(2026, 9, 24, 10, 0)))

	shown := h.todayText(now)

	if strings.Contains(shown, "[click here](http://example.invalid)") {
		t.Errorf("a title from the provider was rendered as a link:\n%s", shown)
	}
	if !strings.Contains(shown, "click here") {
		t.Errorf("the title was dropped rather than escaped:\n%s", shown)
	}
}

func TestAnUnreadableEventIsSkippedAndTheRestStillArrive(t *testing.T) {
	now := moment(2026, 9, 24, 8, 0)
	h := connectedHarness(t, now)
	calendar := h.calendar("events-1000001")
	calendar.Put("broken", "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nBEGIN:VEVENT\r\nUID:broken\r\n"+
		"SUMMARY:Nonsense\r\nDTSTART;TZID=Mars/Olympus:20260924T090000\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n")
	calendar.Put("fine", timedEvent("fine", "Perfectly fine meeting",
		moment(2026, 9, 24, 11, 0), moment(2026, 9, 24, 12, 0)))

	shown := h.todayText(now)

	if !strings.Contains(shown, "Perfectly fine meeting") {
		t.Errorf("one bad entry stopped the others:\n%s", shown)
	}
	if strings.Contains(shown, "Mars/Olympus") || strings.Contains(strings.ToLower(shown), "error") {
		t.Errorf("the person was shown the technical detail:\n%s", shown)
	}
	if !strings.Contains(strings.Join(h.logged(), "\n"), "Skipped an unreadable calendar entry") {
		t.Errorf("the skipped entry was not logged: %v", h.logged())
	}
}

func TestTimesAreShownInTheReadersOwnTimezone(t *testing.T) {
	now := moment(2026, 9, 24, 8, 0)
	h := connectedHarness(t, now)
	calendar := h.calendar("events-1000001")
	// Moscow is UTC+3 all year; Berlin in September is UTC+2. So 14:00 in
	// Moscow is 13:00 in Berlin.
	calendar.Put("m", timedEvent("m", "All-hands", moment(2026, 9, 24, 14, 0), moment(2026, 9, 24, 14, 30)))

	inMoscow := h.todayText(now)
	if !strings.Contains(inMoscow, "14:00") {
		t.Errorf("a Moscow reader was not shown 14:00:\n%s", inMoscow)
	}

	h.api.setTimezone("Europe/Berlin")
	inBerlin := h.todayText(now)
	if !strings.Contains(inBerlin, "13:00") {
		t.Errorf("a Berlin reader was not shown 13:00:\n%s", inBerlin)
	}
	if strings.Contains(inBerlin, "14:00") {
		t.Errorf("a Berlin reader was shown the Moscow time:\n%s", inBerlin)
	}
}

func TestTheDayIsTheReadersOwnDay(t *testing.T) {
	// 00:30 on the 25th in Moscow is 23:30 on the 24th in Berlin, so the two
	// readers are asking about different days.
	now := moment(2026, 9, 25, 0, 30)
	h := connectedHarness(t, now)
	calendar := h.calendar("events-1000001")
	calendar.Put("late", timedEvent("late", "Late call", moment(2026, 9, 25, 1, 30), moment(2026, 9, 25, 2, 0)))
	calendar.Put("earlier", timedEvent("earlier", "Yesterday evening", moment(2026, 9, 24, 22, 0), moment(2026, 9, 24, 23, 0)))

	inMoscow := h.todayText(now)
	if !strings.Contains(inMoscow, "Late call") || strings.Contains(inMoscow, "Yesterday evening") {
		t.Errorf("Moscow's day is wrong:\n%s", inMoscow)
	}

	h.api.setTimezone("Europe/Berlin")
	inBerlin := h.todayText(now)
	if !strings.Contains(inBerlin, "Yesterday evening") {
		t.Errorf("Berlin, where it is still the 24th, was not shown that evening's meeting:\n%s", inBerlin)
	}
}

func TestAnAllDayEventAppearsInTheDaysList(t *testing.T) {
	now := moment(2026, 9, 24, 8, 0)
	h := connectedHarness(t, now)
	calendar := h.calendar("events-1000001")
	calendar.Put("holiday", allDayEvent("holiday", "Public holiday", moment(2026, 9, 24, 0, 0)))

	shown := h.todayText(now)

	if !strings.Contains(shown, "Public holiday") {
		t.Errorf("an all-day event was not listed:\n%s", shown)
	}
	if !strings.Contains(shown, "All day") {
		t.Errorf("an all-day event was given a time of day:\n%s", shown)
	}
}

func TestAnEventAlreadyRunningIsStillInTheDaysList(t *testing.T) {
	now := moment(2026, 9, 24, 11, 0)
	h := connectedHarness(t, now)
	calendar := h.calendar("events-1000001")
	calendar.Put("long", timedEvent("long", "Workshop", moment(2026, 9, 24, 9, 0), moment(2026, 9, 24, 17, 0)))

	shown := h.todayText(now)

	if !strings.Contains(shown, "Workshop") {
		t.Errorf("a meeting that is happening right now was left out of today:\n%s", shown)
	}
}

func TestTheConferenceLinkIsOfferedWhenThereIsOne(t *testing.T) {
	now := moment(2026, 9, 24, 8, 0)
	h := connectedHarness(t, now)
	calendar := h.calendar("events-1000001")
	calendar.Put("call", timedEvent("call", "Video call",
		moment(2026, 9, 24, 9, 0), moment(2026, 9, 24, 10, 0),
		"X-TELEMOST-CONFERENCE:https://telemost.yandex.ru/j/9900"))

	shown := h.todayText(now)

	if !strings.Contains(shown, "https://telemost.yandex.ru/j/9900") {
		t.Errorf("the conference link was not offered:\n%s", shown)
	}
}

func TestADangerousConferenceLinkIsNotRendered(t *testing.T) {
	now := moment(2026, 9, 24, 8, 0)
	h := connectedHarness(t, now)
	calendar := h.calendar("events-1000001")
	calendar.Put("bad", timedEvent("bad", "Suspicious call",
		moment(2026, 9, 24, 9, 0), moment(2026, 9, 24, 10, 0),
		"X-TELEMOST-CONFERENCE:javascript:alert(1)"))

	shown := h.todayText(now)

	if strings.Contains(shown, "javascript:") {
		t.Errorf("a link that is not a web address was rendered:\n%s", shown)
	}
}

// Recurring Events, ticket 05, end to end.

func TestAStandingEventAppearsOnItsDay(t *testing.T) {
	// The series started weeks ago; today is one of its Thursdays.
	now := moment(2026, 9, 24, 8, 0)
	h := connectedHarness(t, now)
	calendar := h.calendar("events-1000001")
	calendar.Put("weekly", recurringEvent("weekly", "Weekly sync",
		moment(2026, 8, 6, 10, 0), moment(2026, 8, 6, 11, 0), "FREQ=WEEKLY;BYDAY=TH"))

	shown := h.todayText(now)

	if !strings.Contains(shown, "Weekly sync") || !strings.Contains(shown, "10:00") {
		t.Errorf("a standing meeting did not appear at its time:\n%s", shown)
	}
}

func TestAStandingEventDoesNotAppearOnADayItDoesNotFallOn(t *testing.T) {
	now := moment(2026, 9, 25, 8, 0) // a Friday
	h := connectedHarness(t, now)
	calendar := h.calendar("events-1000001")
	calendar.Put("weekly", recurringEvent("weekly", "Weekly sync",
		moment(2026, 8, 6, 10, 0), moment(2026, 8, 6, 11, 0), "FREQ=WEEKLY;BYDAY=TH"))

	shown := h.todayText(now)

	if strings.Contains(shown, "Weekly sync") {
		t.Errorf("a Thursday meeting appeared on a Friday:\n%s", shown)
	}
}

func TestACancelledOccurrenceDoesNotAppear(t *testing.T) {
	now := moment(2026, 9, 24, 8, 0)
	h := connectedHarness(t, now)
	calendar := h.calendar("events-1000001")
	calendar.Put("weekly", seriesWithOverride("weekly", "Weekly sync",
		moment(2026, 8, 6, 10, 0), moment(2026, 8, 6, 11, 0), "FREQ=WEEKLY;BYDAY=TH",
		cancelledOccurrence("weekly", "Weekly sync", moment(2026, 9, 24, 10, 0))))

	shown := h.todayText(now)

	if strings.Contains(shown, "Weekly sync") {
		t.Errorf("an occurrence the person called off still appeared:\n%s", shown)
	}
}

func TestAMovedOccurrenceAppearsAtItsNewTime(t *testing.T) {
	now := moment(2026, 9, 24, 8, 0)
	h := connectedHarness(t, now)
	calendar := h.calendar("events-1000001")
	calendar.Put("weekly", seriesWithOverride("weekly", "Weekly sync",
		moment(2026, 8, 6, 10, 0), moment(2026, 8, 6, 11, 0), "FREQ=WEEKLY;BYDAY=TH",
		movedOccurrence("weekly", "Weekly sync",
			moment(2026, 9, 24, 10, 0), moment(2026, 9, 24, 15, 30), moment(2026, 9, 24, 16, 30))))

	shown := h.todayText(now)

	if !strings.Contains(shown, "15:30") {
		t.Errorf("a moved occurrence did not appear at its new time:\n%s", shown)
	}
	if strings.Contains(shown, "10:00") {
		t.Errorf("a moved occurrence still appeared at its old time:\n%s", shown)
	}
}

func TestExcludedDatesRemoveAnOccurrence(t *testing.T) {
	now := moment(2026, 9, 24, 8, 0)
	h := connectedHarness(t, now)
	calendar := h.calendar("events-1000001")
	calendar.Put("weekly", recurringEvent("weekly", "Weekly sync",
		moment(2026, 8, 6, 10, 0), moment(2026, 8, 6, 11, 0), "FREQ=WEEKLY;BYDAY=TH",
		"EXDATE;TZID=Europe/Moscow:20260924T100000"))

	shown := h.todayText(now)

	if strings.Contains(shown, "Weekly sync") {
		t.Errorf("an excluded date still produced an occurrence:\n%s", shown)
	}
}

func TestRecurringAndSingleEventsAreListedTogetherInOrder(t *testing.T) {
	now := moment(2026, 9, 24, 8, 0)
	h := connectedHarness(t, now)
	calendar := h.calendar("events-1000001")
	calendar.Put("weekly", recurringEvent("weekly", "Weekly sync",
		moment(2026, 8, 6, 14, 0), moment(2026, 8, 6, 15, 0), "FREQ=WEEKLY;BYDAY=TH"))
	calendar.Put("one-off", timedEvent("one-off", "One-off chat",
		moment(2026, 9, 24, 9, 0), moment(2026, 9, 24, 9, 30)))

	shown := h.todayText(now)

	if strings.Index(shown, "One-off chat") > strings.Index(shown, "Weekly sync") {
		t.Errorf("the day is not in start order:\n%s", shown)
	}
}

func TestQueriesAreAlwaysBoundedInTime(t *testing.T) {
	// A full read with no time filter returns the whole collection, which on a
	// real account is years of history.
	now := moment(2026, 9, 24, 8, 0)
	h := connectedHarness(t, now)
	calendar := h.calendar("events-1000001")
	calendar.Put("ancient", timedEvent("ancient", "A meeting from 2017",
		moment(2017, 3, 1, 10, 0), moment(2017, 3, 1, 11, 0)))

	h.caldav.Reset()
	shown := h.todayText(now)

	if strings.Contains(shown, "2017") {
		t.Errorf("years of history came back:\n%s", shown)
	}
	for _, request := range h.caldav.Requests() {
		if request.Method == "REPORT" && !strings.Contains(request.Body, "time-range") {
			t.Errorf("an unbounded read was issued:\n%s", request.Body)
		}
	}
}
