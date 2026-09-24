package main

import (
	"strings"
	"testing"
	"time"

	"github.com/Maunty/mattermost-ya-calendar/server/internal/calendar"
)

// Rendering, escaping and encryption: the pieces that decide what a person
// actually sees, and what somebody with the database does not.

func occurrence(title string, start, end time.Time) calendar.Occurrence {
	return calendar.Occurrence{UID: "u", Title: title, Start: start, End: end, RecurrenceID: start}
}

func TestTheSameEventReadsCorrectlyToPeopleInDifferentTimezones(t *testing.T) {
	// One meeting, three readers. Nobody does arithmetic.
	meeting := occurrence("All-hands", moment(2026, 9, 24, 14, 0), moment(2026, 9, 24, 15, 0))

	for _, reader := range []struct {
		zone  string
		start string
		abbr  string
	}{
		{"Europe/Moscow", "14:00", "MSK"},
		{"Europe/Berlin", "13:00", "CEST"},
		{"Asia/Tokyo", "20:00", "JST"},
	} {
		loc := mustLoadZone(reader.zone)
		shown := timeRange(meeting, loc)
		if !strings.Contains(shown, reader.start) {
			t.Errorf("%s reader was shown %q, want it to start at %s", reader.zone, shown, reader.start)
		}
		if !strings.Contains(shown, reader.abbr) {
			t.Errorf("%s reader was not told which timezone they are reading: %q", reader.zone, shown)
		}
	}
}

func TestAMeetingThatRunsPastMidnightNamesBothDays(t *testing.T) {
	overnight := occurrence("Deployment window", moment(2026, 9, 24, 22, 0), moment(2026, 9, 25, 3, 0))

	shown := timeRange(overnight, testZone)

	if !strings.Contains(shown, "Thu") || !strings.Contains(shown, "Fri") {
		t.Errorf("a meeting running past midnight does not say which days: %q", shown)
	}
}

func TestAnAllDayOccurrenceIsNotGivenATimeOfDay(t *testing.T) {
	holiday := calendar.Occurrence{
		UID: "h", Title: "Public holiday", AllDay: true,
		Start: moment(2026, 9, 24, 0, 0), End: moment(2026, 9, 25, 0, 0),
	}

	if shown := timeRange(holiday, testZone); shown != "All day" {
		t.Errorf("an all-day occurrence rendered as %q", shown)
	}
}

func TestAnUntitledEventIsStillReadable(t *testing.T) {
	post := reminderPost(occurrence("", moment(2026, 9, 24, 10, 0), moment(2026, 9, 24, 11, 0)), testZone, 10*time.Minute)

	if !strings.Contains(text(post), "untitled") {
		t.Errorf("an event with no title rendered as nothing:\n%s", text(post))
	}
}

func TestSanitisingProviderText(t *testing.T) {
	for _, c := range []struct {
		name  string
		in    string
		want  string
		avoid string
	}{
		{name: "a link is defused", in: "[click](http://evil.invalid)", want: `\[click\](http://evil.invalid)`},
		{name: "emphasis is defused", in: "*urgent*", want: `\*urgent\*`},
		{name: "a table break is defused", in: "a | b", want: `a \| b`},
		{name: "HTML is defused", in: "<img src=x>", want: `\<img src=x\>`},
		{name: "newlines become spaces", in: "one\ntwo", want: "one two"},
		{name: "an ordinary title is left alone", in: "Budget review", want: "Budget review"},
		{name: "an email address is left alone", in: "sam@yandex.ru", want: "sam@yandex.ru"},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := sanitise(c.in)
			if c.want != "" && got != c.want {
				t.Errorf("sanitise(%q) = %q, want %q", c.in, got, c.want)
			}
			if c.avoid != "" && strings.Contains(got, c.avoid) {
				t.Errorf("sanitise(%q) = %q, which still contains %q", c.in, got, c.avoid)
			}
		})
	}
}

func TestAnAbsurdlyLongTitleIsCutDown(t *testing.T) {
	got := sanitise(strings.Repeat("very long ", 200))

	if len([]rune(got)) > maxRenderedText+1 {
		t.Errorf("a title of %d characters was let through", len([]rune(got)))
	}
}

func TestOnlyWebLinksAreOffered(t *testing.T) {
	for _, link := range []string{
		"javascript:alert(1)", "data:text/html,<script>", "file:///etc/passwd", "not a url at all", "",
	} {
		if got := safeLink(link); got != "" {
			t.Errorf("safeLink(%q) offered %q", link, got)
		}
	}
	for _, link := range []string{"https://telemost.yandex.ru/j/1", "http://meet.example.com/x"} {
		if safeLink(link) != link {
			t.Errorf("safeLink(%q) dropped a perfectly good link", link)
		}
	}
}

func TestLeadTimesAreWrittenTheWayAPersonWouldSayThem(t *testing.T) {
	for _, c := range []struct {
		lead time.Duration
		want string
	}{
		{10 * time.Minute, "10 minutes"},
		{time.Minute, "a minute"},
		{30 * time.Second, "a minute"},
		{time.Hour, "an hour"},
		{2 * time.Hour, "2 hours"},
		{90 * time.Minute, "1h30"},
	} {
		if got := humaniseLead(c.lead); got != c.want {
			t.Errorf("humaniseLead(%v) = %q, want %q", c.lead, got, c.want)
		}
	}
}

func TestEncryptionRoundTrips(t *testing.T) {
	sealed, err := encrypt("a key", []byte("a refresh token"))
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if strings.Contains(sealed, "refresh token") {
		t.Fatalf("the plaintext is visible in %q", sealed)
	}

	opened, err := decrypt("a key", sealed)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if string(opened) != "a refresh token" {
		t.Errorf("round trip gave %q", opened)
	}
}

func TestTheSameValueEncryptsDifferentlyEachTime(t *testing.T) {
	first, _ := encrypt("a key", []byte("same"))
	second, _ := encrypt("a key", []byte("same"))

	if first == second {
		t.Error("two encryptions of the same value are identical, so the nonce is not doing its job")
	}
}

func TestADifferentKeyCannotOpenIt(t *testing.T) {
	sealed, _ := encrypt("the original key", []byte("a refresh token"))

	if _, err := decrypt("a regenerated key", sealed); err == nil {
		t.Error("a value encrypted with one key was opened with another")
	}
}

func TestWithoutAKeyNothingIsStored(t *testing.T) {
	if _, err := encrypt("", []byte("a refresh token")); err == nil {
		t.Error("a token was encrypted with no key at all")
	}
}
