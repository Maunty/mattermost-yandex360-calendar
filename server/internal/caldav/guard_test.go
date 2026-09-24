package caldav

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

// refusingDoer fails the test if it is ever asked to send anything.
type refusingDoer struct{ t *testing.T }

func (d refusingDoer) Do(req *http.Request) (*http.Response, error) {
	d.t.Fatalf("a %s request reached the transport", req.Method)
	return nil, nil
}

func TestAWriteMethodIsRefusedBeforeItReachesTheTransport(t *testing.T) {
	client, err := New("https://caldav.example/", refusingDoer{t}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	for _, method := range []string{http.MethodPut, http.MethodPost, http.MethodDelete, "MKCALENDAR", "PROPPATCH", "MOVE"} {
		_, err := client.request(context.Background(), method, "/calendars/x/", 0, "")
		if err == nil {
			t.Errorf("%s was allowed", method)
			continue
		}
		if !strings.Contains(err.Error(), "only reads") {
			t.Errorf("%s was refused for the wrong reason: %v", method, err)
		}
	}
}
