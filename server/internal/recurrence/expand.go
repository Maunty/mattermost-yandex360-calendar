// Package recurrence expands stored calendar Events into the concrete
// Occurrences a person experiences.
//
// Yandex accepts a request to expand recurrence server-side and silently
// ignores it, returning masters with their rules intact, so this work cannot
// be pushed onto the provider. Expansion here is a pure function: it performs
// no I/O, touches no storage, and depends on nothing else in the plugin.
package recurrence

import (
	"fmt"
	"sort"
	"time"

	"github.com/teambition/rrule-go"

	"github.com/Maunty/mattermost-ya-calendar/server/internal/calendar"
)

// Problem records one Event that could not be expanded. Expansion continues
// past it: one unreadable rule must not cost a person every other Reminder.
type Problem struct {
	UID    string
	Source string
	Err    error
}

func (p Problem) Error() string {
	return fmt.Sprintf("event %s from %s: %v", p.UID, p.Source, p.Err)
}

// Expand turns Events into the Occurrences that fall inside [from, to). An
// Occurrence is inside the window when it is happening during it, so an Event
// that began before the window opened and is still running belongs in it.
//
// Events are expanded in their own timezone, which is what keeps a 10:00
// Event at 10:00 on both sides of a daylight-saving transition.
func Expand(events []calendar.Event, from, to time.Time) ([]calendar.Occurrence, []Problem) {
	var (
		masters   []calendar.Event
		overrides = map[string][]calendar.Event{}
		cancelled = map[string]bool{}
	)
	for _, e := range events {
		switch {
		case e.IsOverride():
			overrides[e.UID] = append(overrides[e.UID], e)
		default:
			masters = append(masters, e)
			if e.Cancelled {
				cancelled[e.UID] = true
			}
		}
	}

	var (
		occurrences []calendar.Occurrence
		problems    []Problem
	)

	// Overrides are applied first, because an overridden occurrence replaces
	// whatever the master's rule would have generated for it. Its start may be
	// anywhere, including outside the window the rule covers.
	replaced := map[string]bool{}
	for uid, list := range overrides {
		for _, o := range list {
			replaced[instanceOf(uid, o.RecurrenceID)] = true
			if o.Cancelled || cancelled[uid] {
				continue
			}
			if overlaps(o.Start, o.End, from, to) {
				occurrences = append(occurrences, occurrenceOf(o, o.Start, o.End, o.RecurrenceID))
			}
		}
	}

	for _, m := range masters {
		if m.Cancelled {
			continue
		}
		if !m.IsRecurring() {
			if !replaced[instanceOf(m.UID, m.Start)] && overlaps(m.Start, m.End, from, to) {
				occurrences = append(occurrences, occurrenceOf(m, m.Start, m.End, m.Start))
			}
			continue
		}

		set, err := ruleSet(m)
		if err != nil {
			problems = append(problems, Problem{UID: m.UID, Source: m.Source, Err: err})
			continue
		}

		duration := m.Duration()
		// Widen the query backwards by the Event's own length, so that an
		// occurrence which started before the window and is still running is
		// generated and can then be judged by overlaps.
		for _, start := range set.Between(from.Add(-duration), to, true) {
			if replaced[instanceOf(m.UID, start)] {
				continue
			}
			end := start.Add(duration)
			if !overlaps(start, end, from, to) {
				continue
			}
			occurrences = append(occurrences, occurrenceOf(m, start, end, start))
		}
	}

	sort.SliceStable(occurrences, func(i, j int) bool {
		if occurrences[i].Start.Equal(occurrences[j].Start) {
			return occurrences[i].Title < occurrences[j].Title
		}
		return occurrences[i].Start.Before(occurrences[j].Start)
	})
	return occurrences, problems
}

// ruleSet assembles the Event's rule, extra dates and excluded dates into one
// generator, anchored at the Event's own start in the Event's own timezone.
func ruleSet(e calendar.Event) (*rrule.Set, error) {
	set := &rrule.Set{}
	set.DTStart(e.Start)

	if e.RRule != "" {
		option, err := rrule.StrToROptionInLocation(e.RRule, e.Start.Location())
		if err != nil {
			return nil, fmt.Errorf("unreadable recurrence rule %q: %w", e.RRule, err)
		}
		option.Dtstart = e.Start
		rule, err := rrule.NewRRule(*option)
		if err != nil {
			return nil, fmt.Errorf("unusable recurrence rule %q: %w", e.RRule, err)
		}
		set.RRule(rule)
	}
	for _, d := range e.RDates {
		set.RDate(d)
	}
	for _, d := range e.ExDates {
		set.ExDate(d)
	}
	return set, nil
}

func occurrenceOf(e calendar.Event, start, end time.Time, recurrenceID time.Time) calendar.Occurrence {
	return calendar.Occurrence{
		UID:          e.UID,
		Title:        e.Title,
		Location:     e.Location,
		Conference:   e.Conference,
		Start:        start,
		End:          end,
		AllDay:       e.AllDay,
		RecurrenceID: recurrenceID,
	}
}

// instanceOf names one occurrence of one Event by the instant it was
// originally scheduled for. Providers spell the same instant with whichever
// timezone they please, so the name is built from the instant, not the text.
func instanceOf(uid string, recurrenceID time.Time) string {
	return uid + "@" + recurrenceID.UTC().Format(time.RFC3339)
}

// overlaps reports whether an occurrence running from start to end is
// happening at any point during [from, to).
func overlaps(start, end, from, to time.Time) bool {
	if !start.Before(to) {
		return false
	}
	if end.After(start) {
		return end.After(from)
	}
	// An occurrence with no length is in the window when its start is.
	return !start.Before(from)
}
