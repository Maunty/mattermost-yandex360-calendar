package recurrence_test

import (
	"testing"
	"time"

	"github.com/maunty/mattermost-yandex360-calendar/server/internal/calendar"
	"github.com/maunty/mattermost-yandex360-calendar/server/internal/recurrence"
)

// These tests run against the real expansion with no fakes, no mocks and no
// substitution of any kind. Expansion is a pure function; there is nothing
// here to stand in for.

var (
	moscow = mustLoad("Europe/Moscow")
	berlin = mustLoad("Europe/Berlin")
)

func mustLoad(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		panic(err)
	}
	return loc
}

// at builds a time in Moscow, the timezone most of these fixtures live in.
func at(y int, m time.Month, d, h, min int) time.Time {
	return time.Date(y, m, d, h, min, 0, 0, moscow)
}

// event builds a one-hour Event starting at start.
func event(uid, title string, start time.Time) calendar.Event {
	return calendar.Event{
		UID:   uid,
		Title: title,
		Start: start,
		End:   start.Add(time.Hour),
	}
}

// starts renders the expansion as local wall-clock strings, which is what the
// tests actually care about.
func starts(occs []calendar.Occurrence) []string {
	out := make([]string, 0, len(occs))
	for _, o := range occs {
		out = append(out, o.Start.Format("2006-01-02 15:04 MST"))
	}
	return out
}

func assertStarts(t *testing.T, occs []calendar.Occurrence, want ...string) {
	t.Helper()
	got := starts(occs)
	if len(got) != len(want) {
		t.Fatalf("got %d occurrences %v, want %d %v", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("occurrence %d: got %q, want %q", i, got[i], want[i])
		}
	}
}

func TestSingleEventInsideWindow(t *testing.T) {
	e := event("a", "Standup", at(2026, 9, 24, 10, 0))

	occs, problems := recurrence.Expand([]calendar.Event{e}, at(2026, 9, 24, 0, 0), at(2026, 9, 25, 0, 0))

	if len(problems) != 0 {
		t.Fatalf("unexpected problems: %v", problems)
	}
	assertStarts(t, occs, "2026-09-24 10:00 MSK")
	if occs[0].Title != "Standup" {
		t.Errorf("title: got %q", occs[0].Title)
	}
	if !occs[0].End.Equal(at(2026, 9, 24, 11, 0)) {
		t.Errorf("end: got %v", occs[0].End)
	}
}

func TestSingleEventOutsideWindowIsExcluded(t *testing.T) {
	e := event("a", "Yesterday", at(2026, 9, 23, 10, 0))

	occs, _ := recurrence.Expand([]calendar.Event{e}, at(2026, 9, 24, 0, 0), at(2026, 9, 25, 0, 0))

	assertStarts(t, occs)
}

func TestEventOverlappingWindowStartIsIncluded(t *testing.T) {
	// An all-morning meeting that began before the window opened is still
	// happening inside it, so it belongs in the day's list.
	e := calendar.Event{
		UID:   "a",
		Title: "Offsite",
		Start: at(2026, 9, 23, 22, 0),
		End:   at(2026, 9, 24, 4, 0),
	}

	occs, _ := recurrence.Expand([]calendar.Event{e}, at(2026, 9, 24, 0, 0), at(2026, 9, 25, 0, 0))

	assertStarts(t, occs, "2026-09-23 22:00 MSK")
}

func TestCancelledEventProducesNothing(t *testing.T) {
	e := event("a", "Called off", at(2026, 9, 24, 10, 0))
	e.Cancelled = true

	occs, _ := recurrence.Expand([]calendar.Event{e}, at(2026, 9, 24, 0, 0), at(2026, 9, 25, 0, 0))

	assertStarts(t, occs)
}

func TestOccurrencesAreOrderedByStart(t *testing.T) {
	events := []calendar.Event{
		event("c", "Third", at(2026, 9, 24, 16, 0)),
		event("a", "First", at(2026, 9, 24, 9, 0)),
		event("b", "Second", at(2026, 9, 24, 11, 30)),
	}

	occs, _ := recurrence.Expand(events, at(2026, 9, 24, 0, 0), at(2026, 9, 25, 0, 0))

	assertStarts(t, occs,
		"2026-09-24 09:00 MSK",
		"2026-09-24 11:30 MSK",
		"2026-09-24 16:00 MSK",
	)
}

