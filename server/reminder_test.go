package main

import (
	"strings"
	"testing"
	"time"
)

// Ticket 07: Reminders.

// tick polls and then delivers, which is what the two scheduled jobs do.
func (h *harness) tick(now time.Time) {
	h.plugin.RunPoll(now)
	h.plugin.RunDelivery(now)
}

func TestAReminderArrivesTheLeadTimeBeforeTheEvent(t *testing.T) {
	start := moment(2026, 9, 24, 10, 0)
	h := connectedHarness(t, start.Add(-time.Hour))
	calendar := h.calendar("events-1000001")
	calendar.Put("standup", timedEvent("standup", "Standup", start, start.Add(30*time.Minute),
		"LOCATION:Room 3", "X-TELEMOST-CONFERENCE:https://telemost.yandex.ru/j/42"))

	h.tick(start.Add(-time.Hour))
	if messages := h.reminders(); len(messages) != 0 {
		t.Fatalf("a reminder arrived an hour early:\n%s", allText(messages))
	}

	h.plugin.RunDelivery(start.Add(-10 * time.Minute))

	messages := h.reminders()
	if len(messages) != 1 {
		t.Fatalf("got %d reminders ten minutes before the event, want one:\n%s", len(messages), allText(messages))
	}
	shown := text(messages[0])
	for _, want := range []string{"Standup", "10:00", "10:30", "MSK", "Room 3", "https://telemost.yandex.ru/j/42"} {
		if !strings.Contains(shown, want) {
			t.Errorf("the reminder does not mention %q:\n%s", want, shown)
		}
	}
}

func TestTheReminderLeadTimeIsWhatTheAdministratorSet(t *testing.T) {
	start := moment(2026, 9, 24, 10, 0)
	h := connectedHarness(t, start.Add(-2*time.Hour))
	h.plugin.setConfiguration(&configuration{
		ClientID: "client-id", ClientSecret: "client-secret",
		EncryptionKey: "an-encryption-key-for-tests", ReminderLeadMinutes: 30,
	})
	calendar := h.calendar("events-1000001")
	calendar.Put("standup", timedEvent("standup", "Standup", start, start.Add(30*time.Minute)))

	h.tick(start.Add(-2 * time.Hour))
	h.plugin.RunDelivery(start.Add(-25 * time.Minute))

	if messages := h.reminders(); len(messages) != 1 {
		t.Fatalf("got %d reminders 25 minutes before a 30-minute lead time, want one", len(messages))
	}
}

func TestExactlyOneReminderIsSentPerOccurrence(t *testing.T) {
	start := moment(2026, 9, 24, 10, 0)
	h := connectedHarness(t, start.Add(-time.Hour))
	calendar := h.calendar("events-1000001")
	calendar.Put("standup", timedEvent("standup", "Standup", start, start.Add(30*time.Minute)))
	h.plugin.RunPoll(start.Add(-time.Hour))

	// Every minute from ten minutes before until the moment it starts.
	for offset := 10; offset >= 0; offset-- {
		h.plugin.RunDelivery(start.Add(-time.Duration(offset) * time.Minute))
	}

	if messages := h.reminders(); len(messages) != 1 {
		t.Fatalf("got %d reminders for one occurrence:\n%s", len(messages), allText(messages))
	}
}

func TestARestartDoesNotResendAReminder(t *testing.T) {
	start := moment(2026, 9, 24, 10, 0)
	h := connectedHarness(t, start.Add(-time.Hour))
	calendar := h.calendar("events-1000001")
	calendar.Put("standup", timedEvent("standup", "Standup", start, start.Add(30*time.Minute)))
	h.tick(start.Add(-time.Hour))
	h.plugin.RunDelivery(start.Add(-10 * time.Minute))
	h.clearPosts()

	if err := h.plugin.OnDeactivate(); err != nil {
		t.Fatalf("OnDeactivate: %v", err)
	}
	if err := h.plugin.OnActivate(); err != nil {
		t.Fatalf("OnActivate: %v", err)
	}
	h.plugin.RunDelivery(start.Add(-9 * time.Minute))

	if messages := h.reminders(); len(messages) != 0 {
		t.Errorf("a restart resent a reminder:\n%s", allText(messages))
	}
}

func TestAnEventThatHasAlreadyStartedProducesNoReminder(t *testing.T) {
	start := moment(2026, 9, 24, 10, 0)
	h := connectedHarness(t, start.Add(time.Minute))
	calendar := h.calendar("events-1000001")
	calendar.Put("standup", timedEvent("standup", "Standup", start, start.Add(30*time.Minute)))

	// The plugin comes up after the meeting began, which is what a restart
	// looks like from here.
	h.tick(start.Add(time.Minute))

	if messages := h.reminders(); len(messages) != 0 {
		t.Errorf("a reminder went out for a meeting already under way:\n%s", allText(messages))
	}
}

