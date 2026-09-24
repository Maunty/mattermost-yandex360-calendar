package main

import (
	"context"
	"hash/fnv"
	"time"

	"github.com/Maunty/mattermost-ya-calendar/server/internal/caldav"
	"github.com/Maunty/mattermost-ya-calendar/server/internal/calendar"
	"github.com/Maunty/mattermost-ya-calendar/server/internal/recurrence"
)

// How polling is paced.
const (
	// pollInterval is how often one person's calendars are read.
	pollInterval = 10 * time.Minute
	// pollJitter spreads people out. It is not about request cost: Events
	// cluster on the hour and half hour, so without it every person's
	// messages would come due in the same second.
	pollJitter = 4 * time.Minute
)

// How much of the future is kept ready.
const (
	// cacheHorizon is how far ahead occurrences are cached: enough for
	// tomorrow's Daily Summary and every Reminder that could come due before
	// the next poll.
	cacheHorizon = 48 * time.Hour
	// cacheFloor is how little of that may be left before the window is
	// extended. Keeping these apart is what lets a poll tick cost one request
	// when nothing has changed.
	cacheFloor = 24 * time.Hour
	// inProgressLookback keeps meetings that started before now in view, so
	// that a day's list includes the one that is running.
	inProgressLookback = 24 * time.Hour
)

// RunPoll is the scheduled read. It takes the current time as a value: there
// is no clock to mock, and a test drives the same code by passing the moment
// it wants.
func (p *Plugin) RunPoll(now time.Time) {
	if !p.getConfiguration().IsConfigured() {
		return
	}

	userIDs, err := p.store.ConnectedUserIDs()
	if err != nil {
		p.client.Log.Error("Could not list connected users", "error", err.Error())
		return
	}

	for _, userID := range userIDs {
		// One person's failure is theirs alone. Everyone else is still polled.
		if err := p.pollUser(context.Background(), userID, now); err != nil {
			p.client.Log.Warn("Could not poll a calendar", "user_id", userID, "error", err.Error())
		}
	}
}

func (p *Plugin) pollUser(ctx context.Context, userID string, now time.Time) error {
	connection, err := p.store.Connection(userID)
	if err != nil {
		if isNotConnected(err) {
			// The index has gone stale. Nothing to poll, and nothing to fix.
			return nil
		}
		return err
	}
	if !connection.Active {
		return nil
	}

	state, err := p.store.SyncState(userID)
	if err != nil {
		return err
	}
	if !state.NextPollAt.IsZero() && now.Before(state.NextPollAt) {
		return nil
	}

	pollErr := p.refresh(ctx, connection, state, now)

	// Whatever happened, this person is not polled again until their next due
	// time, so a failing Connection does not turn into a tight retry loop.
	state.NextPollAt = nextPollAt(userID, now)
	if pollErr != nil {
		if saveErr := p.store.SaveSyncState(userID, state); saveErr != nil {
			p.client.Log.Warn("Could not save sync state", "user_id", userID, "error", saveErr.Error())
		}
		p.recordFailure(connection, pollErr)
		return pollErr
	}

	state.LastSuccessAt = now
	if err := p.store.SaveSyncState(userID, state); err != nil {
		return err
	}
	p.recordSuccess(connection, now)
	return nil
}

