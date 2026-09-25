package main

import (
	"strings"
	"testing"
)

// Ticket 08: the Daily Summary, and the settings that govern it.

func TestADailySummaryArrivesAtEightInTheMorning(t *testing.T) {
	yesterday := moment(2026, 9, 23, 20, 0)
	h := connectedHarness(t, yesterday)
	calendar := h.calendar("events-1000001")
	calendar.Put("a", timedEvent("a", "Standup", moment(2026, 9, 24, 10, 0), moment(2026, 9, 24, 10, 30)))
	calendar.Put("b", timedEvent("b", "Review", moment(2026, 9, 24, 15, 0), moment(2026, 9, 24, 16, 0)))
	h.plugin.RunPoll(yesterday)

	h.plugin.RunDelivery(moment(2026, 9, 24, 7, 55))
	if summaries := h.summaries(); len(summaries) != 0 {
		t.Fatalf("a summary arrived before the chosen time:\n%s", allText(summaries))
	}

	h.plugin.RunDelivery(moment(2026, 9, 24, 8, 0))

	summaries := h.summaries()
	if len(summaries) != 1 {
		t.Fatalf("got %d summaries at 08:00, want one:\n%s", len(summaries), allText(summaries))
	}
	shown := summaries[0].Message
	for _, want := range []string{"Thursday, 24 September", "Standup", "10:00", "Review", "15:00"} {
		if !strings.Contains(shown, want) {
			t.Errorf("the summary does not mention %q:\n%s", want, shown)
		}
	}
}

func TestTheDailySummaryIsInStartOrder(t *testing.T) {
	yesterday := moment(2026, 9, 23, 20, 0)
	h := connectedHarness(t, yesterday)
	calendar := h.calendar("events-1000001")
	calendar.Put("late", timedEvent("late", "Afternoon thing", moment(2026, 9, 24, 16, 0), moment(2026, 9, 24, 17, 0)))
	calendar.Put("early", timedEvent("early", "Morning thing", moment(2026, 9, 24, 9, 0), moment(2026, 9, 24, 10, 0)))
	h.plugin.RunPoll(yesterday)

	h.plugin.RunDelivery(moment(2026, 9, 24, 8, 0))

	shown := h.summaries()[0].Message
	if strings.Index(shown, "Morning thing") > strings.Index(shown, "Afternoon thing") {
		t.Errorf("the summary is not in start order:\n%s", shown)
	}
}

func TestAClearDayStillGetsAMessage(t *testing.T) {
	yesterday := moment(2026, 9, 23, 20, 0)
	h := connectedHarness(t, yesterday)
	h.calendar("events-1000001")
	h.plugin.RunPoll(yesterday)

	h.plugin.RunDelivery(moment(2026, 9, 24, 8, 0))

	summaries := h.summaries()
	if len(summaries) != 1 {
		t.Fatalf("got %d summaries for an empty day, want one saying so", len(summaries))
	}
	if !strings.Contains(strings.ToLower(summaries[0].Message), "clear") {
		t.Errorf("an empty day did not say it was clear:\n%s", summaries[0].Message)
	}
}

func TestAllDayEventsAppearInTheDailySummary(t *testing.T) {
	yesterday := moment(2026, 9, 23, 20, 0)
	h := connectedHarness(t, yesterday)
	calendar := h.calendar("events-1000001")
	calendar.Put("holiday", allDayEvent("holiday", "Public holiday", moment(2026, 9, 24, 0, 0)))
	h.plugin.RunPoll(yesterday)

	h.plugin.RunDelivery(moment(2026, 9, 24, 8, 0))

	if shown := h.summaries()[0].Message; !strings.Contains(shown, "Public holiday") {
		t.Errorf("an all-day event was left out of the summary:\n%s", shown)
	}
}