func TestAnAllDayEventProducesNoReminder(t *testing.T) {
	day := moment(2026, 9, 25, 0, 0)
	h := connectedHarness(t, moment(2026, 9, 24, 12, 0))
	calendar := h.calendar("events-1000001")
	calendar.Put("holiday", allDayEvent("holiday", "Public holiday", day))

	h.plugin.RunPoll(moment(2026, 9, 24, 12, 0))
	for _, at := range []time.Time{
		moment(2026, 9, 24, 23, 50), moment(2026, 9, 24, 23, 55), moment(2026, 9, 25, 0, 0),
	} {
		h.plugin.RunDelivery(at)
	}

	for _, message := range h.reminders() {
		if strings.Contains(text(message), "Public holiday") {
			t.Errorf("an all-day event produced a reminder:\n%s", text(message))
		}
	}
}

func TestAnEventDeletedAtTheProviderProducesNoReminder(t *testing.T) {
	start := moment(2026, 9, 24, 10, 0)
	h := connectedHarness(t, start.Add(-time.Hour))
	calendar := h.calendar("events-1000001")
	calendar.Put("standup", timedEvent("standup", "Standup", start, start.Add(30*time.Minute)))
	h.plugin.RunPoll(start.Add(-time.Hour))

	calendar.Remove("standup")
	h.plugin.RunPoll(start.Add(-15 * time.Minute))
	h.plugin.RunDelivery(start.Add(-10 * time.Minute))

	if messages := h.reminders(); len(messages) != 0 {
		t.Errorf("a reminder went out for a meeting that no longer exists:\n%s", allText(messages))
	}
}

func TestAnEventAddedAtTheLastMinuteStillReachesThePerson(t *testing.T) {
	// Story: a last-minute invitation is worth a reminder while there is
	// still time to act, and the message must not claim ten minutes when
	// there are three.
	start := moment(2026, 9, 24, 10, 0)
	h := connectedHarness(t, start.Add(-time.Hour))
	calendar := h.calendar("events-1000001")
	h.plugin.RunPoll(start.Add(-time.Hour))

	calendar.Put("urgent", timedEvent("urgent", "Urgent chat", start, start.Add(15*time.Minute)))
	h.tick(start.Add(-3 * time.Minute))

	messages := h.reminders()
	if len(messages) != 1 {
		t.Fatalf("got %d reminders for a last-minute invitation, want one:\n%s", len(messages), allText(messages))
	}
	if !strings.Contains(messages[0].Message, "3 minutes") {
		t.Errorf("the reminder does not say how long there actually is: %q", messages[0].Message)
	}
}

func TestAMovedOccurrenceIsRemindedAtItsNewTime(t *testing.T) {
	originalStart := moment(2026, 9, 24, 10, 0)
	h := connectedHarness(t, moment(2026, 9, 24, 8, 0))
	calendar := h.calendar("events-1000001")
	calendar.Put("weekly", recurringEvent("weekly", "Weekly sync",
		moment(2026, 8, 6, 10, 0), moment(2026, 8, 6, 11, 0), "FREQ=WEEKLY;BYDAY=TH"))
	h.plugin.RunPoll(moment(2026, 9, 24, 8, 0))

	// The person moves today's occurrence to the afternoon.
	calendar.Put("weekly", seriesWithOverride("weekly", "Weekly sync",
		moment(2026, 8, 6, 10, 0), moment(2026, 8, 6, 11, 0), "FREQ=WEEKLY;BYDAY=TH",
		movedOccurrence("weekly", "Weekly sync", originalStart,
			moment(2026, 9, 24, 15, 0), moment(2026, 9, 24, 16, 0))))
	h.plugin.RunPoll(moment(2026, 9, 24, 9, 0))

	h.plugin.RunDelivery(originalStart.Add(-10 * time.Minute))
	if messages := h.reminders(); len(messages) != 0 {
		t.Fatalf("a reminder went out at the old time:\n%s", allText(messages))
	}

	h.plugin.RunDelivery(moment(2026, 9, 24, 14, 50))
	messages := h.reminders()
	if len(messages) != 1 {
		t.Fatalf("got %d reminders at the new time, want one:\n%s", len(messages), allText(messages))
	}
	if !strings.Contains(text(messages[0]), "15:00") {
		t.Errorf("the reminder does not name the new time:\n%s", text(messages[0]))
	}
}

