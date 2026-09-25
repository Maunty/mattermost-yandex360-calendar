package calendarapi_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/Maunty/mattermost-ya-calendar/server/internal/calendarapi"
	"github.com/Maunty/mattermost-ya-calendar/server/internal/fakecalendarapi"
)

var (
	from = time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC)
	to   = time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC)
)

const standup = "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nBEGIN:VEVENT\r\nUID:standup\r\nSUMMARY:Standup\r\n" +
	"DTSTART:20260924T070000Z\r\nDTEND:20260924T073000Z\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"

func clientFor(t *testing.T, server *fakecalendarapi.Server) *calendarapi.Client {
	t.Helper()
	client, err := calendarapi.New(server.URL, server.Client(), func(context.Context) (string, error) {
		return "OAuth " + server.Token, nil
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return client
}

func TestEverythingTheClientDoesIsARead(t *testing.T) {
	server := fakecalendarapi.New(t)
	server.PageSize = 1
	calendar := server.AddCalendar("events", "events")
	calendar.Put("a", standup)
	calendar.Put("b",
		"BEGIN:VCALENDAR\r\nVERSION:2.0\r\nBEGIN:VEVENT\r\nUID:b\r\nSUMMARY:Review\r\n"+
			"DTSTART:20260924T090000Z\r\nDTEND:20260924T100000Z\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n")

	occurrences, _, err := clientFor(t, server).Occurrences(context.Background(), from, to)
	if err != nil {
		t.Fatalf("Occurrences: %v", err)
	}
	if len(occurrences) != 2 {
		t.Fatalf("got %d occurrences across two pages, want 2", len(occurrences))
	}
	for _, request := range server.Requests() {
		if request.Method != http.MethodGet || request.Path != fakecalendarapi.EventsPath {
			t.Errorf("the client sent %s %s; it must never do anything but read Events",
				request.Method, request.Path)
		}
	}
}

func TestARefusedTokenIsAnAuthFailureAndAnOutageIsNot(t *testing.T) {
	for status, auth := range map[int]bool{
		http.StatusUnauthorized:       true,
		http.StatusForbidden:          true,
		http.StatusTooManyRequests:    false,
		http.StatusServiceUnavailable: false,
	} {
		server := fakecalendarapi.New(t)
		server.Fail(status, 1)

		_, _, err := clientFor(t, server).Occurrences(context.Background(), from, to)

		if calendarapi.IsAuthFailure(err) != auth {
			t.Errorf("%d: IsAuthFailure = %v, want %v", status, !auth, auth)
		}
		if calendarapi.IsTransient(err) == auth {
			t.Errorf("%d: IsTransient = %v, want %v", status, auth, !auth)
		}
	}
}