func TestExactlyOneDailySummaryPerDay(t *testing.T) {
	yesterday := moment(2026, 9, 23, 20, 0)
	h := connectedHarness(t, yesterday)
	calendar := h.calendar("events-1000001")
	calendar.Put("a", timedEvent("a", "Standup", moment(2026, 9, 24, 10, 0), moment(2026, 9, 24, 10, 30)))
	h.plugin.RunPoll(yesterday)

	for hour := 8; hour < 18; hour++ {
		h.plugin.RunDelivery(moment(2026, 9, 24, hour, 0))
	}

	if summaries := h.summaries(); len(summaries) != 1 {
		t.Errorf("got %d summaries in one day:\n%s", len(summaries), allText(summaries))
	}
}

func TestARestartDoesNotResendTheDailySummary(t *testing.T) {
	yesterday := moment(2026, 9, 23, 20, 0)
	h := connectedHarness(t, yesterday)
	h.calendar("events-1000001")
	h.plugin.RunPoll(yesterday)
	h.plugin.RunDelivery(moment(2026, 9, 24, 8, 0))
	h.clearPosts()

	if err := h.plugin.OnDeactivate(); err != nil {
		t.Fatalf("OnDeactivate: %v", err)
	}
	if err := h.plugin.OnActivate(); err != nil {
		t.Fatalf("OnActivate: %v", err)
	}
	h.plugin.RunDelivery(moment(2026, 9, 24, 8, 5))

	if summaries := h.summaries(); len(summaries) != 0 {
		t.Errorf("a restart resent the summary:\n%s", allText(summaries))
	}
}

func TestTheNextDayGetsItsOwnSummary(t *testing.T) {
	yesterday := moment(2026, 9, 23, 20, 0)
	h := connectedHarness(t, yesterday)
	h.calendar("events-1000001")
	h.plugin.RunPoll(yesterday)

	h.plugin.RunDelivery(moment(2026, 9, 24, 8, 0))
	h.plugin.RunPoll(moment(2026, 9, 25, 7, 0))
	h.plugin.RunDelivery(moment(2026, 9, 25, 8, 0))

	if summaries := h.summaries(); len(summaries) != 2 {
		t.Errorf("got %d summaries across two days, want one each:\n%s", len(summaries), allText(summaries))
	}
}

func TestTheDeliveryTimeCanBeChanged(t *testing.T) {
	yesterday := moment(2026, 9, 23, 20, 0)
	h := connectedHarness(t, yesterday)
	h.calendar("events-1000001")

	response := h.command("/yacal summary 06:30", yesterday)
	if !strings.Contains(response.Text, "06:30") {
		t.Fatalf("changing the time was not confirmed: %s", response.Text)
	}
	h.plugin.RunPoll(yesterday)
	h.clearPosts()

	h.plugin.RunDelivery(moment(2026, 9, 24, 6, 0))
	if summaries := h.summaries(); len(summaries) != 0 {
		t.Fatalf("a summary arrived before the chosen time:\n%s", allText(summaries))
	}

	h.plugin.RunDelivery(moment(2026, 9, 24, 6, 30))
	if summaries := h.summaries(); len(summaries) != 1 {
		t.Errorf("got %d summaries at the chosen time, want one", len(summaries))
	}
}

func TestARubbishDeliveryTimeIsRefusedWithoutChangingAnything(t *testing.T) {
	now := moment(2026, 9, 23, 20, 0)
	h := connectedHarness(t, now)

	response := h.command("/yacal summary half past six", now)

	if !strings.Contains(response.Text, "08:00") && !strings.Contains(response.Text, "not") {
		t.Errorf("a rubbish time was not explained: %s", response.Text)
	}
	settings, _ := h.plugin.store.Settings(testUserID)
	if settings.SummaryTimeText() != "08:00" {
		t.Errorf("the stored time changed to %q", settings.SummaryTimeText())
	}
}

