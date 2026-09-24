package caldav_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/Maunty/mattermost-ya-calendar/server/internal/caldav"
	"github.com/Maunty/mattermost-ya-calendar/server/internal/fakecaldav"
)

func exportedMethods() []string {
	t := reflect.TypeOf(&caldav.Client{})
	names := make([]string, 0, t.NumMethod())
	for i := range t.NumMethod() {
		names = append(names, t.Method(i).Name)
	}
	return names
}

func TestEverythingTheClientDoesUsesAReadMethod(t *testing.T) {
	server := fakecaldav.New(t)
	collection := server.AddCalendar("events-1000001", "Мои события")
	collection.Put("m", event("m", "Standup", "20260924T070000Z", "20260924T073000Z"))
	client := clientFor(t, server)

	ctx := context.Background()
	home, err := client.FindCalendarHome(ctx)
	if err != nil {
		t.Fatalf("FindCalendarHome: %v", err)
	}
	collections, err := client.ListCalendars(ctx, home)
	if err != nil {
		t.Fatalf("ListCalendars: %v", err)
	}
	for _, c := range collections {
		if _, err := client.QueryEvents(ctx, c.Href,
			time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC),
			time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC)); err != nil {
			t.Fatalf("QueryEvents: %v", err)
		}
	}

	readOnly := map[string]bool{"OPTIONS": true, "PROPFIND": true, "REPORT": true}
	for _, request := range server.Requests() {
		if !readOnly[request.Method] {
			t.Errorf("the plugin sent %s %s; it must never do anything but read",
				request.Method, request.Path)
		}
	}
}
