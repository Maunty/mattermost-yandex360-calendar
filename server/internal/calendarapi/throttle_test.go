package calendarapi_test

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/maunty/mattermost-yandex360-calendar/server/internal/calendarapi"
	"github.com/maunty/mattermost-yandex360-calendar/server/internal/fakecalendarapi"
)

// stopwatch is a Doer that records when each request reached it, then passes
// it on to next, or answers 200 itself when there is no next.
type stopwatch struct {
	next  calendarapi.Doer
	mu    sync.Mutex
	times []time.Time
}

func (s *stopwatch) Do(req *http.Request) (*http.Response, error) {
	s.mu.Lock()
	s.times = append(s.times, time.Now())
	s.mu.Unlock()
	if s.next != nil {
		return s.next.Do(req)
	}
	return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody}, nil
}

func (s *stopwatch) closestGap() time.Duration {
	closest := time.Duration(1<<63 - 1)
	for i := 1; i < len(s.times); i++ {
		closest = min(closest, s.times[i].Sub(s.times[i-1]))
	}
	return closest
}

func TestOnePersonsReadStaysUnderTheirRateLimit(t *testing.T) {
	// Yandex allows 5 requests a second for each person. A read that runs to
	// several pages must not go faster than that.
	server := fakecalendarapi.New(t)
	server.PageSize = 1
	calendar := server.AddCalendar("events", "events")
	calendar.Put("a", standup)
	calendar.Put("b",
		"BEGIN:VCALENDAR\r\nVERSION:2.0\r\nBEGIN:VEVENT\r\nUID:b\r\nSUMMARY:Review\r\n"+
			"DTSTART:20260924T090000Z\r\nDTEND:20260924T100000Z\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n")
	recorded := &stopwatch{next: server.Client()}
	client, err := calendarapi.New(server.URL, recorded, func(context.Context) (string, error) {
		return "OAuth " + server.Token, nil
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if _, _, err := client.Occurrences(context.Background(), from, to); err != nil {
		t.Fatalf("Occurrences: %v", err)
	}

	if len(recorded.times) < 2 {
		t.Fatalf("the read took %d requests; the test needs it to page", len(recorded.times))
	}
	if gap := recorded.closestGap(); gap < 200*time.Millisecond {
		t.Errorf("pages were requested %v apart, faster than 5 a second", gap)
	}
}

func TestAThrottleSpacesRequestsOut(t *testing.T) {
	recorded := &stopwatch{}
	throttle := calendarapi.NewThrottle(recorded, 20)

	// From several goroutines at once, so that the spacing cannot come from
	// the caller waiting for each answer.
	var wg sync.WaitGroup
	for range 6 {
		wg.Go(func() {
			req, _ := http.NewRequest(http.MethodGet, "http://example.invalid", nil)
			if _, err := throttle.Do(req); err != nil {
				t.Errorf("Do: %v", err)
			}
		})
	}
	wg.Wait()

	// 20 a second is one every 50ms. A little slack for the scheduler.
	if gap := recorded.closestGap(); gap < 45*time.Millisecond {
		t.Errorf("two requests were %v apart, want at least 50ms", gap)
	}
}

func TestAThrottledRequestGivesUpWhenItsContextDoes(t *testing.T) {
	throttle := calendarapi.NewThrottle(&stopwatch{}, 1)
	req, _ := http.NewRequest(http.MethodGet, "http://example.invalid", nil)
	_, _ = throttle.Do(req)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err := throttle.Do(req.WithContext(ctx))

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("got %v, want the context's error", err)
	}
	if waited := time.Since(started); waited > 500*time.Millisecond {
		t.Errorf("waited %v for a slot after the context ended", waited)
	}
}

func TestBeingRateLimitedSaysHowLongToWait(t *testing.T) {
	for _, c := range []struct {
		status     int
		retryAfter string
		limited    bool
		wait       time.Duration
	}{
		{http.StatusTooManyRequests, "120", true, 2 * time.Minute},
		{http.StatusTooManyRequests, "", true, 0},
		{http.StatusTooManyRequests, "soon", true, 0},
		{http.StatusServiceUnavailable, "120", false, 0},
	} {
		server := fakecalendarapi.New(t)
		server.RetryAfter = c.retryAfter
		server.Fail(c.status, 1)

		_, _, err := clientFor(t, server).Occurrences(context.Background(), from, to)

		wait, limited := calendarapi.RateLimited(err)
		if limited != c.limited || wait != c.wait {
			t.Errorf("%d with Retry-After %q: RateLimited = %v, %v; want %v, %v",
				c.status, c.retryAfter, wait, limited, c.wait, c.limited)
		}
	}
}