func TestAllDayEventIsMarked(t *testing.T) {
	e := calendar.Event{
		UID:    "a",
		Title:  "Public holiday",
		Start:  time.Date(2026, 9, 24, 0, 0, 0, 0, moscow),
		End:    time.Date(2026, 9, 25, 0, 0, 0, 0, moscow),
		AllDay: true,
	}

	occs, _ := recurrence.Expand([]calendar.Event{e}, at(2026, 9, 24, 0, 0), at(2026, 9, 25, 0, 0))

	if len(occs) != 1 || !occs[0].AllDay {
		t.Fatalf("expected one all-day occurrence, got %+v", occs)
	}
}

func TestDailyFrequency(t *testing.T) {
	e := event("a", "Daily", at(2026, 9, 24, 10, 0))
	e.RRule = "FREQ=DAILY"

	occs, _ := recurrence.Expand([]calendar.Event{e}, at(2026, 9, 24, 0, 0), at(2026, 9, 27, 0, 0))

	assertStarts(t, occs,
		"2026-09-24 10:00 MSK",
		"2026-09-25 10:00 MSK",
		"2026-09-26 10:00 MSK",
	)
}

func TestWeeklyFrequency(t *testing.T) {
	e := event("a", "Weekly standing meeting", at(2026, 9, 24, 10, 0))
	e.RRule = "FREQ=WEEKLY"

	occs, _ := recurrence.Expand([]calendar.Event{e}, at(2026, 9, 24, 0, 0), at(2026, 10, 16, 0, 0))

	assertStarts(t, occs,
		"2026-09-24 10:00 MSK",
		"2026-10-01 10:00 MSK",
		"2026-10-08 10:00 MSK",
		"2026-10-15 10:00 MSK",
	)
}

func TestMonthlyFrequency(t *testing.T) {
	e := event("a", "Monthly", at(2026, 9, 3, 10, 0))
	e.RRule = "FREQ=MONTHLY"

	occs, _ := recurrence.Expand([]calendar.Event{e}, at(2026, 9, 1, 0, 0), at(2026, 12, 1, 0, 0))

	assertStarts(t, occs,
		"2026-09-03 10:00 MSK",
		"2026-10-03 10:00 MSK",
		"2026-11-03 10:00 MSK",
	)
}

func TestYearlyFrequency(t *testing.T) {
	e := event("a", "Yearly", at(2024, 2, 29, 10, 0))
	e.RRule = "FREQ=YEARLY"

	occs, _ := recurrence.Expand([]calendar.Event{e}, at(2024, 1, 1, 0, 0), at(2033, 1, 1, 0, 0))

	// 29 February exists only in leap years, and the rule does not invent it.
	assertStarts(t, occs,
		"2024-02-29 10:00 MSK",
		"2028-02-29 10:00 MSK",
		"2032-02-29 10:00 MSK",
	)
}

func TestIntervalIsHonoured(t *testing.T) {
	e := event("a", "Fortnightly", at(2026, 9, 24, 10, 0))
	e.RRule = "FREQ=WEEKLY;INTERVAL=2"

	occs, _ := recurrence.Expand([]calendar.Event{e}, at(2026, 9, 24, 0, 0), at(2026, 10, 23, 0, 0))

	assertStarts(t, occs,
		"2026-09-24 10:00 MSK",
		"2026-10-08 10:00 MSK",
		"2026-10-22 10:00 MSK",
	)
}

func TestByDayRule(t *testing.T) {
	e := event("a", "Mondays and Wednesdays", at(2026, 9, 21, 10, 0)) // a Monday
	e.RRule = "FREQ=WEEKLY;BYDAY=MO,WE"

	occs, _ := recurrence.Expand([]calendar.Event{e}, at(2026, 9, 21, 0, 0), at(2026, 9, 28, 0, 0))

	assertStarts(t, occs,
		"2026-09-21 10:00 MSK",
		"2026-09-23 10:00 MSK",
	)
}

func TestBySetPositionLastWorkingDayOfMonth(t *testing.T) {
	e := event("a", "Month end", at(2026, 9, 30, 17, 0))
	e.RRule = "FREQ=MONTHLY;BYDAY=MO,TU,WE,TH,FR;BYSETPOS=-1"

	occs, _ := recurrence.Expand([]calendar.Event{e}, at(2026, 9, 1, 0, 0), at(2027, 1, 1, 0, 0))

	// 31 October 2026 is a Saturday and 1 November a Sunday, so October's last
	// working day is Friday the 30th. 31 December is a Thursday.
	assertStarts(t, occs,
		"2026-09-30 17:00 MSK",
		"2026-10-30 17:00 MSK",
		"2026-11-30 17:00 MSK",
		"2026-12-31 17:00 MSK",
	)
}

