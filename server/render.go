package main

import (
	"fmt"
	"net/url"
	"strings"
	"time"
	"unicode"

	"github.com/mattermost/mattermost/server/public/model"

	"github.com/Maunty/mattermost-ya-calendar/server/internal/calendar"
)

// Everything a provider put in a calendar is untrusted input on its way into a
// chat message. Titles and locations are escaped; descriptions are not
// rendered at all, because they are long, frequently HTML, and often hold
// private notes that have no business being reproduced in a DM.

// markdownSpecials are the characters that would otherwise be read as
// formatting, as a link, as a table cell boundary or as HTML. Ordinary
// punctuation is left alone: escaping a full stop would turn an email address
// into something nobody wants to read.
const markdownSpecials = "\\`*_[]~|<>#"

// maxRenderedText caps a single field. A calendar will happily hold a title of
// several thousand characters; a Reminder should still be readable.
const maxRenderedText = 200

// sanitise makes provider text safe to put in a post: no control characters,
// no line breaks that would break the layout, and nothing that would be read
// as Markdown formatting or as a link.
func sanitise(text string) string {
	text = strings.TrimSpace(text)

	var b strings.Builder
	for _, r := range text {
		switch {
		case r == '\n' || r == '\r' || r == '\t':
			b.WriteRune(' ')
		case unicode.IsControl(r):
			// dropped
		case strings.ContainsRune(markdownSpecials, r):
			b.WriteRune('\\')
			b.WriteRune(r)
		default:
			b.WriteRune(r)
		}
	}

	out := strings.Join(strings.Fields(b.String()), " ")
	if len([]rune(out)) > maxRenderedText {
		out = string([]rune(out)[:maxRenderedText]) + "…"
	}
	return out
}

// safeLink returns a conference link only when it is one a client can follow.
// Anything else — a scheme that runs code, a value that is not a URL at all —
// is dropped rather than rendered.
func safeLink(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return ""
	}
	if parsed.Host == "" {
		return ""
	}
	return parsed.String()
}

// titleOf is what to call an Event with no title of its own.
func titleOf(occurrence calendar.Occurrence) string {
	title := sanitise(occurrence.Title)
	if title == "" {
		return "(untitled)"
	}
	return title
}

// timeRange renders when an Occurrence runs, in the reader's own timezone.
func timeRange(occurrence calendar.Occurrence, loc *time.Location) string {
	if occurrence.AllDay {
		return "All day"
	}

	start := occurrence.Start.In(loc)
	end := occurrence.End.In(loc)

	zone, _ := start.Zone()
	if !end.After(start) {
		return fmt.Sprintf("%s (%s)", start.Format("15:04"), zone)
	}
	if sameDay(start, end) {
		return fmt.Sprintf("%s – %s (%s)", start.Format("15:04"), end.Format("15:04"), zone)
	}
	return fmt.Sprintf("%s – %s (%s)", start.Format("Mon 2 Jan 15:04"), end.Format("Mon 2 Jan 15:04"), zone)
}

func sameDay(a, b time.Time) bool {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	return ay == by && am == bm && ad == bd
}

// reminderPost is the message that arrives before an Event starts. The
// conference link is the title's link, so that joining is one click.
func reminderPost(occurrence calendar.Occurrence, loc *time.Location, lead time.Duration) *model.Post {
	attachment := &model.MessageAttachment{
		Title:    titleOf(occurrence),
		Fallback: fmt.Sprintf("%s starts in %s", titleOf(occurrence), humaniseLead(lead)),
		Fields: []*model.MessageAttachmentField{{
			Title: "When",
			Value: timeRange(occurrence, loc),
			Short: true,
		}},
	}
	if link := safeLink(occurrence.Conference); link != "" {
		attachment.TitleLink = link
		attachment.Fields = append(attachment.Fields, &model.MessageAttachmentField{
			Title: "Join",
			Value: link,
			Short: true,
		})
	}
	if location := sanitise(occurrence.Location); location != "" {
		attachment.Fields = append(attachment.Fields, &model.MessageAttachmentField{
			Title: "Where",
			Value: location,
			Short: false,
		})
	}

	post := &model.Post{Message: fmt.Sprintf("Starting in %s", humaniseLead(lead))}
	model.ParseMessageAttachment(post, []*model.MessageAttachment{attachment})
	return post
}

func humaniseLead(lead time.Duration) string {
	minutes := int(lead.Round(time.Minute).Minutes())
	switch {
	case minutes <= 1:
		return "a minute"
	case minutes < 60:
		return fmt.Sprintf("%d minutes", minutes)
	case minutes == 60:
		return "an hour"
	case minutes%60 == 0:
		return fmt.Sprintf("%d hours", minutes/60)
	default:
		return fmt.Sprintf("%dh%02d", minutes/60, minutes%60)
	}
}

// dailySummaryPost lists a day. An empty day is still worth a message: the
// person learns their day is clear rather than wondering whether the plugin
// is working.
func dailySummaryPost(day time.Time, occurrences []calendar.Occurrence, loc *time.Location) *model.Post {
	heading := fmt.Sprintf("#### %s", day.In(loc).Format("Monday, 2 January"))

	if len(occurrences) == 0 {
		return &model.Post{Message: heading + "\nYour day is clear."}
	}

	var b strings.Builder
	b.WriteString(heading)
	b.WriteString("\n")
	for _, occurrence := range occurrences {
		b.WriteString(fmt.Sprintf("- **%s** — %s", timeRange(occurrence, loc), titleOf(occurrence)))
		if location := sanitise(occurrence.Location); location != "" {
			b.WriteString(" · " + location)
		}
		if link := safeLink(occurrence.Conference); link != "" {
			b.WriteString(" · [join](" + link + ")")
		}
		b.WriteString("\n")
	}
	return &model.Post{Message: strings.TrimRight(b.String(), "\n")}
}

// todayPost answers /yacal today. It is the Daily Summary's content on demand,
// which is what a person asking for it expects to see.
func todayPost(day time.Time, occurrences []calendar.Occurrence, loc *time.Location) *model.Post {
	return dailySummaryPost(day, occurrences, loc)
}
