package caldav_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Maunty/mattermost-ya-calendar/server/internal/caldav"
	"github.com/Maunty/mattermost-ya-calendar/server/internal/fakecaldav"
)

func clientFor(t *testing.T, server *fakecaldav.Server) *caldav.Client {
	t.Helper()
	c, err := caldav.New(server.URL, server.Client(), func(context.Context) (string, error) {
		return "OAuth " + server.Token, nil
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func event(uid, summary, start, end string) string {
	return "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nBEGIN:VEVENT\r\nUID:" + uid +
		"\r\nSUMMARY:" + summary + "\r\nDTSTART:" + start + "\r\nDTEND:" + end +
		"\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
}

func TestDiscoveryFollowsThePrincipalToTheCalendarHome(t *testing.T) {
	server := fakecaldav.New(t)
	client := clientFor(t, server)

	home, err := client.FindCalendarHome(context.Background())
	if err != nil {
		t.Fatalf("FindCalendarHome: %v", err)
	}
	if home != server.HomeHref() {
		t.Errorf("home: got %q, want %q", home, server.HomeHref())
	}
}

func TestOnlyCalendarsHoldingEventsAreKept(t *testing.T) {
	server := fakecaldav.New(t)
	// A real account carries an events collection with a numeric suffix, a
	// task list, and the two scheduling collections.
	server.AddCalendar("events-1000001", "Мои события")
	server.AddTaskList("todos-1000002", "Не забыть")

	client := clientFor(t, server)
	collections, err := client.ListCalendars(context.Background(), server.HomeHref())
	if err != nil {
		t.Fatalf("ListCalendars: %v", err)
	}

	if len(collections) != 1 {
		t.Fatalf("got %d collections, want only the one holding events: %+v", len(collections), collections)
	}
	if !strings.Contains(collections[0].Href, "events-1000001") {
		t.Errorf("kept the wrong collection: %q", collections[0].Href)
	}
	if collections[0].DisplayName != "Мои события" {
		t.Errorf("display name: %q", collections[0].DisplayName)
	}
	if collections[0].CTag == "" {
		t.Error("the collection came back with no change tag, so a poll could never skip it")
	}
}

func TestTheCalendarHomeIsNotListedAsOneOfItsOwnCollections(t *testing.T) {
	server := fakecaldav.New(t)
	server.AddCalendar("events-1000001", "Мои события")

	client := clientFor(t, server)
	collections, _ := client.ListCalendars(context.Background(), server.HomeHref())

	for _, c := range collections {
		if caldav.SameHref(c.Href, server.HomeHref()) {
			t.Errorf("the home %q was listed as a collection", c.Href)
		}
	}
}

func TestCollectionHrefsAreUsedAsReturnedRatherThanRebuilt(t *testing.T) {
	// The fake spells the account SamTester@yandex.ru, which is not how the
	// person types it. A client that rebuilt the path from a login would ask
	// for the wrong one and get nothing back.
	server := fakecaldav.New(t)
	collection := server.AddCalendar("events-1000001", "Мои события")
	collection.Put("meeting", event("m1", "Standup", "20260924T070000Z", "20260924T073000Z"))

	client := clientFor(t, server)
	home, err := client.FindCalendarHome(context.Background())
	if err != nil {
		t.Fatalf("FindCalendarHome: %v", err)
	}
	collections, err := client.ListCalendars(context.Background(), home)
	if err != nil {
		t.Fatalf("ListCalendars: %v", err)
	}
	if len(collections) != 1 {
		t.Fatalf("got %d collections", len(collections))
	}

	objects, err := client.QueryEvents(context.Background(), collections[0].Href,
		time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC), time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("QueryEvents: %v", err)
	}
	if len(objects) != 1 {
		t.Fatalf("got %d objects, want 1", len(objects))
	}
	if !strings.Contains(objects[0].Data, "Standup") {
		t.Errorf("object data: %q", objects[0].Data)
	}
}

func TestHrefComparisonIgnoresLetterCasing(t *testing.T) {
	// The same account answers with different casing depending on whether the
	// request authenticated with a password or a token.
	if !caldav.SameHref("/calendars/samtester%40yandex.ru/", "/calendars/SamTester%40yandex.ru") {
		t.Error("hrefs differing only in case and a trailing slash were treated as different")
	}
	if caldav.SameHref("/calendars/a/", "/calendars/b/") {
		t.Error("different hrefs were treated as the same")
	}
}

func TestQueryIsAlwaysTimeRanged(t *testing.T) {
	server := fakecaldav.New(t)
	collection := server.AddCalendar("events-1000001", "Мои события")
	collection.Put("old", event("old", "Ancient history", "20170101T090000Z", "20170101T100000Z"))
	collection.Put("today", event("today", "Standup", "20260924T070000Z", "20260924T073000Z"))

	client := clientFor(t, server)
	server.Reset()
	objects, err := client.QueryEvents(context.Background(), collection.Href(),
		time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC), time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("QueryEvents: %v", err)
	}

	if len(objects) != 1 {
		t.Fatalf("got %d objects, want only the one in the window", len(objects))
	}
	requests := server.Requests()
	if len(requests) != 1 {
		t.Fatalf("got %d requests, want 1", len(requests))
	}
	if !strings.Contains(requests[0].Body, "time-range") {
		t.Errorf("the query carried no time range, so it would return years of history:\n%s", requests[0].Body)
	}
}

func TestListingAllCollectionsCostsOneRequest(t *testing.T) {
	server := fakecaldav.New(t)
	server.AddCalendar("events-1000001", "Мои события")
	server.AddCalendar("events-9000001", "Работа")
	server.AddTaskList("todos-1000002", "Не забыть")

	client := clientFor(t, server)
	server.Reset()
	collections, err := client.ListCalendars(context.Background(), server.HomeHref())
	if err != nil {
		t.Fatalf("ListCalendars: %v", err)
	}

	if len(collections) != 2 {
		t.Fatalf("got %d collections, want 2", len(collections))
	}
	for _, c := range collections {
		if c.CTag == "" {
			t.Errorf("collection %q came back with no change tag", c.Href)
		}
	}
	if got := len(server.Requests()); got != 1 {
		t.Errorf("reading every collection's change tag cost %d requests, want 1", got)
	}
}

func TestARejectedTokenIsReportedAsAnAuthenticationFailure(t *testing.T) {
	server := fakecaldav.New(t)
	client, err := caldav.New(server.URL, server.Client(), func(context.Context) (string, error) {
		return "OAuth the-wrong-token", nil
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	_, err = client.FindCalendarHome(context.Background())
	if err == nil {
		t.Fatal("expected an error")
	}
	if !caldav.IsAuthFailure(err) {
		t.Errorf("a 401 was not recognised as an authentication failure: %v", err)
	}
	if caldav.IsTransient(err) {
		t.Errorf("a 401 was treated as worth retrying: %v", err)
	}
}

func TestAServerErrorIsReportedAsTransient(t *testing.T) {
	server := fakecaldav.New(t)
	server.Fail(http.StatusServiceUnavailable, 0)
	client := clientFor(t, server)

	_, err := client.FindCalendarHome(context.Background())
	if err == nil {
		t.Fatal("expected an error")
	}
	if caldav.IsAuthFailure(err) {
		t.Errorf("a 503 was mistaken for a revoked Connection: %v", err)
	}
	if !caldav.IsTransient(err) {
		t.Errorf("a 503 was not treated as worth retrying: %v", err)
	}
}

func TestAnUnreachableProviderIsTransientNotFatal(t *testing.T) {
	server := fakecaldav.New(t)
	client := clientFor(t, server)
	server.Close() // the provider goes away mid-flight

	_, err := client.FindCalendarHome(context.Background())
	if err == nil {
		t.Fatal("expected an error")
	}
	if caldav.IsAuthFailure(err) {
		t.Errorf("an unreachable provider was mistaken for a revoked Connection: %v", err)
	}
	if !caldav.IsTransient(err) {
		t.Errorf("an unreachable provider was not treated as worth retrying: %v", err)
	}
}

func TestClientExposesNoWayToWrite(t *testing.T) {
	// Adding a method that changes anything at the provider must be a
	// deliberate change that breaks this test, not an accident.
	allowed := map[string]bool{
		"FindCalendarHome": true,
		"ListCalendars":    true,
		"QueryEvents":      true,
	}

	for _, name := range exportedMethods() {
		if !allowed[name] {
			t.Errorf("Client grew a method %q. This plugin only reads: if the new method "+
				"writes to a calendar it must not exist, and if it reads, add it to this list.", name)
		}
	}
}