func TestRemindersComeFromEveryCalendar(t *testing.T) {
	start := moment(2026, 9, 24, 10, 0)
	h := connectedHarness(t, start.Add(-time.Hour))
	work := h.calendar("events-1000001")
	work.Put("w", timedEvent("w", "Work meeting", start, start.Add(30*time.Minute)))
	personal := h.calendar("events-9000001")
	personal.Put("p", timedEvent("p", "Dentist", start, start.Add(time.Hour)))

	h.tick(start.Add(-time.Hour))
	h.plugin.RunDelivery(start.Add(-10 * time.Minute))

	shown := allText(h.reminders())
	if !strings.Contains(shown, "Work meeting") || !strings.Contains(shown, "Dentist") {
		t.Errorf("reminders did not come from both calendars:\n%s", shown)
	}
}

func TestARecurringEventIsRemindedEveryWeek(t *testing.T) {
	h := connectedHarness(t, moment(2026, 9, 24, 8, 0))
	calendar := h.calendar("events-1000001")
	calendar.Put("weekly", recurringEvent("weekly", "Weekly sync",
		moment(2026, 9, 24, 10, 0), moment(2026, 9, 24, 11, 0), "FREQ=WEEKLY;BYDAY=TH"))

	for _, thursday := range []time.Time{
		moment(2026, 9, 24, 9, 50), moment(2026, 10, 1, 9, 50), moment(2026, 10, 8, 9, 50),
	} {
		h.plugin.RunPoll(thursday.Add(-time.Hour))
		h.plugin.RunDelivery(thursday)
	}

	if messages := h.reminders(); len(messages) != 3 {
		t.Errorf("got %d reminders across three weeks, want one each:\n%s", len(messages), allText(messages))
	}
}

func TestACancelledOccurrenceIsNotReminded(t *testing.T) {
	h := connectedHarness(t, moment(2026, 9, 24, 8, 0))
	calendar := h.calendar("events-1000001")
	calendar.Put("weekly", seriesWithOverride("weekly", "Weekly sync",
		moment(2026, 9, 24, 10, 0), moment(2026, 9, 24, 11, 0), "FREQ=WEEKLY;BYDAY=TH",
		cancelledOccurrence("weekly", "Weekly sync", moment(2026, 10, 1, 10, 0))))

	h.plugin.RunPoll(moment(2026, 10, 1, 9, 0))
	h.plugin.RunDelivery(moment(2026, 10, 1, 9, 50))

	if messages := h.reminders(); len(messages) != 0 {
		t.Errorf("an occurrence that was called off still produced a reminder:\n%s", allText(messages))
	}
}

func TestRemindersGoOnlyToThePersonTheyConcern(t *testing.T) {
	start := moment(2026, 9, 24, 10, 0)
	h := connectedHarness(t, start.Add(-time.Hour))
	calendar := h.calendar("events-1000001")
	calendar.Put("private", timedEvent("private", "Private meeting", start, start.Add(30*time.Minute)))

	h.tick(start.Add(-time.Hour))
	h.plugin.RunDelivery(start.Add(-10 * time.Minute))

	messages := h.reminders()
	if len(messages) != 1 {
		t.Fatalf("got %d reminders", len(messages))
	}
	if !strings.HasPrefix(messages[0].ChannelId, "dm_") {
		t.Errorf("the reminder was not a direct message: channel %q", messages[0].ChannelId)
	}
	if !strings.Contains(messages[0].ChannelId, testUserID) {
		t.Errorf("the reminder went somewhere other than this person's direct channel: %q", messages[0].ChannelId)
	}
}

func TestRemindersCarryNoEventDescription(t *testing.T) {
	start := moment(2026, 9, 24, 10, 0)
	h := connectedHarness(t, start.Add(-time.Hour))
	calendar := h.calendar("events-1000001")
	calendar.Put("private", timedEvent("private", "One to one", start, start.Add(30*time.Minute),
		"DESCRIPTION:Salary discussion and the reorganisation plan"))

	h.tick(start.Add(-time.Hour))
	h.plugin.RunDelivery(start.Add(-10 * time.Minute))

	if shown := allText(h.reminders()); strings.Contains(shown, "Salary") {
		t.Errorf("a description reached a reminder:\n%s", shown)
	}
}