func TestTheSummaryArrivesInTheReadersOwnTimezone(t *testing.T) {
	yesterday := moment(2026, 9, 23, 20, 0)
	h := connectedHarness(t, yesterday)
	h.calendar("events-1000001")
	h.api.setTimezone("Europe/Berlin")
	h.plugin.RunPoll(yesterday)

	// 08:00 in Moscow is 07:00 in Berlin, so a Berlin reader is not due yet.
	h.plugin.RunDelivery(moment(2026, 9, 24, 8, 0))
	if summaries := h.summaries(); len(summaries) != 0 {
		t.Fatalf("the summary arrived at Moscow's eight o'clock:\n%s", allText(summaries))
	}

	h.plugin.RunDelivery(moment(2026, 9, 24, 9, 0)) // 08:00 in Berlin
	if summaries := h.summaries(); len(summaries) != 1 {
		t.Errorf("got %d summaries at the reader's own eight o'clock", len(summaries))
	}
}

func TestChangingTimezoneMovesTheNextSummary(t *testing.T) {
	h := connectedHarness(t, moment(2026, 9, 23, 20, 0))
	h.calendar("events-1000001")
	h.plugin.RunPoll(moment(2026, 9, 23, 20, 0))
	h.plugin.RunDelivery(moment(2026, 9, 24, 8, 0)) // Moscow's eight
	h.clearPosts()

	h.api.setTimezone("Europe/Berlin")
	h.plugin.RunPoll(moment(2026, 9, 25, 5, 0))

	h.plugin.RunDelivery(moment(2026, 9, 25, 8, 0))
	if summaries := h.summaries(); len(summaries) != 0 {
		t.Fatalf("the summary still arrived at the old local time:\n%s", allText(summaries))
	}
	h.plugin.RunDelivery(moment(2026, 9, 25, 9, 0))
	if summaries := h.summaries(); len(summaries) != 1 {
		t.Errorf("got %d summaries at the new local time", len(summaries))
	}
}

func TestTheSummaryCanBeTurnedOffAndOnWithoutTouchingReminders(t *testing.T) {
	yesterday := moment(2026, 9, 23, 20, 0)
	h := connectedHarness(t, yesterday)
	calendar := h.calendar("events-1000001")
	calendar.Put("a", timedEvent("a", "Standup", moment(2026, 9, 24, 10, 0), moment(2026, 9, 24, 10, 30)))

	h.command("/yacal summary off", yesterday)
	h.clearPosts()
	h.plugin.RunPoll(yesterday)
	h.plugin.RunDelivery(moment(2026, 9, 24, 8, 0))
	h.plugin.RunDelivery(moment(2026, 9, 24, 9, 50))

	if summaries := h.summaries(); len(summaries) != 0 {
		t.Errorf("the summary was turned off but %d arrived:\n%s", len(summaries), allText(summaries))
	}
	if reminders := h.reminders(); len(reminders) != 1 {
		t.Errorf("turning the summary off also stopped reminders: got %d", len(reminders))
	}

	h.command("/yacal summary on", moment(2026, 9, 24, 20, 0))
	h.clearPosts()
	h.plugin.RunPoll(moment(2026, 9, 25, 7, 0))
	h.plugin.RunDelivery(moment(2026, 9, 25, 8, 0))

	if summaries := h.summaries(); len(summaries) != 1 {
		t.Errorf("the summary did not come back on: got %d", len(summaries))
	}
}

