package calendarapi

import (
	"net/http"
	"sync"
	"time"
)

// Throttle is a Doer that spaces requests out evenly, because Yandex limits
// how many requests a second may be made for each person. Every Client has its
// own, since a Client reads for one person.
type Throttle struct {
	doer     Doer
	interval time.Duration

	mu   sync.Mutex
	next time.Time
}

// NewThrottle lets at most perSecond requests through to doer each second.
func NewThrottle(doer Doer, perSecond int) *Throttle {
	return &Throttle{doer: doer, interval: time.Second / time.Duration(perSecond)}
}

// Do waits for the next free slot, then sends the request. A request whose
// context ends while it waits is not sent.
func (t *Throttle) Do(req *http.Request) (*http.Response, error) {
	t.mu.Lock()
	now := time.Now()
	slot := t.next
	if slot.Before(now) {
		slot = now
	}
	t.next = slot.Add(t.interval)
	t.mu.Unlock()

	if wait := time.Until(slot); wait > 0 {
		timer := time.NewTimer(wait)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-req.Context().Done():
			return nil, req.Context().Err()
		}
	}
	return t.doer.Do(req)
}