func TestFirstMondayOfTheMonth(t *testing.T) {
	e := event("a", "First Monday", at(2026, 9, 7, 10, 0))
	e.RRule = "FREQ=MONTHLY;BYDAY=1MO"

	occs, _ := recurrence.Expand([]calendar.Event{e}, at(2026, 9, 1, 0, 0), at(2026, 12, 1, 0, 0))

	assertStarts(t, occs,
		"2026-09-07 10:00 MSK",
		"2026-10-05 10:00 MSK",
		"2026-11-02 10:00 MSK",
	)
}

func TestCountLimitIsHonoured(t *testing.T) {
	e := event("a", "Three times only", at(2026, 9, 24, 10, 0))
	e.RRule = "FREQ=DAILY;COUNT=3"

	occs, _ := recurrence.Expand([]calendar.Event{e}, at(2026, 9, 24, 0, 0), at(2026, 10, 24, 0, 0))

	assertStarts(t, occs,
		"2026-09-24 10:00 MSK",
		"2026-09-25 10:00 MSK",
		"2026-09-26 10:00 MSK",
	)
}

func TestCountIsCountedFromTheSeriesStartNotTheWindow(t *testing.T) {
	// The series began long before the window. Its three occurrences are
	// already used up, so the window must be empty — a naive expansion that
	// counts from the window start would wrongly produce three more.
	e := event("a", "Three times only", at(2026, 1, 1, 10, 0))
	e.RRule = "FREQ=DAILY;COUNT=3"

	occs, _ := recurrence.Expand([]calendar.Event{e}, at(2026, 9, 24, 0, 0), at(2026, 10, 24, 0, 0))

	assertStarts(t, occs)
}

func TestUntilLimitIsHonoured(t *testing.T) {
	e := event("a", "Until October", at(2026, 9, 24, 10, 0))
	e.RRule = "FREQ=DAILY;UNTIL=20260926T070000Z" // 10:00 MSK on the 26th

	occs, _ := recurrence.Expand([]calendar.Event{e}, at(2026, 9, 24, 0, 0), at(2026, 10, 24, 0, 0))

	assertStarts(t, occs,
		"2026-09-24 10:00 MSK",
		"2026-09-25 10:00 MSK",
		"2026-09-26 10:00 MSK",
	)
}

func TestExcludedDateRemovesThatOccurrence(t *testing.T) {
	e := event("a", "Daily except Friday", at(2026, 9, 24, 10, 0))
	e.RRule = "FREQ=DAILY"
	e.ExDates = []time.Time{at(2026, 9, 25, 10, 0)}

	occs, _ := recurrence.Expand([]calendar.Event{e}, at(2026, 9, 24, 0, 0), at(2026, 9, 27, 0, 0))

	assertStarts(t, occs,
		"2026-09-24 10:00 MSK",
		"2026-09-26 10:00 MSK",
	)
}

func TestExcludedDateGivenInAnotherTimezoneStillMatches(t *testing.T) {
	// Providers write EXDATE with whatever TZID they like. What identifies the
	// occurrence is the instant, not the spelling.
	e := event("a", "Daily", at(2026, 9, 24, 10, 0))
	e.RRule = "FREQ=DAILY"
	e.ExDates = []time.Time{at(2026, 9, 25, 10, 0).UTC()}

	occs, _ := recurrence.Expand([]calendar.Event{e}, at(2026, 9, 24, 0, 0), at(2026, 9, 27, 0, 0))

	assertStarts(t, occs,
		"2026-09-24 10:00 MSK",
		"2026-09-26 10:00 MSK",
	)
}

func TestRecurrenceDateAddsAnOccurrence(t *testing.T) {
	e := event("a", "Weekly plus one", at(2026, 9, 24, 10, 0))
	e.RRule = "FREQ=WEEKLY"
	e.RDates = []time.Time{at(2026, 9, 26, 15, 0)}

	occs, _ := recurrence.Expand([]calendar.Event{e}, at(2026, 9, 24, 0, 0), at(2026, 10, 2, 0, 0))

	assertStarts(t, occs,
		"2026-09-24 10:00 MSK",
		"2026-09-26 15:00 MSK",
		"2026-10-01 10:00 MSK",
	)
}

