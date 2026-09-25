package calendarapi

import (
	"errors"
	"fmt"
	"net/http"
)

// StatusError is a response the provider refused. As on the CalDAV path, the
// line between authentication and everything else is what the whole failure
// story rests on: a revoked Connection must be retired, and a provider having a
// bad afternoon must not be.
type StatusError struct {
	StatusCode int
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("calendarapi: reading Events: %s", http.StatusText(e.StatusCode))
}

// TransportError is the provider being unreachable. It is never a reason to
// retire a Connection.
type TransportError struct {
	Err error
}

func (e *TransportError) Error() string {
	return fmt.Sprintf("calendarapi: reading Events: %v", e.Err)
}

func (e *TransportError) Unwrap() error { return e.Err }

// IsAuthFailure reports whether the provider rejected the credential itself. A
// 403 counts: it is what a token without the events scope gets.
func IsAuthFailure(err error) bool {
	var status *StatusError
	if !errors.As(err, &status) {
		return false
	}
	return status.StatusCode == http.StatusUnauthorized || status.StatusCode == http.StatusForbidden
}

// IsTransient reports whether the failure is worth trying again: the provider
// was unreachable, rate-limiting, or briefly broken.
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
