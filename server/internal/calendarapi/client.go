// Package calendarapi reads a person's Events from Yandex's REST calendar API,
// and deliberately does nothing else.
//
// The API expands recurring Events on the server: every occurrence of a series
// comes back as its own item, moved and cancelled occurrences already applied.
// So unlike the CalDAV path, nothing here knows what a recurrence rule is.
//
// The Client has no method that creates, changes or deletes anything. The
// consent it runs under is read-only, and the code says the same thing.
package calendarapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"time"

	"github.com/maunty/mattermost-yandex360-calendar/server/internal/calendar"
)

// eventsPath is the one route this package reads.
const eventsPath = "/v1/calendar/events"

// pageSize is the largest page the API serves. A day rarely fills one.
const pageSize = 100

// maxPages bounds how far paging is followed. A window of a few days that runs
// to thousands of Events is a fault, and following it forever would stall the
// poll for everybody behind this person.
const maxPages = 50

// requestsPerSecond keeps one person's reads under Yandex's limit of 5
// requests a second for each person. It only bites when a read runs to
// several pages.
const requestsPerSecond = 4

// maxResponseBytes caps a single page.
const maxResponseBytes = 8 << 20

// Doer is the slice of http.Client this package uses.
type Doer interface {
	Do(req *http.Request) (*http.Response, error)
}

// Authorizer supplies the Authorization header value for each request, so that
// a token refreshed between pages is picked up without rebuilding the Client.
type Authorizer func(ctx context.Context) (string, error)

// Client reads one person's Events.
type Client struct {
	base       *url.URL
	doer       Doer
	authorizer Authorizer
}

// New builds a Client against the API's base URL.
func New(baseURL string, doer Doer, authorizer Authorizer) (*Client, error) {
	base, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("calendarapi: bad endpoint %q: %w", baseURL, err)
	}
	if doer == nil {
		doer = &http.Client{Timeout: 30 * time.Second}
	}
	return &Client{base: base, doer: NewThrottle(doer, requestsPerSecond), authorizer: authorizer}, nil
}

// Occurrences returns the person's Events happening during [from, to), one per
// occurrence. The second result lists items that were skipped because they
// could not be read: one malformed Event must not cost a person every other
// Reminder.
//
// Only the person's own Events come back: those they organise, are invited
// to, or watch. Declined Events are left out by the API itself, and an Event
// the person has no part in is dropped here.
func (c *Client) Occurrences(ctx context.Context, from, to time.Time) ([]calendar.Occurrence, []error, error) {
	query := url.Values{}
	query.Set("from", from.UTC().Format(time.RFC3339))
	query.Set("to", to.UTC().Format(time.RFC3339))
	query.Set("limit", strconv.Itoa(pageSize))

	var (
		occurrences []calendar.Occurrence
		problems    []error
	)
	for range maxPages {
		page, err := c.page(ctx, query)
		if err != nil {
			return nil, nil, err
		}
		for _, item := range page.Items {
			if !takesPart(item.RelationType) {
				continue
			}
			occurrence, err := item.occurrence()
			if err != nil {
				problems = append(problems, fmt.Errorf("event %s: %w", item.EventID, err))
				continue
			}
			occurrences = append(occurrences, occurrence)
		}
		if page.IterationKey == "" {
			return occurrences, problems, nil
		}
		query.Set("iteration_key", page.IterationKey)
	}
	return nil, nil, fmt.Errorf("calendarapi: more than %d pages of Events between %s and %s",
		maxPages, from.Format(time.RFC3339), to.Format(time.RFC3339))
}

// takesPart reports whether the Event is one of the person's. SUBSCRIBER is
// not a Subscribed Calendar: probing showed it on the person's own calendar,
// for someone else's Event they had added without being invited, which is a
// Watched Event. Anything else, including a relation this code has never
// seen, is left out.
func takesPart(relation string) bool {
	switch relation {
	case "ORGANIZER", "ATTENDEE", "OPTIONAL_ATTENDEE", "SUBSCRIBER":
		return true
	}
	return false
}

