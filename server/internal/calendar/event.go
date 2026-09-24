// Package calendar holds the plugin's view of a calendar: the Event as it is
// stored at the provider, and the Occurrence as a person experiences it.
package calendar

import "time"

// Event is one VEVENT exactly as a calendar collection stores it. A recurring
// Event is stored once, as a master carrying its rules; the occurrences a
// person sees are derived from it. An Event that overrides a single occurrence
// of a master is stored as a separate Event sharing the master's UID and
// carrying the RecurrenceID of the occurrence it replaces.
type Event struct {
	UID        string
	Title      string
	Location   string
	Conference string

	// Start and End are absolute instants, already resolved from the Event's
	// own timezone. Start keeps that timezone as its location, because
	// recurrence is expanded in the Event's local time, not in UTC.
	Start time.Time
	End   time.Time

	// AllDay marks an Event with a date but no time of day.
	AllDay bool

	// Cancelled marks STATUS:CANCELLED. On a master it removes the whole
	// series; on an override it removes that one occurrence.
	Cancelled bool

	// RRule is the raw RRULE value, empty for a non-recurring Event.
	RRule   string
	RDates  []time.Time
	ExDates []time.Time

	// RecurrenceID is zero unless this Event overrides one occurrence of a
	// master, in which case it is the original start of that occurrence.
	RecurrenceID time.Time

	// Source records which collection the Event came from, for diagnostics.
	Source string
}

// IsOverride reports whether this Event replaces a single occurrence of a
// recurring master rather than standing on its own.
func (e Event) IsOverride() bool { return !e.RecurrenceID.IsZero() }

// IsRecurring reports whether this Event generates more than one occurrence.
func (e Event) IsRecurring() bool { return e.RRule != "" || len(e.RDates) > 0 }

// Duration is how long the Event runs. An Event with no end runs for no time.
func (e Event) Duration() time.Duration {
	if e.End.IsZero() || e.End.Before(e.Start) {
		return 0
	}
	return e.End.Sub(e.Start)
}

// Occurrence is one concrete instance of an Event at one point on the
// timeline. Reminders and Daily Summaries are built from Occurrences, never
// from Events, because a recurring Event has no single start.
type Occurrence struct {
	UID        string
	Title      string
	Location   string
	Conference string
	Start      time.Time
	End        time.Time
	AllDay     bool

	// Source records which collection the Occurrence came from, so that a
	// poll can replace one collection's cached occurrences without disturbing
	// another's.
	Source string

	// RecurrenceID identifies which occurrence of the UID this is, so that a
	// Reminder can be recorded as sent for this instance and not for the
	// series. It is the Occurrence's original start, which for a moved
	// occurrence differs from Start.
	RecurrenceID time.Time
}

// InstanceKey identifies one occurrence uniquely and stably across polls, so
// that a Reminder sent for it is never sent twice. A moved occurrence keeps
// its key, because RecurrenceID does not move with it.
func (o Occurrence) InstanceKey() string {
	return o.UID + "@" + o.RecurrenceID.UTC().Format(time.RFC3339)
}
