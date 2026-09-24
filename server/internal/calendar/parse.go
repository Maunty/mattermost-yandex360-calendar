package calendar

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	// Embedded timezone data. The plugin resolves IANA identifiers that come
	// off the wire, and it must do so on a host that carries no tzdata of its
	// own, which is the normal case for a container image.
	_ "time/tzdata"

	"github.com/emersion/go-ical"
	"github.com/teambition/rrule-go"
)

// ErrNotAnEvent is returned for a calendar object that holds no VEVENT, such
// as a task or a free/busy report.
var ErrNotAnEvent = errors.New("calendar: object contains no event")

const (
	dateLayout     = "20060102"
	dateTimeLayout = "20060102T150405"
	utcLayout      = "20060102T150405Z"
)

// Conference links are not standardised across providers. CONFERENCE is the
// RFC 7986 property; the rest are what calendar services put there instead.
var conferenceProps = []string{
	ical.PropConference,
	"X-TELEMOST-CONFERENCE",
	"X-TELEMOST-URL",
	"X-YANDEX-CONFERENCE-URL",
	"X-GOOGLE-CONFERENCE",
}

// ParseObject reads one calendar object — the body of a single resource in a
// collection — and returns every Event in it. A recurring Event arrives as a
// master plus one Event per overridden occurrence, all sharing a UID, so one
// object routinely yields several Events.
func ParseObject(data string) ([]Event, error) {
	cal, err := ical.NewDecoder(strings.NewReader(data)).Decode()
	if err != nil {
		return nil, fmt.Errorf("calendar: unreadable object: %w", err)
	}

	zones := inlineZones(cal)

	var events []Event
	for _, child := range cal.Children {
		if child.Name != ical.CompEvent {
			continue
		}
		event, err := parseEvent(child, zones)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	if len(events) == 0 {
		return nil, ErrNotAnEvent
	}
	return events, nil
}

func parseEvent(comp *ical.Component, zones map[string]*time.Location) (Event, error) {
	var e Event

	e.UID = textOf(comp, ical.PropUID)
	if e.UID == "" {
		return e, errors.New("calendar: event has no UID")
	}
	e.Title = textOf(comp, ical.PropSummary)
	e.Location = textOf(comp, ical.PropLocation)
	e.Conference = conferenceOf(comp)
	e.Cancelled = strings.EqualFold(textOf(comp, ical.PropStatus), "CANCELLED")

	startProp := comp.Props.Get(ical.PropDateTimeStart)
	if startProp == nil {
		return e, fmt.Errorf("calendar: event %s has no start", e.UID)
	}
	start, isDate, err := parseTime(startProp, zones)
	if err != nil {
		return e, fmt.Errorf("calendar: event %s: %w", e.UID, err)
	}
	e.Start = start
	e.AllDay = isDate

	e.End, err = parseEnd(comp, e.Start, e.AllDay, zones)
	if err != nil {
		return e, fmt.Errorf("calendar: event %s: %w", e.UID, err)
	}

	if rule := comp.Props.Get(ical.PropRecurrenceRule); rule != nil {
		e.RRule = rule.Value
	}
	if e.RDates, err = parseTimeList(comp, ical.PropRecurrenceDates, zones); err != nil {
		return e, fmt.Errorf("calendar: event %s: %w", e.UID, err)
	}
	if e.ExDates, err = parseTimeList(comp, ical.PropExceptionDates, zones); err != nil {
		return e, fmt.Errorf("calendar: event %s: %w", e.UID, err)
	}

	if rid := comp.Props.Get(ical.PropRecurrenceID); rid != nil {
		recurrenceID, _, err := parseTime(rid, zones)
		if err != nil {
			return e, fmt.Errorf("calendar: event %s: %w", e.UID, err)
		}
		e.RecurrenceID = recurrenceID
	}

	return e, nil
}

func parseEnd(comp *ical.Component, start time.Time, allDay bool, zones map[string]*time.Location) (time.Time, error) {
	if endProp := comp.Props.Get(ical.PropDateTimeEnd); endProp != nil {
		end, _, err := parseTime(endProp, zones)
		return end, err
	}
	if durProp := comp.Props.Get(ical.PropDuration); durProp != nil {
		d, err := parseDuration(durProp.Value)
		if err != nil {
			return time.Time{}, err
		}
		return start.Add(d), nil
	}
	if allDay {
		// An all-day Event with no end covers exactly its one day.
		return start.AddDate(0, 0, 1), nil
	}
	return start, nil
}

func textOf(comp *ical.Component, name string) string {
	prop := comp.Props.Get(name)
	if prop == nil {
		return ""
	}
	// Text values arrive escaped: \, \; \n and \\ all carry meaning.
	text, err := prop.Text()
	if err != nil {
		return prop.Value
	}
	return text
}

func conferenceOf(comp *ical.Component) string {
	for _, name := range conferenceProps {
		if prop := comp.Props.Get(name); prop != nil && prop.Value != "" {
			return prop.Value
		}
	}
	// URL is a general-purpose property, so it counts as a conference link
	// only when it is one that can be followed.
	if prop := comp.Props.Get(ical.PropURL); prop != nil {
		if strings.HasPrefix(prop.Value, "https://") || strings.HasPrefix(prop.Value, "http://") {
			return prop.Value
		}
	}
	return ""
}

// parseTime resolves one date-time property to an absolute instant, keeping
// the Event's own timezone as the result's location because recurrence is
// expanded in local time. The second result reports a date with no time.
func parseTime(prop *ical.Prop, zones map[string]*time.Location) (time.Time, bool, error) {
	value := prop.Value
	isDate := prop.Params.Get("VALUE") == string(ical.ValueDate) || len(value) == len(dateLayout)

	loc, err := locationFor(prop, zones)
	if err != nil {
		return time.Time{}, false, err
	}

	switch {
	case isDate:
		t, err := time.ParseInLocation(dateLayout, value, loc)
		return t, true, err
	case strings.HasSuffix(value, "Z"):
		t, err := time.ParseInLocation(utcLayout, value, time.UTC)
		return t, false, err
	default:
		t, err := time.ParseInLocation(dateTimeLayout, value, loc)
		return t, false, err
	}
}

func parseTimeList(comp *ical.Component, name string, zones map[string]*time.Location) ([]time.Time, error) {
	var out []time.Time
	for _, prop := range comp.Props.Values(name) {
		// One property can carry several comma-separated values, and the
		// property itself can repeat.
		for _, value := range strings.Split(prop.Value, ",") {
			value = strings.TrimSpace(value)
			if value == "" {
				continue
			}
			single := prop
			single.Value = value
			t, _, err := parseTime(&single, zones)
			if err != nil {
				return nil, err
			}
			out = append(out, t)
		}
	}
	return out, nil
}

// locationFor resolves a property's TZID. Identifiers on the wire are IANA
// names, so the system database answers almost always; the inline VTIMEZONE
// definition is the fallback for anything it does not know.
func locationFor(prop *ical.Prop, zones map[string]*time.Location) (*time.Location, error) {
	tzid := prop.Params.Get(ical.ParamTimezoneID)
	if tzid == "" {
		return time.UTC, nil
	}
	if loc, err := time.LoadLocation(tzid); err == nil {
		return loc, nil
	}
	if loc, ok := zones[tzid]; ok {
		return loc, nil
	}
	return nil, fmt.Errorf("calendar: unknown timezone %q", tzid)
}

// inlineZones turns each VTIMEZONE in the object into a usable location. The
// result is an approximation: it carries the offset in force at the most
// recent transition rather than the whole history, which is enough to place an
// Event that names a timezone the system database has never heard of.
func inlineZones(cal *ical.Calendar) map[string]*time.Location {
	zones := map[string]*time.Location{}
	for _, child := range cal.Children {
		if child.Name != ical.CompTimezone {
			continue
		}
		tzid := textOf(child, ical.PropTimezoneID)
		if tzid == "" {
			continue
		}
		if loc, ok := latestOffset(child); ok {
			zones[tzid] = loc
		}
	}
	return zones
}

// latestOffset picks the STANDARD or DAYLIGHT rule whose most recent onset is
// closest to now, and returns its offset as a fixed zone.
func latestOffset(tz *ical.Component) (*time.Location, bool) {
	var (
		best     time.Time
		bestZone *time.Location
		now      = time.Now()
	)
	for _, rule := range tz.Children {
		if rule.Name != ical.CompTimezoneStandard && rule.Name != ical.CompTimezoneDaylight {
			continue
		}
		offset, err := parseUTCOffset(textOf(rule, ical.PropTimezoneOffsetTo))
		if err != nil {
			continue
		}
		name := textOf(rule, ical.PropTimezoneName)
		if name == "" {
			name = rule.Name
		}

		onset, ok := latestOnset(rule, now)
		if !ok {
			continue
		}
		if bestZone == nil || onset.After(best) {
			best, bestZone = onset, time.FixedZone(name, offset)
		}
	}
	return bestZone, bestZone != nil
}

// latestOnset is when this rule last came into force at or before limit.
func latestOnset(rule *ical.Component, limit time.Time) (time.Time, bool) {
	startProp := rule.Props.Get(ical.PropDateTimeStart)
	if startProp == nil {
		return time.Time{}, false
	}
	start, err := time.ParseInLocation(dateTimeLayout, startProp.Value, time.UTC)
	if err != nil {
		return time.Time{}, false
	}
	if start.After(limit) {
		return time.Time{}, false
	}

	onset := start
	if rrProp := rule.Props.Get(ical.PropRecurrenceRule); rrProp != nil {
		option, err := rrule.StrToROptionInLocation(rrProp.Value, time.UTC)
		if err == nil {
			option.Dtstart = start
			if r, err := rrule.NewRRule(*option); err == nil {
				if last := r.Before(limit, true); !last.IsZero() {
					onset = last
				}
			}
		}
	}
	// RDATE transitions, which is how a historical table is usually written.
	for _, prop := range rule.Props.Values(ical.PropRecurrenceDates) {
		for _, value := range strings.Split(prop.Value, ",") {
			t, err := time.ParseInLocation(dateTimeLayout, strings.TrimSpace(value), time.UTC)
			if err == nil && !t.After(limit) && t.After(onset) {
				onset = t
			}
		}
	}
	return onset, true
}

// parseUTCOffset reads the ±HHMM or ±HHMMSS form used by TZOFFSETTO.
func parseUTCOffset(value string) (int, error) {
	if len(value) != 5 && len(value) != 7 {
		return 0, fmt.Errorf("calendar: malformed UTC offset %q", value)
	}
	sign := 1
	switch value[0] {
	case '-':
		sign = -1
	case '+':
	default:
		return 0, fmt.Errorf("calendar: malformed UTC offset %q", value)
	}
	hours, err := strconv.Atoi(value[1:3])
	if err != nil {
		return 0, err
	}
	minutes, err := strconv.Atoi(value[3:5])
	if err != nil {
		return 0, err
	}
	seconds := 0
	if len(value) == 7 {
		if seconds, err = strconv.Atoi(value[5:7]); err != nil {
			return 0, err
		}
	}
	return sign * (hours*3600 + minutes*60 + seconds), nil
}

// parseDuration reads the RFC 5545 duration form, which is not Go's.
func parseDuration(value string) (time.Duration, error) {
	sign := time.Duration(1)
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "-") {
		sign, value = -1, value[1:]
	} else {
		value = strings.TrimPrefix(value, "+")
	}
	if !strings.HasPrefix(value, "P") {
		return 0, fmt.Errorf("calendar: malformed duration %q", value)
	}
	value = value[1:]

	var (
		total   time.Duration
		digits  string
		inTime  bool
		anyPart bool
	)
	units := map[byte]time.Duration{
		'W': 7 * 24 * time.Hour,
		'D': 24 * time.Hour,
		'H': time.Hour,
		'M': time.Minute,
		'S': time.Second,
	}
	for i := 0; i < len(value); i++ {
		c := value[i]
		switch {
		case c == 'T':
			inTime = true
		case c >= '0' && c <= '9':
			digits += string(c)
		default:
			unit, ok := units[c]
			if !ok || digits == "" {
				return 0, fmt.Errorf("calendar: malformed duration %q", value)
			}
			if c == 'M' && !inTime {
				return 0, fmt.Errorf("calendar: months are not a fixed duration in %q", value)
			}
			n, err := strconv.Atoi(digits)
			if err != nil {
				return 0, err
			}
			total += time.Duration(n) * unit
			digits, anyPart = "", true
		}
	}
	if !anyPart || digits != "" {
		return 0, fmt.Errorf("calendar: malformed duration %q", value)
	}
	return sign * total, nil
}