func TestOverriddenOccurrenceAppearsAtItsOverriddenTimeAndDetails(t *testing.T) {
	master := event("a", "Weekly sync", at(2026, 9, 24, 10, 0))
	master.RRule = "FREQ=WEEKLY"

	moved := event("a", "Weekly sync (moved)", at(2026, 10, 1, 15, 30))
	moved.RecurrenceID = at(2026, 10, 1, 10, 0)
	moved.Location = "Room 2"

	occs, _ := recurrence.Expand([]calendar.Event{master, moved}, at(2026, 9, 24, 0, 0), at(2026, 10, 9, 0, 0))

	assertStarts(t, occs,
		"2026-09-24 10:00 MSK",
		"2026-10-01 15:30 MSK",
		"2026-10-08 10:00 MSK",
	)
	if occs[1].Title != "Weekly sync (moved)" || occs[1].Location != "Room 2" {
		t.Errorf("override details not used: %+v", occs[1])
	}
}

func TestCancelledOccurrenceDoesNotAppear(t *testing.T) {
	master := event("a", "Weekly sync", at(2026, 9, 24, 10, 0))
	master.RRule = "FREQ=WEEKLY"

	off := event("a", "Weekly sync", at(2026, 10, 1, 10, 0))
	off.RecurrenceID = at(2026, 10, 1, 10, 0)
	off.Cancelled = true

	occs, _ := recurrence.Expand([]calendar.Event{master, off}, at(2026, 9, 24, 0, 0), at(2026, 10, 9, 0, 0))

	assertStarts(t, occs,
		"2026-09-24 10:00 MSK",
		"2026-10-08 10:00 MSK",
	)
}

func TestOccurrenceMovedOutOfTheWindowDoesNotAppearAtItsOldTime(t *testing.T) {
	master := event("a", "Weekly sync", at(2026, 9, 24, 10, 0))
	master.RRule = "FREQ=WEEKLY"

	moved := event("a", "Weekly sync", at(2026, 10, 5, 10, 0))
	moved.RecurrenceID = at(2026, 10, 1, 10, 0)

	occs, _ := recurrence.Expand([]calendar.Event{master, moved}, at(2026, 10, 1, 0, 0), at(2026, 10, 2, 0, 0))

	assertStarts(t, occs)
}

func TestOccurrenceMovedIntoTheWindowAppears(t *testing.T) {
	master := event("a", "Weekly sync", at(2026, 9, 24, 10, 0))
	master.RRule = "FREQ=WEEKLY"

	moved := event("a", "Weekly sync", at(2026, 10, 3, 9, 0))
	moved.RecurrenceID = at(2026, 10, 1, 10, 0)

	occs, _ := recurrence.Expand([]calendar.Event{master, moved}, at(2026, 10, 3, 0, 0), at(2026, 10, 4, 0, 0))

	assertStarts(t, occs, "2026-10-03 09:00 MSK")
}

func TestMovedOccurrenceKeepsTheInstanceKeyOfTheOccurrenceItReplaces(t *testing.T) {
	// A Reminder recorded against the occurrence before it moved must still
	// match it afterwards, or moving a meeting sends the Reminder twice.
	master := event("a", "Weekly sync", at(2026, 9, 24, 10, 0))
	master.RRule = "FREQ=WEEKLY"

	before, _ := recurrence.Expand([]calendar.Event{master}, at(2026, 10, 1, 0, 0), at(2026, 10, 2, 0, 0))

	moved := event("a", "Weekly sync", at(2026, 10, 1, 15, 0))
	moved.RecurrenceID = at(2026, 10, 1, 10, 0)
	after, _ := recurrence.Expand([]calendar.Event{master, moved}, at(2026, 10, 1, 0, 0), at(2026, 10, 2, 0, 0))

	if len(before) != 1 || len(after) != 1 {
		t.Fatalf("expected one occurrence each, got %d and %d", len(before), len(after))
	}
	if before[0].InstanceKey() != after[0].InstanceKey() {
		t.Errorf("instance key changed when the occurrence moved: %q then %q",
			before[0].InstanceKey(), after[0].InstanceKey())
	}
}

