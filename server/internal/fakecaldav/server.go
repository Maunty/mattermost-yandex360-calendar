// Package fakecaldav is a stand-in CalDAV server for tests.
//
// It is the only substitution point below the Mattermost plugin API: every
// layer above the wire — discovery, collection filtering, change-tag
// comparison, query construction, iCalendar parsing, recurrence expansion and
// rendering — runs for real against it. The response shapes are the ones a
// real account returned during probing, quirks included: the principal comes
// back with different letter casing than the login, the calendar collection
// carries a numeric suffix rather than the documented default name, and the
// home itself answers 404 for the properties it does not hold.
package fakecaldav

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Maunty/mattermost-ya-calendar/server/internal/calendar"
	"github.com/Maunty/mattermost-ya-calendar/server/internal/recurrence"
)

// Server is a running fake. Close it with t.Cleanup, which New arranges.
type Server struct {
	*httptest.Server

	// Login is the account as the server spells it back, which is not how the
	// person typed it.
	Login string
	// Token is the bearer the fake accepts. Anything else gets a 401, which is
	// what a revoked Connection looks like.
	Token string

	mu          sync.Mutex
	collections []*Collection
	requests    []Request
	failStatus  int
	failCount   int
}

// Request is one call the fake received, for tests that care what a poll cost.
type Request struct {
	Method string
	Path   string
	Body   string
}

// Collection is one collection in the calendar home.
type Collection struct {
	server       *Server
	name         string
	displayName  string
	resourceType string
	components   []string

	mu      sync.Mutex
	ctag    int64
	objects map[string]string
}

// New starts a fake serving one account with no collections yet.
func New(t *testing.T) *Server {
	t.Helper()
	s := &Server{Login: "SamTester@yandex.ru", Token: "test-access-token"}
	s.Server = httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.Close)
	return s
}

// AddCalendar adds a collection holding events, named the way a real account
// names one: a numeric suffix, not the name in the documentation.
func (s *Server) AddCalendar(name, displayName string) *Collection {
	return s.add(name, displayName, "calendar", []string{"VEVENT"})
}

// AddTaskList adds the collection of to-dos every account has. Reading it for
// Events is a mistake the plugin must not make.
func (s *Server) AddTaskList(name, displayName string) *Collection {
	return s.add(name, displayName, "calendar", []string{"VTODO"})
}

func (s *Server) add(name, displayName, resourceType string, components []string) *Collection {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := &Collection{
		server:       s,
		name:         name,
		displayName:  displayName,
		resourceType: resourceType,
		components:   components,
		ctag:         time.Now().UnixMilli(),
		objects:      map[string]string{},
	}
	s.collections = append(s.collections, c)
	return c
}

// Put stores an object, as creating or changing an Event at the provider does.
// It moves the collection's change tag, which is the signal a poll looks for.
func (c *Collection) Put(name, icalData string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.objects[name] = icalData
	c.bump()
}

// Remove deletes an object, as deleting an Event at the provider does.
func (c *Collection) Remove(name string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.objects, name)
	c.bump()
}

// bump must be called with the lock held.
func (c *Collection) bump() {
	next := time.Now().UnixMilli()
	if next <= c.ctag {
		next = c.ctag + 1
	}
	c.ctag = next
}

// Href is the collection's path, as the server reports it.
func (c *Collection) Href() string {
	return "/calendars/" + c.server.escapedLogin() + "/" + c.name + "/"
}

// HomeHref is where this account's collections live.
func (s *Server) HomeHref() string {
	return "/calendars/" + s.escapedLogin() + "/"
}

// PrincipalHref is the account's principal, spelled as the server spells it.
func (s *Server) PrincipalHref() string {
	return "/principals/users/" + s.escapedLogin() + "/"
}

// escapedLogin spells the login the way the server does on the wire, with the
// at sign percent-encoded.
func (s *Server) escapedLogin() string {
	return strings.ReplaceAll(s.Login, "@", "%40")
}

// Fail makes the next n requests answer with status. A 401 is a revoked
// Connection; a 503 is a provider having a bad afternoon. The plugin must tell
// them apart. A count of zero fails every request until Recover.
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

