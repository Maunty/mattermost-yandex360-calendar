package calendar_test

import (
	"strings"
	"testing"
	"time"

	"github.com/maunty/mattermost-yandex360-calendar/server/internal/calendar"
)

// object wraps VEVENT bodies in the VCALENDAR envelope a CalDAV collection
// actually returns, so the tests below read as the fragments that matter.
func object(body ...string) string {
	return "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//Yandex LLC//Yandex Calendar//EN\r\n" +
		strings.Join(body, "") + "END:VCALENDAR\r\n"
}

// moscowZone is the inline VTIMEZONE Yandex ships with every event, trimmed to
// the transitions that matter.
const moscowZone = "BEGIN:VTIMEZONE\r\n" +
	"TZID:Europe/Moscow\r\n" +
	"BEGIN:STANDARD\r\n" +
	"DTSTART:19700101T000000\r\n" +
	"TZOFFSETFROM:+0300\r\n" +
	"TZOFFSETTO:+0300\r\n" +
	"TZNAME:MSK\r\n" +
	"END:STANDARD\r\n" +
	"END:VTIMEZONE\r\n"

func parseOne(t *testing.T, data string) calendar.Event {
	t.Helper()
	events, err := calendar.ParseObject(data)
	if err != nil {
		t.Fatalf("ParseObject: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("got %d events, want 1", len(events))
	}
	return events[0]
}

func TestParseTimedEventInItsOwnTimezone(t *testing.T) {
	e := parseOne(t, object(moscowZone,
		"BEGIN:VEVENT\r\n"+
			"UID:abc-123\r\n"+
			"SUMMARY:Weekly sync\r\n"+
			"LOCATION:Room 3\r\n"+
			"DTSTART;TZID=Europe/Moscow:20260924T100000\r\n"+
			"DTEND;TZID=Europe/Moscow:20260924T110000\r\n"+
			"END:VEVENT\r\n"))

	if e.UID != "abc-123" || e.Title != "Weekly sync" || e.Location != "Room 3" {
		t.Errorf("fields: %+v", e)
	}
	if got := e.Start.UTC(); !got.Equal(time.Date(2026, 9, 24, 7, 0, 0, 0, time.UTC)) {
		t.Errorf("start resolved to %v, want 07:00Z", got)
	}
	if name, _ := e.Start.Zone(); name != "MSK" {
		t.Errorf("start kept zone %q, want MSK", name)
	}
	if e.Duration() != time.Hour {
		t.Errorf("duration: %v", e.Duration())
	}
	if e.AllDay {
		t.Error("a timed event was read as all-day")
	}
}

func TestParseEventInUTC(t *testing.T) {
	e := parseOne(t, object(
		"BEGIN:VEVENT\r\n"+
			"UID:u\r\n"+
			"SUMMARY:UTC meeting\r\n"+
			"DTSTART:20260924T090000Z\r\n"+
			"DTEND:20260924T093000Z\r\n"+
			"END:VEVENT\r\n"))

	if !e.Start.Equal(time.Date(2026, 9, 24, 9, 0, 0, 0, time.UTC)) {
		t.Errorf("start: %v", e.Start)
	}
	if e.Duration() != 30*time.Minute {
		t.Errorf("duration: %v", e.Duration())
	}
}

func TestParseAllDayEvent(t *testing.T) {
	e := parseOne(t, object(
		"BEGIN:VEVENT\r\n"+
			"UID:holiday\r\n"+
			"SUMMARY:Public holiday\r\n"+
			"DTSTART;VALUE=DATE:20260924\r\n"+
			"DTEND;VALUE=DATE:20260925\r\n"+
			"END:VEVENT\r\n"))

	if !e.AllDay {
		t.Error("an event with a date and no time was not read as all-day")
	}
	if e.Duration() != 24*time.Hour {
		t.Errorf("duration: %v", e.Duration())
	}
}

func TestParseAllDayEventWithNoEndCoversOneDay(t *testing.T) {
	e := parseOne(t, object(
		"BEGIN:VEVENT\r\nUID:h\r\nSUMMARY:Day off\r\nDTSTART;VALUE=DATE:20260924\r\nEND:VEVENT\r\n"))

	if !e.AllDay || e.Duration() != 24*time.Hour {
		t.Errorf("got allDay=%v duration=%v", e.AllDay, e.Duration())
	}
}

func TestParseEventWithDurationInsteadOfEnd(t *testing.T) {
	e := parseOne(t, object(moscowZone,
		"BEGIN:VEVENT\r\n"+
			"UID:u\r\n"+
			"SUMMARY:Ninety minutes\r\n"+
			"DTSTART;TZID=Europe/Moscow:20260924T100000\r\n"+
			"DURATION:PT1H30M\r\n"+
			"END:VEVENT\r\n"))

	if e.Duration() != 90*time.Minute {
		t.Errorf("duration: %v", e.Duration())
	}
}

func TestParseRecurrenceRuleAndExcludedDates(t *testing.T) {
	e := parseOne(t, object(moscowZone,
		"BEGIN:VEVENT\r\n"+
			"UID:weekly\r\n"+
			"SUMMARY:Standing meeting\r\n"+
			"DTSTART;TZID=Europe/Moscow:20260924T100000\r\n"+
			"DTEND;TZID=Europe/Moscow:20260924T110000\r\n"+
			"RRULE:FREQ=WEEKLY;BYDAY=TH\r\n"+
			"EXDATE;TZID=Europe/Moscow:20261001T100000,20261008T100000\r\n"+
			"END:VEVENT\r\n"))

	if e.RRule != "FREQ=WEEKLY;BYDAY=TH" {
		t.Errorf("rule: %q", e.RRule)
	}
	if len(e.ExDates) != 2 {
		t.Fatalf("got %d excluded dates, want 2 from one comma-separated property", len(e.ExDates))
	}
	if !e.ExDates[1].UTC().Equal(time.Date(2026, 10, 8, 7, 0, 0, 0, time.UTC)) {
		t.Errorf("second excluded date: %v", e.ExDates[1])
	}
}

func TestParseRepeatedExcludedDateProperties(t *testing.T) {
	e := parseOne(t, object(moscowZone,
		"BEGIN:VEVENT\r\n"+
			"UID:weekly\r\n"+
			"DTSTART;TZID=Europe/Moscow:20260924T100000\r\n"+
			"DTEND;TZID=Europe/Moscow:20260924T110000\r\n"+
			"RRULE:FREQ=WEEKLY\r\n"+
			"EXDATE;TZID=Europe/Moscow:20261001T100000\r\n"+
			"EXDATE;TZID=Europe/Moscow:20261008T100000\r\n"+
			"END:VEVENT\r\n"))

	if len(e.ExDates) != 2 {
		t.Fatalf("got %d excluded dates, want 2", len(e.ExDates))
	}
}

func TestParseMasterAndOverrideFromOneObject(t *testing.T) {
	events, err := calendar.ParseObject(object(moscowZone,
		"BEGIN:VEVENT\r\n"+
			"UID:weekly\r\n"+
			"SUMMARY:Standing meeting\r\n"+
			"DTSTART;TZID=Europe/Moscow:20260924T100000\r\n"+
			"DTEND;TZID=Europe/Moscow:20260924T110000\r\n"+
			"RRULE:FREQ=WEEKLY\r\n"+
			"END:VEVENT\r\n",
		"BEGIN:VEVENT\r\n"+
			"UID:weekly\r\n"+
			"SUMMARY:Standing meeting (later)\r\n"+
			"RECURRENCE-ID;TZID=Europe/Moscow:20261001T100000\r\n"+
			"DTSTART;TZID=Europe/Moscow:20261001T150000\r\n"+
			"DTEND;TZID=Europe/Moscow:20261001T160000\r\n"+
			"END:VEVENT\r\n"))
	if err != nil {
		t.Fatalf("ParseObject: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("got %d events, want a master and an override", len(events))
	}
	if events[0].IsOverride() {
		t.Error("the master was read as an override")
	}
	if !events[1].IsOverride() {
		t.Fatal("the override was read as a master")
	}
	if !events[1].RecurrenceID.UTC().Equal(time.Date(2026, 10, 1, 7, 0, 0, 0, time.UTC)) {
		t.Errorf("recurrence id: %v", events[1].RecurrenceID)
	}
}

func TestParseCancelledStatus(t *testing.T) {
	e := parseOne(t, object(
		"BEGIN:VEVENT\r\nUID:u\r\nSUMMARY:Called off\r\nSTATUS:CANCELLED\r\n"+
			"DTSTART:20260924T090000Z\r\nDTEND:20260924T100000Z\r\nEND:VEVENT\r\n"))

	if !e.Cancelled {
		t.Error("STATUS:CANCELLED was not read")
	}
}

func TestParseConferenceLink(t *testing.T) {
	e := parseOne(t, object(
		"BEGIN:VEVENT\r\nUID:u\r\nSUMMARY:Call\r\n"+
			"DTSTART:20260924T090000Z\r\nDTEND:20260924T100000Z\r\n"+
			"X-TELEMOST-CONFERENCE:https://telemost.yandex.ru/j/12345\r\n"+
			"END:VEVENT\r\n"))

	if e.Conference != "https://telemost.yandex.ru/j/12345" {
		t.Errorf("conference: %q", e.Conference)
	}
}

func TestParseUnescapesText(t *testing.T) {
	e := parseOne(t, object(
		"BEGIN:VEVENT\r\nUID:u\r\nSUMMARY:Budget\\, final\r\nLOCATION:Floor 2\\; room 3\r\n"+
			"DTSTART:20260924T090000Z\r\nDTEND:20260924T100000Z\r\nEND:VEVENT\r\n"))

	if e.Title != "Budget, final" {
		t.Errorf("title: %q", e.Title)
	}
	if e.Location != "Floor 2; room 3" {
		t.Errorf("location: %q", e.Location)
	}
}

func TestParseFallsBackToTheInlineTimezoneDefinition(t *testing.T) {
	// A TZID the system database has never heard of. The offset has to come
	// from the definition the provider shipped inside the object.
	e := parseOne(t, object(
		"BEGIN:VTIMEZONE\r\n"+
			"TZID:Custom/Somewhere\r\n"+
			"BEGIN:STANDARD\r\n"+
			"DTSTART:19700101T000000\r\n"+
			"TZOFFSETFROM:+0500\r\n"+
			"TZOFFSETTO:+0500\r\n"+
			"TZNAME:PLUS5\r\n"+
			"END:STANDARD\r\n"+
			"END:VTIMEZONE\r\n",
		"BEGIN:VEVENT\r\nUID:u\r\nSUMMARY:Somewhere\r\n"+
			"DTSTART;TZID=Custom/Somewhere:20260924T100000\r\n"+
			"DTEND;TZID=Custom/Somewhere:20260924T110000\r\n"+
			"END:VEVENT\r\n"))

	if got := e.Start.UTC(); !got.Equal(time.Date(2026, 9, 24, 5, 0, 0, 0, time.UTC)) {
		t.Errorf("start resolved to %v, want 05:00Z from the inline definition", got)
	}
}

func TestParseRejectsAnEventWithATimezoneItCannotResolve(t *testing.T) {
	_, err := calendar.ParseObject(object(
		"BEGIN:VEVENT\r\nUID:u\r\nSUMMARY:Nowhere\r\n" +
			"DTSTART;TZID=Custom/Nowhere:20260924T100000\r\n" +
			"DTEND;TZID=Custom/Nowhere:20260924T110000\r\n" +
			"END:VEVENT\r\n"))

	if err == nil {
		t.Fatal("expected an error for a timezone with no definition anywhere")
	}
}

func TestParseRejectsAnEventWithNoStart(t *testing.T) {
	_, err := calendar.ParseObject(object(
		"BEGIN:VEVENT\r\nUID:u\r\nSUMMARY:When?\r\nEND:VEVENT\r\n"))

	if err == nil {
		t.Fatal("expected an error for an event with no start")
	}
}

func TestParseReportsAnObjectWithNoEvent(t *testing.T) {
	_, err := calendar.ParseObject("BEGIN:VCALENDAR\r\nVERSION:2.0\r\n" +
		"BEGIN:VTODO\r\nUID:t\r\nSUMMARY:Buy milk\r\nEND:VTODO\r\nEND:VCALENDAR\r\n")

	if err == nil {
		t.Fatal("expected an error for an object holding only a task")
	}
}

func TestParseFoldedLines(t *testing.T) {
	// Providers fold long lines at 75 octets and continue them with a space.
	e := parseOne(t, "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nBEGIN:VEVENT\r\nUID:u\r\n"+
		"SUMMARY:A title long enough that the provider had to fold it across tw\r\n o lines\r\n"+
		"DTSTART:20260924T090000Z\r\nDTEND:20260924T100000Z\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n")

	if e.Title != "A title long enough that the provider had to fold it across two lines" {
		t.Errorf("unfolded title: %q", e.Title)
	}
}
