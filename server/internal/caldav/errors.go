package caldav

import (
	"errors"
	"fmt"
	"net/http"
)

// StatusError is a response the provider refused. Its distinction between
// authentication and everything else is the one the whole failure story rests
// on: a revoked Connection must be retired, and a provider having a bad
// afternoon must not be.
type StatusError struct {
	Method     string
	Href       string
	StatusCode int
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("caldav: %s %s: %s", e.Method, e.Href, http.StatusText(e.StatusCode))
}

// TransportError is the provider being unreachable: a refused connection, a
// timeout, a DNS failure. It is never a reason to retire a Connection.
type TransportError struct {
	Method string
	Href   string
	Err    error
}

func (e *TransportError) Error() string {
	return fmt.Sprintf("caldav: %s %s: %v", e.Method, e.Href, e.Err)
}

func (e *TransportError) Unwrap() error { return e.Err }

// IsAuthFailure reports whether the provider rejected the credential itself.
// Only this answer may retire a Connection.
func IsAuthFailure(err error) bool {
	var status *StatusError
	if !errors.As(err, &status) {
		return false
	}
	return status.StatusCode == http.StatusUnauthorized || status.StatusCode == http.StatusForbidden
}

// IsTransient reports whether the failure is one that is worth trying again:
// the provider was unreachable, overloaded, or briefly broken.
func IsTransient(err error) bool {
	var transport *TransportError
	if errors.As(err, &transport) {
		return true
	}
	var status *StatusError
	if errors.As(err, &status) {
		return status.StatusCode >= 500 || status.StatusCode == http.StatusTooManyRequests
	}
	return false
}