func (c *Client) page(ctx context.Context, query url.Values) (*eventsPage, error) {
	target := c.base.ResolveReference(&url.URL{Path: eventsPath, RawQuery: query.Encode()}).String()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, fmt.Errorf("calendarapi: building a request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if c.authorizer != nil {
		authorization, err := c.authorizer(ctx)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", authorization)
	}

	resp, err := c.doer.Do(req)
	if err != nil {
		// The provider being unreachable is never a reason to treat a
		// Connection as dead.
		return nil, &TransportError{Err: err}
	}
	defer resp.Body.Close()

	payload, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, &TransportError{Err: err}
	}
	if resp.StatusCode != http.StatusOK {
		// A Retry-After that is not a whole number of seconds is ignored:
		// the next run is only a minute away anyway.
		seconds, _ := strconv.Atoi(resp.Header.Get("Retry-After"))
		return nil, &StatusError{StatusCode: resp.StatusCode, RetryAfter: time.Duration(max(seconds, 0)) * time.Second}
	}

	var page eventsPage
	if err := json.Unmarshal(payload, &page); err != nil {
		return nil, fmt.Errorf("calendarapi: unreadable list of Events: %w", err)
	}
	return &page, nil
}

type eventsPage struct {
	IterationKey string  `json:"iteration_key"`
	Items        []event `json:"items"`
}

type event struct {
	ICalUID      string    `json:"ical_uid"`
	EventID      string    `json:"event_id"`
	RecurrenceID string    `json:"recurrence_id"`
	Start        eventTime `json:"start"`
	End          eventTime `json:"end"`
	Summary      string    `json:"summary"`
	Location     string    `json:"location"`
	Description  string    `json:"description"`
	RelationType string    `json:"relation_type"`
}

// conferenceLabel finds the call link Yandex writes into an Event's
// description. There is no conference field; probing found the link only
// here, at the start of a line and after one of these labels. Only a labelled
// link counts, so an ordinary link in someone's notes is never offered as the
// call.
var conferenceLabel = regexp.MustCompile(
	`(?m)^[ \t]*(?:Ссылка на видеовстречу|Ссылка на звонок|Link to video conference|Call link):[ \t]*(\S+)`)

// conferenceOf returns the labelled call link in a description, or nothing.
// The rest of the description is never kept: it may hold private notes.
func conferenceOf(description string) string {
	if match := conferenceLabel.FindStringSubmatch(description); match != nil {
		return match[1]
	}
	return ""
}

// eventTime is either a local date and time with its timezone, or a bare date
// for an all-day Event.
type eventTime struct {
	DateTime string `json:"date_time"`
	TimeZone string `json:"time_zone"`
	Date     string `json:"date"`
}

const (
	dateLayout     = "2006-01-02"
	dateTimeLayout = "2006-01-02T15:04:05"
)

func (t eventTime) parse() (time.Time, bool, error) {
	if t.Date != "" {
		// All-day dates carry no timezone, the same way CalDAV's do.
		d, err := time.ParseInLocation(dateLayout, t.Date, time.UTC)
		return d, true, err
	}
	loc, err := time.LoadLocation(t.TimeZone)
	if err != nil {
		return time.Time{}, false, fmt.Errorf("unknown timezone %q", t.TimeZone)
	}
	d, err := time.ParseInLocation(dateTimeLayout, t.DateTime, loc)
	return d, false, err
}

// occurrence turns one item into the plugin's view of it.
//
// The identity is the iCalendar UID plus the occurrence's original start: the
// same key the CalDAV path builds, so switching between the two never sends a
// Reminder twice.
func (e event) occurrence() (calendar.Occurrence, error) {
	if e.ICalUID == "" {
		return calendar.Occurrence{}, errors.New("no UID")
	}
	start, allDay, err := e.Start.parse()
	if err != nil {
		return calendar.Occurrence{}, fmt.Errorf("unreadable start: %w", err)
	}
	end, _, err := e.End.parse()
	if err != nil {
		return calendar.Occurrence{}, fmt.Errorf("unreadable end: %w", err)
	}

	recurrenceID := start
	if e.RecurrenceID != "" {
		recurrenceID, err = parseRecurrenceID(e.RecurrenceID)
		if err != nil {
			return calendar.Occurrence{}, fmt.Errorf("unreadable recurrence id: %w", err)
		}
	}

	return calendar.Occurrence{
		UID:          e.ICalUID,
		Title:        e.Summary,
		Location:     e.Location,
		Conference:   conferenceOf(e.Description),
		Start:        start,
		End:          end,
		AllDay:       allDay,
		RecurrenceID: recurrenceID,
	}, nil
}

// parseRecurrenceID reads the original start of an occurrence. It has no
// offset written on it, but it is UTC: probing showed it stays the same
// whatever timezone the response is asked for, while start moves with it.
func parseRecurrenceID(value string) (time.Time, error) {
	if len(value) == len(dateLayout) {
		return time.ParseInLocation(dateLayout, value, time.UTC)
	}
	return time.ParseInLocation(dateTimeLayout, value, time.UTC)
}
