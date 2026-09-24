// Package caldav speaks just enough of the CalDAV protocol to read a person's
// calendars, and deliberately no more.
//
// The Client has no method that creates, changes or deletes anything at the
// provider. That is the point: the plugin has to ask Yandex for write-capable
// consent because every narrower scope is refused, so the guarantee that it
// never writes has to be carried by the code rather than by the grant.
package caldav

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// readMethods are the only HTTP methods this package will ever send. Anything
// else is a programming error, and one with consequences for someone's
// calendar, so it is refused rather than logged.
var readMethods = map[string]bool{
	http.MethodOptions: true,
	"PROPFIND":         true,
	"REPORT":           true,
}

// Doer is the slice of http.Client this package uses.
type Doer interface {
	Do(req *http.Request) (*http.Response, error)
}

// Authorizer supplies the Authorization header value for each request, so that
// a token refreshed between calls is picked up without rebuilding the Client.
type Authorizer func(ctx context.Context) (string, error)

// Client reads calendars from one CalDAV endpoint on behalf of one person.
type Client struct {
	base       *url.URL
	doer       Doer
	authorizer Authorizer
}

// New builds a Client against a CalDAV endpoint.
func New(baseURL string, doer Doer, authorizer Authorizer) (*Client, error) {
	base, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("caldav: bad endpoint %q: %w", baseURL, err)
	}
	if doer == nil {
		doer = &http.Client{Timeout: 30 * time.Second}
	}
	return &Client{base: base, doer: doer, authorizer: authorizer}, nil
}

// Collection is one calendar collection that holds events.
type Collection struct {
	// Href is exactly what the server returned. It is never reconstructed
	// from the login: the same account comes back with different letter
	// casing depending on how the request authenticated.
	Href        string
	DisplayName string
	// CTag changes whenever anything in the collection changes, which is how
	// a poll skips collections with nothing new in them.
	CTag string
}

// Object is one stored resource: the iCalendar body of one Event, which for a
// recurring Event carries the master and every overridden occurrence.
type Object struct {
	Href string
	ETag string
	Data string
}

// FindCalendarHome discovers where this account's calendars live. The chain is
// current-user-principal, then calendar-home-set on the principal. Nothing in
// it may be guessed: the documented default collection name does not exist on
// real accounts.
func (c *Client) FindCalendarHome(ctx context.Context) (string, error) {
	principal, err := c.propfindOne(ctx, "/", 0, propfindCurrentUserPrincipal, func(p props) string {
		return p.CurrentUserPrincipal.href()
	})
	if err != nil {
		return "", fmt.Errorf("caldav: finding the principal: %w", err)
	}
	if principal == "" {
		return "", fmt.Errorf("caldav: the server named no principal for this account")
	}

	home, err := c.propfindOne(ctx, principal, 0, propfindCalendarHomeSet, func(p props) string {
		return p.CalendarHomeSet.href()
	})
	if err != nil {
		return "", fmt.Errorf("caldav: finding the calendar home: %w", err)
	}
	if home == "" {
		return "", fmt.Errorf("caldav: the server named no calendar home for %s", principal)
	}
	return home, nil
}

// ListCalendars returns the collections in a calendar home that hold events,
// with their change tags. It costs exactly one request: a depth-one read of
// the home answers with every collection's tag at once, which is what makes a
// poll tick affordable.
//
// Collections are kept only when they are calendars and their component set
// includes VEVENT. That is what excludes the task list, whose events would
// otherwise be read as Events, and the scheduling inbox and outbox.
func (c *Client) ListCalendars(ctx context.Context, homeHref string) ([]Collection, error) {
	responses, err := c.propfind(ctx, homeHref, 1, propfindCollections)
	if err != nil {
		return nil, fmt.Errorf("caldav: listing calendars: %w", err)
	}

	var collections []Collection
	for _, response := range responses {
		href := response.hrefValue()
		if href == "" || sameHref(href, homeHref) {
			continue
		}
		p := response.props()
		if !p.ResourceType.isCalendar() || !p.SupportedComponents.includes("VEVENT") {
			continue
		}
		collections = append(collections, Collection{
			Href:        href,
			DisplayName: p.DisplayName,
			CTag:        p.CTag,
		})
	}
	return collections, nil
}