// refresh brings one person's cached window up to date.
//
// The cost of a tick when nothing has changed is a single request: one
// depth-one read of the calendar home answers with every collection's change
// tag at once, and a collection whose tag has not moved is not queried.
func (p *Plugin) refresh(ctx context.Context, connection *Connection, state *SyncState, now time.Time) error {
	client, err := p.calendarClient(connection, now)
	if err != nil {
		return err
	}

	if connection.CalendarHome == "" {
		home, err := client.FindCalendarHome(ctx)
		if err != nil {
			return err
		}
		connection.CalendarHome = home
		if err := p.store.SaveConnection(connection); err != nil {
			return err
		}
	}

	collections, err := client.ListCalendars(ctx, connection.CalendarHome)
	if err != nil {
		return err
	}

	from := now.Add(-inProgressLookback)
	to := now.Add(cacheHorizon)
	// The window is extended, rather than merely refreshed, when it is
	// running out. That is the only reason to re-read a collection whose
	// contents have not changed.
	extending := state.CachedThrough.Before(now.Add(cacheFloor))

	ctags := make(map[string]string, len(collections))
	cached := make(map[string][]calendar.Occurrence, len(collections))

	for _, collection := range collections {
		ctags[collection.Href] = collection.CTag

		previous, hadCTag := previousCTag(state.CTags, collection.Href)
		unchanged := hadCTag && previous == collection.CTag && collection.CTag != ""
		if unchanged && !extending {
			cached[collection.Href] = previousOccurrences(state.Occurrences, collection.Href)
			continue
		}

		occurrences, err := p.readCollection(ctx, client, collection, from, to, connection.MattermostUserID)
		if err != nil {
			return err
		}
		cached[collection.Href] = occurrences
	}

	state.CTags = ctags
	state.Occurrences = flatten(cached)
	if extending {
		state.CachedThrough = to
	}
	return nil
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

// readCalendars reads a window directly, without touching the cache. This is
// what /yacal today runs: somebody asking what is on today wants the answer
// from their calendar, not from whenever the last poll happened.
func (p *Plugin) readCalendars(ctx context.Context, connection *Connection, from, to time.Time, now time.Time) ([]calendar.Occurrence, error) {
	client, err := p.calendarClient(connection, now)
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
	return sortOccurrences(all), nil
}

func previousCTag(ctags map[string]string, href string) (string, bool) {
	if ctag, ok := ctags[href]; ok {
		return ctag, true
	}
	// The same collection can come back under a different spelling of the
	// account's own name, so a miss is checked case-insensitively before it
	// is believed.
	for stored, ctag := range ctags {
		if caldav.SameHref(stored, href) {
			return ctag, true
		}
	}
	return "", false
}

// previousOccurrences is what the last poll cached for one collection.
func previousOccurrences(occurrences []calendar.Occurrence, href string) []calendar.Occurrence {
	var out []calendar.Occurrence
	for _, occurrence := range occurrences {
		if caldav.SameHref(occurrence.Source, href) {
			out = append(out, occurrence)
		}
	}
	return out
}

func flatten(cached map[string][]calendar.Occurrence) []calendar.Occurrence {
	var all []calendar.Occurrence
	for _, occurrences := range cached {
		all = append(all, occurrences...)
	}
	return sortOccurrences(all)
}

func sortOccurrences(occurrences []calendar.Occurrence) []calendar.Occurrence {
	// Sorting here rather than at every reader keeps the cache in the order a
	// person reads their day.
	for i := 1; i < len(occurrences); i++ {
		for j := i; j > 0 && occurrences[j].Start.Before(occurrences[j-1].Start); j-- {
			occurrences[j], occurrences[j-1] = occurrences[j-1], occurrences[j]
		}
	}
	return occurrences
}

// nextPollAt is when this person is read again: ten minutes from now, moved by
// up to four minutes either way. The offset is derived from the user id, so it
// is the same every time and people stay spread out rather than drifting back
// together.
func nextPollAt(userID string, now time.Time) time.Time {
	return now.Add(pollInterval + jitterFor(userID))
}

func jitterFor(userID string) time.Duration {
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(userID))

	// The spread is worked out in seconds. Nanoseconds would make the span
	// far larger than the hash, so people would land within a few seconds of
	// each other and stay bunched exactly where the jitter is meant to help.
	spanSeconds := int64(2*pollJitter/time.Second) + 1
	offset := time.Duration(int64(hash.Sum32())%spanSeconds) * time.Second
	return offset - pollJitter
}
