// Package fakecalendarapi is a stand-in for Yandex's REST calendar API, for
// tests.
//
// Tests describe calendars the way they always have, as iCalendar objects in
// named calendars, and the fake answers the way the real API does: every
// occurrence of a series as its own item, moved and cancelled occurrences
// already applied, declined Events left out unless asked for, and the whole
// list paged. The expansion it does to get there is the plugin's own
// recurrence package, standing in for the provider's; the plugin under test
// never expands anything itself.
package fakecalendarapi

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/Maunty/mattermost-ya-calendar/server/internal/calendar"
	"github.com/Maunty/mattermost-ya-calendar/server/internal/recurrence"
)

// EventsPath is the route the fake serves.
const EventsPath = "/v1/calendar/events"

// maxLimit is the largest page the real API accepts.
const maxLimit = 100

// Server is a running fake. New arranges for it to be closed.
type Server struct {
	*httptest.Server

	// Token is the bearer the fake accepts. Anything else gets a 401, which is
	// what a revoked Connection looks like.
	Token string
	// PageSize caps how many items one page holds, below whatever the client
	// asked for, so that a test can make paging happen with a handful of
	// Events.
	PageSize int

	mu         sync.Mutex
	calendars  []*Calendar
	requests   []Request
	failStatus int
	failCount  int
}

// Request is one call the fake received.
type Request struct {
	Method string
	Path   string
	Query  string
}

// Calendar is one of the person's calendars. The API itself has no notion of
// which calendar an Event is on; the fake keeps them apart only so that tests
// can describe an account the way a person would.
type Calendar struct {
	relation string
	tasks    bool

	mu       sync.Mutex
	objects  map[string]string
	declined map[string]bool
	watched  map[string]bool
	raw      map[string]map[string]any
}

// New starts a fake serving one account with no calendars yet.
func New(t *testing.T) *Server {
	t.Helper()
	s := &Server{Token: "test-access-token"}
	s.Server = httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.Close)
	return s
}

// AddCalendar adds a calendar the person owns.
func (s *Server) AddCalendar(name, displayName string) *Calendar {
	return s.add(&Calendar{relation: "ORGANIZER"})
}

// AddTaskList adds the list of to-dos every account has. The API never returns
// tasks as Events.
func (s *Server) AddTaskList(name, displayName string) *Calendar {
	return s.add(&Calendar{relation: "ORGANIZER", tasks: true})
}

func (s *Server) add(c *Calendar) *Calendar {
	c.objects = map[string]string{}
	c.declined = map[string]bool{}
	c.watched = map[string]bool{}
	c.raw = map[string]map[string]any{}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calendars = append(s.calendars, c)
	return c
}

// Put stores an Event, as creating or changing one at the provider does.
func (c *Calendar) Put(name, icalData string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.objects[name] = icalData
	delete(c.declined, name)
	delete(c.watched, name)
}

// PutWatched stores someone else's Event that the person added to this
// calendar without being invited: a Watched Event.
func (c *Calendar) PutWatched(name, icalData string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.objects[name] = icalData
	delete(c.declined, name)
	c.watched[name] = true
}

// PutDeclined stores an Event the person was invited to and declined.
func (c *Calendar) PutDeclined(name, icalData string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.objects[name] = icalData
	c.declined[name] = true
}

// PutItem stores an item exactly as the API would return it, for an Event
// the provider holds but describes in a way the plugin cannot read. It is
// returned for every window.
func (c *Calendar) PutItem(name string, item map[string]any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.raw[name] = item
}

// Remove deletes an Event, as deleting it at the provider does.
func (c *Calendar) Remove(name string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.objects, name)
	delete(c.declined, name)
	delete(c.watched, name)
	delete(c.raw, name)
}

// Fail makes the next n requests answer with status. A count of zero fails
// every request until Recover.
func (s *Server) Fail(status, n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failStatus, s.failCount = status, n
}

// Recover stops failing.
func (s *Server) Recover() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failStatus, s.failCount = 0, 0
}

// Requests returns every call received since the last Reset.
func (s *Server) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Request, len(s.requests))
	copy(out, s.requests)
	return out
}

// Reset forgets the recorded requests.
func (s *Server) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests = nil
}

