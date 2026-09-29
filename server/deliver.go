package main

import (
	"time"

	"github.com/maunty/mattermost-yandex360-calendar/server/internal/calendar"
)

// sentRecordFloor is the shortest time a "this was already sent" record is
// kept. It outlives any plausible restart, which is what stops a restart from
// sending the same Reminder twice.
const sentRecordFloor = 48 * time.Hour

// RunDelivery decides which messages have come due and sends them. Like the
// poll, it takes the current time as a value.
//
// Reminders fire from the cached occurrences at exact times rather than from
// the poll clock, so the lead time is accurate however long ago the last poll
// happened.
func (p *Plugin) RunDelivery(now time.Time) {
	if !p.getConfiguration().IsConfigured() {
		return
	}

	userIDs, err := p.store.ConnectedUserIDs()
	if err != nil {
		p.client.Log.Error("Could not list connected users", "error", err.Error())
		return
	}

	for _, userID := range userIDs {
		if err := p.deliverTo(userID, now); err != nil {
			p.client.Log.Warn("Could not deliver messages", "user_id", userID, "error", err.Error())
		}
	}
}

func (p *Plugin) deliverTo(userID string, now time.Time) error {
	connection, err := p.store.Connection(userID)
	if err != nil {
		if isNotConnected(err) {
			return nil
		}
		return err
	}
	// An inactive Connection sends nothing. The person has already been told
	// once, and has a reconnect link.
	if !connection.Active {
		return nil
	}

	settings, err := p.store.Settings(userID)
	if err != nil {
		return err
	}
	state, err := p.store.SyncState(userID)
	if err != nil {
		return err
	}
	loc := p.userLocation(userID)

	if settings.RemindersEnabled() {
		lead, _ := p.getConfiguration().LeadTime(settings)
		p.sendDueReminders(userID, lead, state, loc, now)
	}
	if settings.DailySummaryEnabled() {
		p.sendDailySummary(userID, settings, state, loc, now)
	}
	return nil
}

// sendDueReminders sends one message per occurrence whose Lead Time has
// arrived and which has not started yet.
//
// Delivery wakes once a tick, so a Reminder is due one tick early: the run
// that sends it is the last one with at least the Lead Time still to go, and
// nobody is given less warning than they asked for.
func (p *Plugin) sendDueReminders(userID string, lead time.Duration, state *SyncState, loc *time.Location, now time.Time) {
	for _, occurrence := range state.Occurrences {
		// An all-day Event has no start time to be early for.
		if occurrence.AllDay {
			continue
		}
		// An Event that has already started produces no Reminder, which is
		// also what keeps a restart from reminding somebody about the past.
		if !occurrence.Start.After(now) {
			continue
		}
		if occurrence.Start.Sub(now) > lead+tickInterval {
			continue
		}

		// Recording first is deliberate. Two nodes reaching the same
		// occurrence in the same tick produce one message, and the one that
		// loses the race does not also send.
		key := occurrence.InstanceKey()
		ttl := occurrence.Start.Sub(now) + sentRecordFloor
		first, err := p.store.MarkReminderSent(userID, key, ttl)
		if err != nil {
			p.client.Log.Error("Could not record a reminder", "user_id", userID, "error", err.Error())
			continue
		}
		if !first {
			continue
		}

		// The message says how long there actually is, not how long there was
		// meant to be: an Event added minutes before it starts is still worth
		// a Reminder, and lying about the time would not help anybody.
		remaining := occurrence.Start.Sub(now)
		if err := p.dm(userID, reminderPost(occurrence, loc, remaining)); err != nil {
			p.client.Log.Error("Could not send a reminder", "user_id", userID, "error", err.Error())
		}
	}
}

// sendDailySummary sends one message a day, at the time the person chose, in
// the timezone they read.
func (p *Plugin) sendDailySummary(userID string, settings Settings, state *SyncState, loc *time.Location, now time.Time) {
	local := now.In(loc)
	hour, minute := settings.SummaryTime()
	due := time.Date(local.Year(), local.Month(), local.Day(), hour, minute, 0, 0, loc)
	if local.Before(due) {
		return
	}

	dayStart := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
	dayEnd := dayStart.AddDate(0, 0, 1)

	// Keyed by the person's own local day, so exactly one goes out per day
	// however many times this runs.
	first, err := p.store.MarkSummarySent(userID, dayStart, sentRecordFloor)
	if err != nil {
		p.client.Log.Error("Could not record a daily summary", "user_id", userID, "error", err.Error())
		return
	}
	if !first {
		return
	}

	if err := p.dm(userID, dailySummaryPost(dayStart, occurrencesOn(state.Occurrences, dayStart, dayEnd), loc)); err != nil {
		p.client.Log.Error("Could not send a daily summary", "user_id", userID, "error", err.Error())
	}
}

// occurrencesOn is the part of the cached window that falls on one day, in
// start order.
func occurrencesOn(occurrences []calendar.Occurrence, from, to time.Time) []calendar.Occurrence {
	var out []calendar.Occurrence
	for _, occurrence := range occurrences {
		if occurrence.Start.Before(to) && occurrence.End.After(from) {
			out = append(out, occurrence)
			continue
		}
		// An occurrence with no length belongs to the day it starts on.
		if !occurrence.End.After(occurrence.Start) &&
			!occurrence.Start.Before(from) && occurrence.Start.Before(to) {
			out = append(out, occurrence)
		}
	}
	return sortOccurrences(out)
}
