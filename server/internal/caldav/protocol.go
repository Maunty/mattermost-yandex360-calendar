package caldav

import (
	"encoding/xml"
	"fmt"
	"strings"
	"time"
)

// The request bodies. They are constants rather than marshalled structs
// because what goes on the wire is exactly this, and a reader comparing the
// plugin against a packet capture should see the same text in both.

const propfindCurrentUserPrincipal = `<?xml version="1.0" encoding="utf-8"?>
<d:propfind xmlns:d="DAV:">
  <d:prop><d:current-user-principal/></d:prop>
</d:propfind>`

const propfindCalendarHomeSet = `<?xml version="1.0" encoding="utf-8"?>
<d:propfind xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav">
  <d:prop><c:calendar-home-set/></d:prop>
</d:propfind>`

const propfindCollections = `<?xml version="1.0" encoding="utf-8"?>
<d:propfind xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav" xmlns:cs="http://calendarserver.org/ns/">
  <d:prop>
    <d:resourcetype/>
    <d:displayname/>
    <cs:getctag/>
    <c:supported-calendar-component-set/>
  </d:prop>
</d:propfind>`

// calendarQuery asks for the events that touch a window. Yandex accepts a
// request to expand recurrence here and silently ignores it, returning masters
// with their rules intact, so no expand element is sent: the plugin expands
// what comes back itself.
func calendarQuery(from, to time.Time) string {
	const layout = "20060102T150405Z"
	return fmt.Sprintf(`<?xml version="1.0" encoding="utf-8"?>
<c:calendar-query xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav">
  <d:prop>
    <d:getetag/>
    <c:calendar-data/>
  </d:prop>
  <c:filter>
    <c:comp-filter name="VCALENDAR">
      <c:comp-filter name="VEVENT">
        <c:time-range start="%s" end="%s"/>
      </c:comp-filter>
    </c:comp-filter>
  </c:filter>
</c:calendar-query>`, from.UTC().Format(layout), to.UTC().Format(layout))
}

// The response shapes.

type multistatus struct {
	XMLName   xml.Name   `xml:"DAV: multistatus"`
	Responses []response `xml:"DAV: response"`
}

type response struct {
	Href      string     `xml:"DAV: href"`
	Propstats []propstat `xml:"DAV: propstat"`
}

type propstat struct {
	Status string `xml:"DAV: status"`
	Prop   props  `xml:"DAV: prop"`
}

type props struct {
	CurrentUserPrincipal *hrefValue `xml:"DAV: current-user-principal"`
	CalendarHomeSet      *hrefValue `xml:"urn:ietf:params:xml:ns:caldav calendar-home-set"`
	DisplayName          string     `xml:"DAV: displayname"`
	ResourceType         *resources `xml:"DAV: resourcetype"`
	SupportedComponents  *supported `xml:"urn:ietf:params:xml:ns:caldav supported-calendar-component-set"`
	CTag                 string     `xml:"http://calendarserver.org/ns/ getctag"`
	ETag                 string     `xml:"DAV: getetag"`
	CalendarData         string     `xml:"urn:ietf:params:xml:ns:caldav calendar-data"`
}

type hrefValue struct {
	Href string `xml:"DAV: href"`
}

func (h *hrefValue) href() string {
	if h == nil {
		return ""
	}
	return strings.TrimSpace(h.Href)
}

type resources struct {
	Calendar *struct{} `xml:"urn:ietf:params:xml:ns:caldav calendar"`
}

func (r *resources) isCalendar() bool { return r != nil && r.Calendar != nil }

type supported struct {
	Comps []struct {
		Name string `xml:"name,attr"`
	} `xml:"urn:ietf:params:xml:ns:caldav comp"`
}

func (s *supported) includes(name string) bool {
	if s == nil {
		return false
	}
	for _, comp := range s.Comps {
		if strings.EqualFold(comp.Name, name) {
			return true
		}
	}
	return false
}

func (r response) hrefValue() string { return strings.TrimSpace(r.Href) }

// props merges the propstat sections that succeeded. A server answers a
// property it does not hold with its own 404 inside a 207, which is normal and
// not an error: the home collection itself has no change tag, for instance.
func (r response) props() props {
	var merged props
	for _, ps := range r.Propstats {
		if !strings.Contains(ps.Status, " 200 ") {
			continue
		}
		p := ps.Prop
		if p.CurrentUserPrincipal != nil {
			merged.CurrentUserPrincipal = p.CurrentUserPrincipal
		}
		if p.CalendarHomeSet != nil {
			merged.CalendarHomeSet = p.CalendarHomeSet
		}
		if p.DisplayName != "" {
			merged.DisplayName = p.DisplayName
		}
		if p.ResourceType != nil {
			merged.ResourceType = p.ResourceType
		}
		if p.SupportedComponents != nil {
			merged.SupportedComponents = p.SupportedComponents
		}
		if p.CTag != "" {
			merged.CTag = p.CTag
		}
		if p.ETag != "" {
			merged.ETag = p.ETag
		}
		if p.CalendarData != "" {
			merged.CalendarData = p.CalendarData
		}
	}
	return merged
}

func parseMultistatus(payload []byte) ([]response, error) {
	var ms multistatus
	if err := xml.Unmarshal(payload, &ms); err != nil {
		return nil, fmt.Errorf("caldav: unreadable response: %w", err)
	}
	return ms.Responses, nil
}