func (s *Server) nextFailure() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failStatus == 0 {
		return 0
	}
	if s.failCount == 0 {
		return s.failStatus
	}
	s.failCount--
	status := s.failStatus
	if s.failCount == 0 {
		s.failStatus = 0
	}
	return status
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.requests = append(s.requests, Request{Method: r.Method, Path: r.URL.Path, Query: r.URL.RawQuery})
	token := s.Token
	s.mu.Unlock()

	if status := s.nextFailure(); status != 0 {
		writeError(w, status, "injected_failure")
		return
	}
	if r.Header.Get("Authorization") != "OAuth "+token {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if r.URL.Path != EventsPath {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}

	query := r.URL.Query()
	from, err := time.Parse(time.RFC3339, query.Get("from"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_from")
		return
	}
	to := time.Now().AddDate(100, 0, 0)
	if raw := query.Get("to"); raw != "" {
		if to, err = time.Parse(time.RFC3339, raw); err != nil || !to.After(from) {
			writeError(w, http.StatusBadRequest, "bad_to")
			return
		}
	}
	limit := 10
	if raw := query.Get("limit"); raw != "" {
		if limit, err = strconv.Atoi(raw); err != nil || limit < 1 || limit > maxLimit {
			writeError(w, http.StatusBadRequest, "bad_limit")
			return
		}
	}
	s.mu.Lock()
	if s.PageSize > 0 && s.PageSize < limit {
		limit = s.PageSize
	}
	s.mu.Unlock()
	offset := 0
	if raw := query.Get("iteration_key"); raw != "" {
		if offset, err = strconv.Atoi(raw); err != nil || offset < 0 {
			writeError(w, http.StatusBadRequest, "bad_iteration_key")
			return
		}
	}

	items := s.items(from, to, query.Get("show_declined_events") == "true")
	page := map[string]any{"limit": limit}
	end := min(offset+limit, len(items))
	if offset > len(items) {
		offset = end
	}
	page["items"] = items[offset:end]
	if end < len(items) {
		page["iteration_key"] = strconv.Itoa(end)
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(page)
}

// items is every occurrence in [from, to) across every calendar, in the order
// the API returns them.
func (s *Server) items(from, to time.Time, showDeclined bool) []map[string]any {
	s.mu.Lock()
	calendars := append([]*Calendar(nil), s.calendars...)
	s.mu.Unlock()

	type found struct {
		occurrence calendar.Occurrence
		relation   string
		recurring  bool
	}
	var (
		all []found
		raw []map[string]any
	)
	for _, c := range calendars {
		if c.tasks {
			continue
		}
		c.mu.Lock()
		for _, item := range c.raw {
			raw = append(raw, item)
		}
		for name, data := range c.objects {
			if c.declined[name] && !showDeclined {
				continue
			}
			events, err := calendar.ParseObject(data)
			if err != nil {
				continue
			}
			recurring := false
			for _, e := range events {
				recurring = recurring || e.IsRecurring() || e.IsOverride()
			}
			relation := c.relation
			if c.watched[name] {
				relation = "SUBSCRIBER"
			}
			occurrences, _ := recurrence.Expand(events, from, to)
			for _, o := range occurrences {
				all = append(all, found{o, relation, recurring})
			}
		}
		c.mu.Unlock()
	}

	sort.SliceStable(all, func(i, j int) bool {
		a, b := all[i].occurrence, all[j].occurrence
		if a.Start.Equal(b.Start) {
			return a.InstanceKey() < b.InstanceKey()
		}
		return a.Start.Before(b.Start)
	})

	items := append(make([]map[string]any, 0, len(raw)+len(all)), raw...)
	for _, f := range all {
		o := f.occurrence
		item := map[string]any{
			"ical_uid":      o.UID,
			"event_id":      eventID(o.UID),
			"start":         eventTime(o.Start, o.AllDay),
			"end":           eventTime(o.End, o.AllDay),
			"summary":       o.Title,
			"relation_type": f.relation,
			"rules":         map[string]any{"visibility": "DEFAULT", "participant_can_invite": true, "participant_can_edit": false},
			"created_at":    "2026-09-01T00:00:00Z",
			"updated_at":    "2026-09-01T00:00:00Z",
		}
		if o.Location != "" {
			item["location"] = o.Location
		}
		if f.recurring {
			if o.AllDay {
				item["recurrence_id"] = o.RecurrenceID.Format("2006-01-02")
			} else {
				// UTC with no offset written, as the real API sends it.
				item["recurrence_id"] = o.RecurrenceID.UTC().Format("2006-01-02T15:04:05")
			}
		}
		items = append(items, item)
	}
	return items
}

func eventTime(t time.Time, allDay bool) map[string]string {
	if allDay {
		return map[string]string{"date": t.Format("2006-01-02")}
	}
	return map[string]string{"date_time": t.Format("2006-01-02T15:04:05"), "time_zone": t.Location().String()}
}

// eventID is a stable, UUID-shaped id for a UID.
func eventID(uid string) string {
	h := fnv.New128a()
	_, _ = h.Write([]byte(uid))
	b := h.Sum(nil)
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func writeError(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": code, "message": http.StatusText(status)})
}
