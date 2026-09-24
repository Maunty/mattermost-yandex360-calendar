package main

import (
	"fmt"
	"strings"
	"time"
)

// The calendar bodies these tests put on the fake server. They are the shape
// a real account returns: a full inline VTIMEZONE, CRLF line endings, and an
// events collection whose name carries a numeric suffix.

var testZone = mustLoadZone("Europe/Moscow")

func mustLoadZone(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		panic(err)
	}
	return loc
}

// moment builds a time in the person's timezone, which these tests treat as
// Europe/Moscow unless they say otherwise.
func moment(year int, month time.Month, day, hour, minute int) time.Time {
	return time.Date(year, month, day, hour, minute, 0, 0, testZone)
}

const vtimezoneMoscow = "BEGIN:VTIMEZONE\r\n" +
	"TZID:Europe/Moscow\r\n" +
	"BEGIN:STANDARD\r\n" +
	"DTSTART:19700101T000000\r\n" +
	"TZOFFSETFROM:+0300\r\n" +
	"TZOFFSETTO:+0300\r\n" +
	"TZNAME:MSK\r\n" +
	"END:STANDARD\r\n" +
	"END:VTIMEZONE\r\n"

func wrapCalendar(body string) string {
	return "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//Yandex LLC//Yandex Calendar//EN\r\n" +
		"CALSCALE:GREGORIAN\r\n" + vtimezoneMoscow + body + "END:VCALENDAR\r\n"
}

func localStamp(t time.Time) string {
	return t.In(testZone).Format("20060102T150405")
}

// timedEvent is an ordinary meeting with a start and an end.
func timedEvent(uid, summary string, start, end time.Time, extra ...string) string {
	return wrapCalendar(vevent(uid, summary, start, end, extra...))
}

func vevent(uid, summary string, start, end time.Time, extra ...string) string {
	var b strings.Builder
	b.WriteString("BEGIN:VEVENT\r\n")
	b.WriteString("UID:" + uid + "\r\n")
	b.WriteString("SUMMARY:" + summary + "\r\n")
	b.WriteString("DTSTAMP:20260901T000000Z\r\n")
	b.WriteString(fmt.Sprintf("DTSTART;TZID=Europe/Moscow:%s\r\n", localStamp(start)))
	b.WriteString(fmt.Sprintf("DTEND;TZID=Europe/Moscow:%s\r\n", localStamp(end)))
	for _, line := range extra {
		b.WriteString(line + "\r\n")
	}
	b.WriteString("END:VEVENT\r\n")
	return b.String()
}

// allDayEvent has a date and no time of day, which is what makes it produce no
// Reminder while still appearing in a Daily Summary.
func allDayEvent(uid, summary string, day time.Time) string {
	local := day.In(testZone)
	return wrapCalendar("BEGIN:VEVENT\r\n" +
		"UID:" + uid + "\r\n" +
		"SUMMARY:" + summary + "\r\n" +
		"DTSTAMP:20260901T000000Z\r\n" +
		"DTSTART;VALUE=DATE:" + local.Format("20060102") + "\r\n" +
		"DTEND;VALUE=DATE:" + local.AddDate(0, 0, 1).Format("20060102") + "\r\n" +
		"END:VEVENT\r\n")
}

// recurringEvent is a standing meeting: one stored resource that stands for
// every one of its occurrences, because the provider will not expand it.
func recurringEvent(uid, summary string, start, end time.Time, rule string, extra ...string) string {
	return timedEvent(uid, summary, start, end, append([]string{"RRULE:" + rule}, extra...)...)
}

// seriesWithOverride is a standing meeting plus one occurrence changed on its
// own: moved, renamed, or called off.
func seriesWithOverride(uid, summary string, start, end time.Time, rule string, override string) string {
	return wrapCalendar(vevent(uid, summary, start, end, "RRULE:"+rule) + override)
}

// movedOccurrence replaces one occurrence of a series with a new time.
func movedOccurrence(uid, summary string, originalStart, newStart, newEnd time.Time) string {
	return "BEGIN:VEVENT\r\n" +
		"UID:" + uid + "\r\n" +
		"SUMMARY:" + summary + "\r\n" +
		"DTSTAMP:20260901T000000Z\r\n" +
		fmt.Sprintf("RECURRENCE-ID;TZID=Europe/Moscow:%s\r\n", localStamp(originalStart)) +
		fmt.Sprintf("DTSTART;TZID=Europe/Moscow:%s\r\n", localStamp(newStart)) +
		fmt.Sprintf("DTEND;TZID=Europe/Moscow:%s\r\n", localStamp(newEnd)) +
		"END:VEVENT\r\n"
}

// cancelledOccurrence calls off one occurrence of a series.
func cancelledOccurrence(uid, summary string, originalStart time.Time) string {
	return "BEGIN:VEVENT\r\n" +
		"UID:" + uid + "\r\n" +
		"SUMMARY:" + summary + "\r\n" +
		"DTSTAMP:20260901T000000Z\r\n" +
		"STATUS:CANCELLED\r\n" +
		fmt.Sprintf("RECURRENCE-ID;TZID=Europe/Moscow:%s\r\n", localStamp(originalStart)) +
		fmt.Sprintf("DTSTART;TZID=Europe/Moscow:%s\r\n", localStamp(originalStart)) +
		fmt.Sprintf("DTEND;TZID=Europe/Moscow:%s\r\n", localStamp(originalStart.Add(time.Hour))) +
		"END:VEVENT\r\n"
}

// task is what lives in the to-do collection. A meeting it is not.
func task(uid, summary string) string {
	return "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nBEGIN:VTODO\r\n" +
		"UID:" + uid + "\r\nSUMMARY:" + summary + "\r\nEND:VTODO\r\nEND:VCALENDAR\r\n"
}