// Reset forgets the recorded requests, so a test can count one poll tick.
func (s *Server) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests = nil
}

func (s *Server) record(r *http.Request, body string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests = append(s.requests, Request{Method: r.Method, Path: r.URL.EscapedPath(), Body: body})
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
	body, _ := io.ReadAll(r.Body)
	s.record(r, string(body))

	if status := s.nextFailure(); status != 0 {
		http.Error(w, http.StatusText(status), status)
		return
	}
	if r.Header.Get("Authorization") != "OAuth "+s.Token {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	path := r.URL.EscapedPath()
	switch {
	case r.Method == http.MethodOptions:
		w.Header().Set("DAV", "1,calendar-access,calendar-auto-schedule")
		w.Header().Set("Allow", "OPTIONS, GET, HEAD, PROPFIND, REPORT")
		w.WriteHeader(http.StatusOK)

	case r.Method == "PROPFIND" && (path == "/" || path == ""):
		writeMultistatus(w, principalResponse(s.PrincipalHref()))

	case r.Method == "PROPFIND" && equalPath(path, s.PrincipalHref()):
		writeMultistatus(w, homeSetResponse(s.PrincipalHref(), s.HomeHref()))

	case r.Method == "PROPFIND" && equalPath(path, s.HomeHref()):
		writeMultistatus(w, s.homeListing())

	case r.Method == "REPORT":
		collection := s.collectionAt(path)
		if collection == nil {
			http.NotFound(w, r)
			return
		}
		writeMultistatus(w, collection.queryResponse(string(body)))

	default:
		http.NotFound(w, r)
	}
}

func (s *Server) collectionAt(path string) *Collection {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.collections {
		if equalPath(path, c.Href()) {
			return c
		}
	}
	return nil
}

// homeListing answers a depth-one read of the home: the home itself, the two
// scheduling collections every account has, and the calendars.
func (s *Server) homeListing() string {
	s.mu.Lock()
	collections := append([]*Collection(nil), s.collections...)
	s.mu.Unlock()

	var b strings.Builder
	// The home is a plain collection and holds no change tag. It answers the
	// properties it does not have with its own 404 inside the 207.
	b.WriteString(fmt.Sprintf(`
 <d:response>
  <d:href>%s</d:href>
  <d:propstat>
   <d:prop><d:resourcetype><d:collection/></d:resourcetype><d:displayname>%s</d:displayname></d:prop>
   <d:status>HTTP/1.1 200 OK</d:status>
  </d:propstat>
  <d:propstat>
   <d:prop><cs:getctag/><c:supported-calendar-component-set/></d:prop>
   <d:status>HTTP/1.1 404 Not Found</d:status>
  </d:propstat>
 </d:response>`, s.HomeHref(), s.Login))

	for _, name := range []string{"inbox", "outbox"} {
		resourceType := "schedule-" + name
		b.WriteString(fmt.Sprintf(`
 <d:response>
  <d:href>%s%s/</d:href>
  <d:propstat>
   <d:prop><d:resourcetype><d:collection/><c:%s/></d:resourcetype></d:prop>
   <d:status>HTTP/1.1 200 OK</d:status>
  </d:propstat>
 </d:response>`, s.HomeHref(), name, resourceType))
	}

	for _, c := range collections {
		c.mu.Lock()
		ctag := c.ctag
		c.mu.Unlock()

		var comps strings.Builder
		for _, comp := range c.components {
			comps.WriteString(fmt.Sprintf(`<c:comp name="%s"/>`, comp))
		}
		b.WriteString(fmt.Sprintf(`
 <d:response>
  <d:href>%s</d:href>
  <d:propstat>
   <d:prop>
    <d:resourcetype><d:collection/><c:%s/></d:resourcetype>
    <d:displayname>%s</d:displayname>
    <cs:getctag>sync-token:1 %d</cs:getctag>
    <c:supported-calendar-component-set>%s</c:supported-calendar-component-set>
   </d:prop>
   <d:status>HTTP/1.1 200 OK</d:status>
  </d:propstat>
 </d:response>`, c.Href(), c.resourceType, c.displayName, ctag, comps.String()))
	}
	return b.String()
}

var timeRangePattern = regexp.MustCompile(`<c:time-range start="([^"]+)" end="([^"]+)"/>`)

// queryResponse answers a calendar-query. It honours the time-range filter the
// way RFC 4791 requires — an object matches when one of its occurrences falls
// in the window, recurrence included — so that a test which forgets the window
// gets everything, exactly as a real account would answer.
func (c *Collection) queryResponse(body string) string {
	from, to, ok := parseTimeRange(body)

	c.mu.Lock()
	names := make([]string, 0, len(c.objects))
	for name := range c.objects {
		names = append(names, name)
	}
	sort.Strings(names)
	objects := make(map[string]string, len(c.objects))
	for name, data := range c.objects {
		objects[name] = data
	}
	etag := c.ctag
	c.mu.Unlock()

	var b strings.Builder
	for _, name := range names {
		data := objects[name]
		if ok && !touchesWindow(data, from, to) {
			continue
		}
		b.WriteString(fmt.Sprintf(`
 <d:response>
  <d:href>%s%s.ics</d:href>
  <d:propstat>
   <d:prop>
    <d:getetag>"%d"</d:getetag>
    <c:calendar-data>%s</c:calendar-data>
   </d:prop>
   <d:status>HTTP/1.1 200 OK</d:status>
  </d:propstat>
 </d:response>`, c.Href(), name, etag, escapeXML(data)))
	}
	return b.String()
}

func parseTimeRange(body string) (time.Time, time.Time, bool) {
	match := timeRangePattern.FindStringSubmatch(body)
	if match == nil {
		return time.Time{}, time.Time{}, false
	}
	const layout = "20060102T150405Z"
	from, err1 := time.Parse(layout, match[1])
	to, err2 := time.Parse(layout, match[2])
	if err1 != nil || err2 != nil {
		return time.Time{}, time.Time{}, false
	}
	return from, to, true
}

func touchesWindow(data string, from, to time.Time) bool {
	events, err := calendar.ParseObject(data)
	if err != nil {
		// An object the plugin cannot read is still an object the server
		// holds, so it is returned and the plugin decides what to do with it.
		return true
	}
	occurrences, problems := recurrence.Expand(events, from, to)
	if len(problems) > 0 {
		// The server does not share the plugin's opinion of what it can
		// expand, so an object the plugin chokes on is still returned.
		return true
	}
	return len(occurrences) > 0
}

func principalResponse(principal string) string {
	return fmt.Sprintf(`
 <d:response>
  <d:href>/</d:href>
  <d:propstat>
   <d:prop><d:current-user-principal><d:href>%s</d:href></d:current-user-principal></d:prop>
   <d:status>HTTP/1.1 200 OK</d:status>
  </d:propstat>
 </d:response>`, principal)
}

func homeSetResponse(principal, home string) string {
	return fmt.Sprintf(`
 <d:response>
  <d:href>%s</d:href>
  <d:propstat>
   <d:prop><c:calendar-home-set><d:href>%s</d:href></c:calendar-home-set></d:prop>
   <d:status>HTTP/1.1 200 OK</d:status>
  </d:propstat>
 </d:response>`, principal, home)
}

func writeMultistatus(w http.ResponseWriter, responses string) {
	w.Header().Set("Content-Type", `application/xml; charset="utf-8"`)
	w.WriteHeader(http.StatusMultiStatus)
	fmt.Fprintf(w, `<?xml version="1.0" encoding="utf-8"?>
<d:multistatus xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav" xmlns:cs="http://calendarserver.org/ns/">%s
</d:multistatus>`, responses)
}

func escapeXML(s string) string {
	replacer := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	return replacer.Replace(s)
}

// equalPath compares request paths the way the real server does: the same
// account is reachable under more than one spelling of its own name.
func equalPath(a, b string) bool {
	return strings.EqualFold(strings.TrimSuffix(a, "/"), strings.TrimSuffix(b, "/"))
}