func TestRemindersCanBeTurnedOffAndOnWithoutTouchingTheSummary(t *testing.T) {
	yesterday := moment(2026, 9, 23, 20, 0)
	h := connectedHarness(t, yesterday)
	calendar := h.calendar("events-1000001")
	calendar.Put("a", timedEvent("a", "Standup", moment(2026, 9, 24, 10, 0), moment(2026, 9, 24, 10, 30)))

	h.command("/yacal reminders off", yesterday)
	h.clearPosts()
	h.plugin.RunPoll(yesterday)
	h.plugin.RunDelivery(moment(2026, 9, 24, 8, 0))
	h.plugin.RunDelivery(moment(2026, 9, 24, 9, 50))

	if reminders := h.reminders(); len(reminders) != 0 {
		t.Errorf("reminders were turned off but %d arrived", len(reminders))
	}
	if summaries := h.summaries(); len(summaries) != 1 {
		t.Errorf("turning reminders off also stopped the summary: got %d", len(summaries))
	}

	h.command("/yacal reminders on", moment(2026, 9, 24, 20, 0))
	h.clearPosts()
	h.plugin.RunPoll(moment(2026, 9, 25, 8, 0))
	calendar.Put("b", timedEvent("b", "Standup", moment(2026, 9, 25, 10, 0), moment(2026, 9, 25, 10, 30)))
	h.plugin.RunPoll(moment(2026, 9, 25, 9, 0))
	h.plugin.RunDelivery(moment(2026, 9, 25, 9, 50))

	if reminders := h.reminders(); len(reminders) != 1 {
		t.Errorf("reminders did not come back on: got %d", len(reminders))
	}
}

func TestSettingsShowsWhatThePluginThinksYouWant(t *testing.T) {
	now := moment(2026, 9, 24, 9, 0)
	h := connectedHarness(t, now)
	h.calendar("events-1000001")
	h.command("/yacal summary 07:15", now)
	h.command("/yacal reminders off", now)

	shown := h.command("/yacal settings", now).Text

	for _, want := range []string{"sam@yandex.ru", "07:15", "Europe/Moscow", "10 minutes"} {
		if !strings.Contains(shown, want) {
			t.Errorf("settings does not show %q:\n%s", want, shown)
		}
	}
	if !strings.Contains(shown, "| Reminders | off") {
		t.Errorf("settings does not show reminders as off:\n%s", shown)
	}
}

func TestSettingsShowsWhenTheCalendarWasLastRead(t *testing.T) {
	now := moment(2026, 9, 24, 9, 0)
	h := connectedHarness(t, now)
	h.calendar("events-1000001")

	before := h.command("/yacal settings", now).Text
	if !strings.Contains(before, "not yet") {
		t.Errorf("before any read, settings should say so:\n%s", before)
	}

	h.plugin.RunPoll(now)
	after := h.command("/yacal settings", now).Text
	if !strings.Contains(after, "24 Sep, 09:00") {
		t.Errorf("settings does not show when the calendar was last read:\n%s", after)
	}
}

func TestSettingsSurviveDeactivationAndReactivation(t *testing.T) {
	now := moment(2026, 9, 24, 9, 0)
	h := connectedHarness(t, now)
	h.command("/yacal summary 06:45", now)

	if err := h.plugin.OnDeactivate(); err != nil {
		t.Fatalf("OnDeactivate: %v", err)
	}
	if err := h.plugin.OnActivate(); err != nil {
		t.Fatalf("OnActivate: %v", err)
	}

	settings, err := h.plugin.store.Settings(testUserID)
	if err != nil {
		t.Fatalf("Settings: %v", err)
	}
	if settings.SummaryTimeText() != "06:45" {
		t.Errorf("the chosen time did not survive: %q", settings.SummaryTimeText())
	}
}

func TestTheSummaryIsADirectMessage(t *testing.T) {
	yesterday := moment(2026, 9, 23, 20, 0)
	h := connectedHarness(t, yesterday)
	h.calendar("events-1000001")
	h.plugin.RunPoll(yesterday)

	h.plugin.RunDelivery(moment(2026, 9, 24, 8, 0))

	summaries := h.summaries()
	if len(summaries) != 1 {
		t.Fatalf("got %d summaries", len(summaries))
	}
	if !strings.HasPrefix(summaries[0].ChannelId, "dm_") || !strings.Contains(summaries[0].ChannelId, testUserID) {
		t.Errorf("the summary was not a direct message to the person: %q", summaries[0].ChannelId)
	}
}