// QueryEvents returns the objects in a collection that touch [from, to). The
// window is never omitted: a query with no time filter returns the entire
// collection, which on a real account means years of history.
func (c *Client) QueryEvents(ctx context.Context, collectionHref string, from, to time.Time) ([]Object, error) {
	body := calendarQuery(from, to)
	responses, err := c.request(ctx, "REPORT", collectionHref, 1, body)
	if err != nil {
		return nil, fmt.Errorf("caldav: querying %s: %w", collectionHref, err)
	}

	var objects []Object
	for _, response := range responses {
		p := response.props()
		if strings.TrimSpace(p.CalendarData) == "" {
			continue
		}
		objects = append(objects, Object{
			Href: response.hrefValue(),
			ETag: p.ETag,
			Data: p.CalendarData,
		})
	}
	return objects, nil
}

func (c *Client) propfind(ctx context.Context, href string, depth int, body string) ([]response, error) {
	return c.request(ctx, "PROPFIND", href, depth, body)
}

func (c *Client) propfindOne(ctx context.Context, href string, depth int, body string, pick func(props) string) (string, error) {
	responses, err := c.propfind(ctx, href, depth, body)
	if err != nil {
		return "", err
	}
	for _, response := range responses {
		if value := pick(response.props()); value != "" {
			return value, nil
		}
	}
	return "", nil
}

func (c *Client) request(ctx context.Context, method, href string, depth int, body string) ([]response, error) {
	if !readMethods[method] {
		// Unreachable by design, and worth keeping unreachable.
		return nil, fmt.Errorf("caldav: refusing to send %s: this client only reads", method)
	}

	target, err := c.resolve(href)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, method, target, strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", `application/xml; charset="utf-8"`)
	req.Header.Set("Depth", fmt.Sprintf("%d", depth))
	if c.authorizer != nil {
		authorization, err := c.authorizer(ctx)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", authorization)
	}

	resp, err := c.doer.Do(req)
	if err != nil {
		// A transport failure is the provider being unreachable, which is
		// never a reason to treat a Connection as dead.
		return nil, &TransportError{Method: method, Href: href, Err: err}
	}
	defer resp.Body.Close()

	payload, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, &TransportError{Method: method, Href: href, Err: err}
	}

	if resp.StatusCode != http.StatusMultiStatus && resp.StatusCode != http.StatusOK {
		return nil, &StatusError{Method: method, Href: href, StatusCode: resp.StatusCode}
	}
	return parseMultistatus(payload)
}

// resolve turns an href the server gave us into an absolute URL without
// rewriting it. Hrefs arrive percent-encoded and case-sensitive-looking; they
// are passed through untouched.
func (c *Client) resolve(href string) (string, error) {
	ref, err := url.Parse(href)
	if err != nil {
		return "", fmt.Errorf("caldav: unusable href %q: %w", href, err)
	}
	return c.base.ResolveReference(ref).String(), nil
}

// maxResponseBytes caps a single response. Event payloads are not small — a
// real object carries dozens of historical timezone transitions — but a
// collection listing that runs to megabytes is a fault, not a calendar.
const maxResponseBytes = 32 << 20

// sameHref compares two hrefs the way the protocol requires: case-insensitively
// and ignoring a trailing slash. The same account returns different casing
// depending on whether the request authenticated with a password or a token.
func sameHref(a, b string) bool {
	return strings.EqualFold(strings.TrimSuffix(a, "/"), strings.TrimSuffix(b, "/"))
}

// SameHref exposes href comparison to callers that store hrefs between polls.
func SameHref(a, b string) bool { return sameHref(a, b) }