func TestRemindersCanBeTurnedOff(t *testing.T) {
	start := moment(2026, 9, 24, 10, 0)
	h := connectedHarness(t, start.Add(-time.Hour))
	calendar := h.calendar("events-1000001")
	calendar.Put("standup", timedEvent("standup", "Standup", start, start.Add(30*time.Minute)))

	h.command("/yacal reminders off", start.Add(-time.Hour))
	h.clearPosts()

	h.tick(start.Add(-time.Hour))
	h.plugin.RunDelivery(start.Add(-10 * time.Minute))

	if messages := h.reminders(); len(messages) != 0 {
		t.Errorf("reminders were turned off but %d arrived:\n%s", len(messages), allText(messages))
	}
}

func TestManyPeopleWhoseEventsStartOnTheHourDoNotAllPollAtOnce(t *testing.T) {
	// Delivery is per minute and exact, so the spreading that matters is of
	// the reads behind it.
	now := moment(2026, 9, 24, 9, 0)
	counts := map[int]int{}
	for i := range 40 {
		userID := strings.Repeat(string(rune('a'+i%26)), 20) + strings.Repeat("0", 6)
		minute := int(nextPollAt(userID, now).Sub(now).Minutes())
		counts[minute]++
	}
	for minute, count := range counts {
		if count > 25 {
			t.Errorf("%d of 40 people would be read in the same minute (%d)", count, minute)
		}
	}
	if len(counts) < 3 {
		t.Errorf("forty people fell into only %d distinct minutes", len(counts))
	}
}

// Which Events are the person's. CONTEXT.md: the Events they organise, are
// invited to (required or optional), or watch, that they have not declined.

func TestADeclinedEventProducesNoReminder(t *testing.T) {
	start := moment(2026, 9, 24, 10, 0)
	h := connectedHarness(t, start.Add(-time.Hour))
	calendar := h.calendar("events-1000001")
	calendar.PutDeclined("declined", timedEvent("declined", "Meeting I said no to", start, start.Add(time.Hour)))

	h.tick(start.Add(-time.Hour))
	h.plugin.RunDelivery(start.Add(-10 * time.Minute))

	if messages := h.reminders(); len(messages) != 0 {
		t.Errorf("a declined Event was reminded:\n%s", allText(messages))
	}
}

func TestAWatchedEventIsReminded(t *testing.T) {
	start := moment(2026, 9, 24, 10, 0)
	h := connectedHarness(t, start.Add(-time.Hour))
	calendar := h.calendar("events-1000001")
	calendar.PutWatched("talk", timedEvent("talk", "A talk I added from a link", start, start.Add(time.Hour)))

	h.tick(start.Add(-time.Hour))
	h.plugin.RunDelivery(start.Add(-10 * time.Minute))

	if messages := h.reminders(); len(messages) != 1 {
		t.Errorf("got %d reminders for a Watched Event, want one", len(messages))
	}
}

func TestAnEventThePersonHasNoPartInProducesNoReminder(t *testing.T) {
	start := moment(2026, 9, 24, 10, 0)
	h := connectedHarness(t, start.Add(-time.Hour))
	calendar := h.calendar("events-1000001")
	calendar.PutItem("theirs", map[string]any{
		"ical_uid":      "theirs",
		"event_id":      "00000000-0000-0000-0000-000000000001",
		"start":         map[string]string{"date_time": "2026-09-24T10:00:00", "time_zone": "Europe/Moscow"},
		"end":           map[string]string{"date_time": "2026-09-24T11:00:00", "time_zone": "Europe/Moscow"},
		"summary":       "Their one-to-one",
		"relation_type": "NONE",
	})
	calendar.Put("mine", timedEvent("mine", "My standup", start, start.Add(30*time.Minute)))

	h.tick(start.Add(-time.Hour))
	h.plugin.RunDelivery(start.Add(-10 * time.Minute))

	shown := allText(h.reminders())
	if strings.Contains(shown, "Their one-to-one") {
		t.Errorf("an Event the person has no part in was reminded:\n%s", shown)
	}
	if !strings.Contains(shown, "My standup") {
		t.Errorf("the person's own Event was not reminded:\n%s", shown)
	}
}

func TestAnEventMarkedAsFreeTimeIsStillReminded(t *testing.T) {
	start := moment(2026, 9, 24, 10, 0)
	h := connectedHarness(t, start.Add(-time.Hour))
	calendar := h.calendar("events-1000001")
	calendar.Put("fyi", timedEvent("fyi", "Release goes out", start, start.Add(time.Hour), "TRANSP:TRANSPARENT"))

	h.tick(start.Add(-time.Hour))
	h.plugin.RunDelivery(start.Add(-10 * time.Minute))

	if messages := h.reminders(); len(messages) != 1 {
		t.Errorf("got %d reminders for an Event marked as free time, want one", len(messages))
	}
}
