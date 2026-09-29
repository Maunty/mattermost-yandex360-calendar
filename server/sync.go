package main

import (
	"context"
	"hash/fnv"
	"time"

	"github.com/maunty/mattermost-yandex360-calendar/server/internal/calendar"
	"github.com/maunty/mattermost-yandex360-calendar/server/internal/calendarapi"
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
	// inProgressLookback keeps Events that started before now in view, so
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
	// The exception is Yandex asking this person's reads to slow down: they
	// are read again as soon as it allows, which without a Retry-After is the
	// next run.
	state.NextPollAt = nextPollAt(userID, now)
	if wait, limited := calendarapi.RateLimited(pollErr); limited {
		state.NextPollAt = now.Add(wait)
	}
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
	p.recordSuccess(connection)
	return nil
}

// eventSource reads the occurrences of a person's Events during [from, to).
// It is the seam between the plugin and the provider: the REST API sits behind
// it, and CalDAV is kept behind it as a fallback (ADR 0002).
type eventSource func(ctx context.Context, connection *Connection, from, to, now time.Time) ([]calendar.Occurrence, error)

// events is the source in use: the REST API unless something else was set.
func (p *Plugin) events() eventSource {
	if p.readEvents != nil {
		return p.readEvents
	}
	return p.readFromAPI
}

// refresh replaces one person's cached window with a fresh read of it. There
// is no change token to skip an unchanged calendar with, so every poll reads
// the whole window.
func (p *Plugin) refresh(ctx context.Context, connection *Connection, state *SyncState, now time.Time) error {
	to := now.Add(cacheHorizon)
	occurrences, err := p.events()(ctx, connection, now.Add(-inProgressLookback), to, now)
	if err != nil {
		return err
	}
	state.Occurrences = sortOccurrences(occurrences)
	state.CachedThrough = to
	return nil
}

// readCalendars reads a window directly, without touching the cache. This is
// what /yacal today runs: somebody asking what is on today wants the answer
// from their calendar, not from whenever the last poll happened.
func (p *Plugin) readCalendars(ctx context.Context, connection *Connection, from, to time.Time, now time.Time) ([]calendar.Occurrence, error) {
	occurrences, err := p.events()(ctx, connection, from, to, now)
	if err != nil {
		return nil, err
	}
	return sortOccurrences(occurrences), nil
}

// readFromAPI reads from the REST API, which has already expanded recurring
// Events and applied every moved and cancelled occurrence. A single unreadable
// Event is logged and skipped: it must not cost this person their other
// Reminders.
func (p *Plugin) readFromAPI(ctx context.Context, connection *Connection, from, to, now time.Time) ([]calendar.Occurrence, error) {
	client, err := calendarapi.New(p.calendarAPIURL, p.httpClient, p.authorizer(connection, now))
	if err != nil {
		return nil, err
	}
	occurrences, problems, err := client.Occurrences(ctx, from, to)
	if err != nil {
		return nil, err
	}
	for _, problem := range problems {
		p.client.Log.Warn("Skipped an unreadable calendar entry",
			"user_id", connection.MattermostUserID, "error", problem.Error())
	}
	return occurrences, nil
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
