package main

import (
	"context"
	"time"

	"github.com/maunty/mattermost-yandex360-calendar/server/internal/caldav"
	"github.com/maunty/mattermost-yandex360-calendar/server/internal/calendar"
	"github.com/maunty/mattermost-yandex360-calendar/server/internal/recurrence"
)

// readFromCalDAV is the fallback event source (ADR 0002). It is not wired in:
// Events are read from the REST API. It is kept, and tested, so that switching
// back is one assignment to Plugin.readEvents and a release. Switching back
// also needs the write-capable CalDAV scope, which means every Connection has
// to be consented again.
//
// Every read discovers the collections and queries each of them: the change
// tags that once let an unchanged tick cost one request went with the move to
// REST, which has nothing like them.
func (p *Plugin) readFromCalDAV(ctx context.Context, connection *Connection, from, to, now time.Time) ([]calendar.Occurrence, error) {
	client, err := caldav.New(p.caldavURL, p.httpClient, p.authorizer(connection, now))
	if err != nil {
		return nil, err
	}

	if connection.CalendarHome == "" {
		home, err := client.FindCalendarHome(ctx)
		if err != nil {
			return nil, err
		}
		connection.CalendarHome = home
		if err := p.store.SaveConnection(connection); err != nil {
			return nil, err
		}
	}

	collections, err := client.ListCalendars(ctx, connection.CalendarHome)
	if err != nil {
		return nil, err
	}

	var all []calendar.Occurrence
	for _, collection := range collections {
		occurrences, err := p.readCollection(ctx, client, collection, from, to, connection.MattermostUserID)
		if err != nil {
			return nil, err
		}
		all = append(all, occurrences...)
	}
	return all, nil
}

// readCollection queries one collection and expands what comes back. A single
// unreadable Event is logged and skipped: it must not cost this person their
// other Reminders, let alone anybody else's.
func (p *Plugin) readCollection(
	ctx context.Context,
	client *caldav.Client,
	collection caldav.Collection,
	from, to time.Time,
	userID string,
) ([]calendar.Occurrence, error) {
	objects, err := client.QueryEvents(ctx, collection.Href, from, to)
	if err != nil {
		return nil, err
	}

	var events []calendar.Event
	for _, object := range objects {
		parsed, err := calendar.ParseObject(object.Data)
		if err != nil {
			p.client.Log.Warn("Skipped an unreadable calendar entry",
				"user_id", userID, "href", object.Href, "error", err.Error())
			continue
		}
		for _, event := range parsed {
			event.Source = collection.Href
			events = append(events, event)
		}
	}

	occurrences, problems := recurrence.Expand(events, from, to)
	for _, problem := range problems {
		p.client.Log.Warn("Skipped an event whose recurrence could not be expanded",
			"user_id", userID, "error", problem.Error())
	}
	for i := range occurrences {
		occurrences[i].Source = collection.Href
	}
	return occurrences, nil
}