func TestTheSummaryCarriesNoEventDescriptions(t *testing.T) {
	yesterday := moment(2026, 9, 23, 20, 0)
	h := connectedHarness(t, yesterday)
	calendar := h.calendar("events-1000001")
	calendar.Put("private", timedEvent("private", "One to one",
		moment(2026, 9, 24, 10, 0), moment(2026, 9, 24, 11, 0),
		"DESCRIPTION:Salary discussion"))
	h.plugin.RunPoll(yesterday)

	h.plugin.RunDelivery(moment(2026, 9, 24, 8, 0))

	if shown := allText(h.summaries()); strings.Contains(shown, "Salary") {
		t.Errorf("a description reached the summary:\n%s", shown)
	}
}

func TestTodayOnDemandMatchesWhatTheSummaryWouldSay(t *testing.T) {
	// Somebody who does not want to wait until tomorrow morning should not
	// have to learn a second format.
	now := moment(2026, 9, 24, 9, 0)
	h := connectedHarness(t, now)
	calendar := h.calendar("events-1000001")
	calendar.Put("a", timedEvent("a", "Standup", moment(2026, 9, 24, 10, 0), moment(2026, 9, 24, 10, 30)))

	onDemand := h.todayText(now)
	h.plugin.RunPoll(now)
	h.plugin.RunDelivery(moment(2026, 9, 24, 9, 30))

	summaries := h.summaries()
	if len(summaries) != 1 {
		t.Fatalf("got %d summaries", len(summaries))
	}
	if strings.TrimSpace(onDemand) != strings.TrimSpace(summaries[0].Message) {
		t.Errorf("on demand and the daily summary disagree:\n%s\n---\n%s", onDemand, summaries[0].Message)
	}
}

func TestTheDailySummaryWaitsUntilItsTimeEvenWhenLate(t *testing.T) {
	// The plugin was down all morning. The person still gets their day, once,
	// when it comes back, rather than never.
	yesterday := moment(2026, 9, 23, 20, 0)
	h := connectedHarness(t, yesterday)
	calendar := h.calendar("events-1000001")
	calendar.Put("a", timedEvent("a", "Afternoon review", moment(2026, 9, 24, 15, 0), moment(2026, 9, 24, 16, 0)))
	h.plugin.RunPoll(yesterday)

	h.plugin.RunDelivery(moment(2026, 9, 24, 11, 30))

	summaries := h.summaries()
	if len(summaries) != 1 {
		t.Fatalf("got %d summaries after a late start, want one", len(summaries))
	}
	if !strings.Contains(summaries[0].Message, "Afternoon review") {
		t.Errorf("the late summary does not list the day:\n%s", summaries[0].Message)
	}
}

func TestDefaultsApplyToSomebodyWhoNeverTouchedTheirSettings(t *testing.T) {
	var settings Settings

	if !settings.RemindersEnabled() || !settings.DailySummaryEnabled() {
		t.Error("a person who has changed nothing should get both message types")
	}
	if settings.SummaryTimeText() != "08:00" {
		t.Errorf("the default delivery time is %q, want 08:00", settings.SummaryTimeText())
	}
	if hour, minute := settings.SummaryTime(); hour != 8 || minute != 0 {
		t.Errorf("the default delivery time parsed to %02d:%02d", hour, minute)
	}
}

func TestADeclinedEventIsLeftOutOfTheDailySummary(t *testing.T) {
	now := moment(2026, 9, 24, 7, 0)
	h := connectedHarness(t, now)
	calendar := h.calendar("events-1000001")
	calendar.PutDeclined("declined", timedEvent("declined", "Meeting I said no to",
		moment(2026, 9, 24, 11, 0), moment(2026, 9, 24, 12, 0)))
	calendar.Put("kept", timedEvent("kept", "Planning", moment(2026, 9, 24, 14, 0), moment(2026, 9, 24, 15, 0)))

	shown := h.todayText(now)

	if strings.Contains(shown, "Meeting I said no to") {
		t.Errorf("a declined Event was listed:\n%s", shown)
	}
	if !strings.Contains(shown, "Planning") {
		t.Errorf("the rest of the day was not listed:\n%s", shown)
	}
}