func TestExpansionAcrossADaylightSavingTransitionKeepsLocalTime(t *testing.T) {
	// 29 March 2026 is when Berlin moves to summer time. A 10:00 meeting is a
	// 10:00 meeting on both sides of it, even though the UTC instant shifts.
	e := calendar.Event{
		UID:   "a",
		Title: "Berlin weekly",
		Start: time.Date(2026, 3, 22, 10, 0, 0, 0, berlin),
		End:   time.Date(2026, 3, 22, 11, 0, 0, 0, berlin),
		RRule: "FREQ=WEEKLY;COUNT=3",
	}

	occs, _ := recurrence.Expand(
		[]calendar.Event{e},
		time.Date(2026, 3, 1, 0, 0, 0, 0, berlin),
		time.Date(2026, 5, 1, 0, 0, 0, 0, berlin),
	)

	assertStarts(t, occs,
		"2026-03-22 10:00 CET",
		"2026-03-29 10:00 CEST",
		"2026-04-05 10:00 CEST",
	)
	// The instants differ by one hour less than a week across the transition.
	if got := occs[1].Start.Sub(occs[0].Start); got != 7*24*time.Hour-time.Hour {
		t.Errorf("interval across the transition: got %v", got)
	}
	// Each occurrence still runs for the hour it was scheduled for.
	for i, o := range occs {
		if got := o.End.Sub(o.Start); got != time.Hour {
			t.Errorf("occurrence %d lasted %v, want 1h", i, got)
		}
	}
}

func TestExpansionAcrossTheAutumnTransition(t *testing.T) {
	e := calendar.Event{
		UID:   "a",
		Title: "Berlin weekly",
		Start: time.Date(2026, 10, 18, 10, 0, 0, 0, berlin),
		End:   time.Date(2026, 10, 18, 11, 0, 0, 0, berlin),
		RRule: "FREQ=WEEKLY;COUNT=3",
	}

	occs, _ := recurrence.Expand(
		[]calendar.Event{e},
		time.Date(2026, 10, 1, 0, 0, 0, 0, berlin),
		time.Date(2026, 11, 30, 0, 0, 0, 0, berlin),
	)

	assertStarts(t, occs,
		"2026-10-18 10:00 CEST",
		"2026-10-25 10:00 CET",
		"2026-11-01 10:00 CET",
	)
}

func TestUnreadableRuleIsReportedAndTheOtherEventsSurvive(t *testing.T) {
	bad := event("bad", "Nonsense rule", at(2026, 9, 24, 9, 0))
	bad.RRule = "FREQ=NEVER;INTERVAL=banana"
	good := event("good", "Fine", at(2026, 9, 24, 10, 0))

	occs, problems := recurrence.Expand([]calendar.Event{bad, good}, at(2026, 9, 24, 0, 0), at(2026, 9, 25, 0, 0))

	assertStarts(t, occs, "2026-09-24 10:00 MSK")
	if len(problems) != 1 {
		t.Fatalf("expected one problem, got %v", problems)
	}
	if problems[0].UID != "bad" {
		t.Errorf("problem names the wrong Event: %+v", problems[0])
	}
}

func TestEventsFromSeveralCalendarsAreExpandedTogether(t *testing.T) {
	work := event("w", "Work meeting", at(2026, 9, 24, 9, 0))
	work.Source = "events-1"
	personal := event("p", "Dentist", at(2026, 9, 24, 8, 0))
	personal.Source = "events-2"

	occs, _ := recurrence.Expand([]calendar.Event{work, personal}, at(2026, 9, 24, 0, 0), at(2026, 9, 25, 0, 0))

	assertStarts(t, occs,
		"2026-09-24 08:00 MSK",
		"2026-09-24 09:00 MSK",
	)
}

func TestLongRunningSeriesDoesNotExpandUnbounded(t *testing.T) {
	// A daily meeting running since 2017 with no end, asked for one day. The
	// answer is one occurrence, and the work to get there must not be
	// proportional to the nine years in between.
	e := event("a", "Since forever", at(2017, 1, 1, 10, 0))
	e.RRule = "FREQ=DAILY"

	occs, _ := recurrence.Expand([]calendar.Event{e}, at(2026, 9, 24, 0, 0), at(2026, 9, 25, 0, 0))

	assertStarts(t, occs, "2026-09-24 10:00 MSK")
}

func TestOverrideOfAnEventWithNoMasterInTheWindowStillAppears(t *testing.T) {
	// The master's own occurrences are all outside the window, but one
	// occurrence was moved into it. The plugin holds both records.
	master := event("a", "Weekly", at(2026, 1, 5, 10, 0))
	master.RRule = "FREQ=WEEKLY;COUNT=5"
	moved := event("a", "Weekly, rescheduled far out", at(2026, 9, 24, 14, 0))
	moved.RecurrenceID = at(2026, 2, 2, 10, 0)

	occs, _ := recurrence.Expand([]calendar.Event{master, moved}, at(2026, 9, 24, 0, 0), at(2026, 9, 25, 0, 0))

	assertStarts(t, occs, "2026-09-24 14:00 MSK")
}
