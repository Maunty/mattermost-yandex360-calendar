package main

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

// Per-user Lead Time: each person chooses how long before their Events the
// Reminder arrives, or follows the Server Default.

// standupAt is a connected person with one Event on their calendar, already
// read into the cache.
func standupAt(t *testing.T, start time.Time) *harness {
	t.Helper()
	h := connectedHarness(t, start.Add(-2*time.Hour))
	h.calendar("events-1000001").Put("standup",
		timedEvent("standup", "Standup", start, start.Add(30*time.Minute)))
	h.plugin.RunPoll(start.Add(-2 * time.Hour))
	return h
}

func TestAChosenLeadTimeIsHonoured(t *testing.T) {
	start := moment(2026, 9, 24, 10, 0)
	h := standupAt(t, start)

	response := h.command("/yacal reminders 1", start.Add(-time.Hour))
	if !strings.Contains(response.Text, "a minute") {
		t.Errorf("choosing a Lead Time was not confirmed: %s", response.Text)
	}

	h.plugin.RunDelivery(start.Add(-10 * time.Minute))
	h.plugin.RunDelivery(start.Add(-150 * time.Second))
	if messages := h.reminders(); len(messages) != 0 {
		t.Fatalf("a person who chose 1 minute was reminded early:\n%s", allText(messages))
	}

	h.plugin.RunDelivery(start.Add(-2 * time.Minute))
	messages := h.reminders()
	if len(messages) != 1 {
		t.Fatalf("got %d reminders 2 minutes before a 1-minute Lead Time, want one", len(messages))
	}
	if !strings.Contains(messages[0].Message, "2 minutes") {
		t.Errorf("the reminder does not say how long there really is: %q", messages[0].Message)
	}
}

func TestALeadTimeOfZeroRemindsInTheLastMinute(t *testing.T) {
	start := moment(2026, 9, 24, 10, 0)
	h := standupAt(t, start)
	h.command("/yacal reminders 0", start.Add(-time.Hour))

	h.plugin.RunDelivery(start.Add(-90 * time.Second))
	if messages := h.reminders(); len(messages) != 0 {
		t.Fatalf("a Lead Time of 0 reminded more than a minute early:\n%s", allText(messages))
	}

	h.plugin.RunDelivery(start.Add(-20 * time.Second))
	messages := h.reminders()
	if len(messages) != 1 {
		t.Fatalf("got %d reminders 20 seconds before the start, want one", len(messages))
	}
	if !strings.Contains(messages[0].Message, "less than a minute") {
		t.Errorf("20 seconds before the start should say less than a minute: %q", messages[0].Message)
	}
	if shown := h.command("/yacal settings", start).Text; !strings.Contains(shown, "in the last minute before each event (your choice)") {
		t.Errorf("settings does not describe a Lead Time of 0 as it works:\n%s", shown)
	}
}

func TestALeadTimeOfZeroNeverRemindsAfterTheStart(t *testing.T) {
	start := moment(2026, 9, 24, 10, 0)
	h := standupAt(t, start)
	h.command("/yacal reminders 0", start.Add(-time.Hour))

	// The delivery runs straddle the start without landing inside the last
	// minute before it.
	h.plugin.RunDelivery(start.Add(-61 * time.Second))
	h.plugin.RunDelivery(start)
	h.plugin.RunDelivery(start.Add(time.Second))

	if messages := h.reminders(); len(messages) != 0 {
		t.Errorf("a reminder arrived for an Event already under way:\n%s", allText(messages))
	}
}

func TestAReminderIsNeverLaterThanTheLeadTime(t *testing.T) {
	start := moment(2026, 9, 24, 10, 0)
	for _, lead := range []int{0, 1, 5, 10} {
		for phase := time.Duration(0); phase < time.Minute; phase += 7 * time.Second {
			h := standupAt(t, start)
			h.command("/yacal reminders "+strconv.Itoa(lead), start.Add(-time.Hour))

			// Delivery wakes once a minute, at whatever second it happens to.
			var remaining time.Duration
			for at := start.Add(-20*time.Minute + phase); at.Before(start.Add(time.Minute)); at = at.Add(time.Minute) {
				h.plugin.RunDelivery(at)
				if len(h.reminders()) > 0 {
					remaining = start.Sub(at)
					break
				}
			}

			leadTime := time.Duration(lead) * time.Minute
			if remaining < leadTime || remaining <= 0 || remaining > leadTime+time.Minute {
				t.Errorf("Lead Time %d, runs %v past the minute: reminded %v before the start",
					lead, phase, remaining)
			}
		}
	}
}

func TestAChoiceSurvivesAChangeToTheServerDefault(t *testing.T) {
	start := moment(2026, 9, 24, 10, 0)
	h := standupAt(t, start)
	// Chosen, even though it equals the Server Default at the time.
	h.command("/yacal reminders 10", start.Add(-time.Hour))

	h.setServerDefault(5)
	h.plugin.RunDelivery(start.Add(-10 * time.Minute))

	if messages := h.reminders(); len(messages) != 1 {
		t.Errorf("got %d reminders 10 minutes before for somebody who chose 10, want one", len(messages))
	}
	if shown := h.command("/yacal settings", start).Text; !strings.Contains(shown, "10 minutes before each event (your choice)") {
		t.Errorf("settings does not show the choice:\n%s", shown)
	}
}

func TestSomebodyWhoNeverChoseFollowsTheServerDefault(t *testing.T) {
	start := moment(2026, 9, 24, 10, 0)
	h := standupAt(t, start)

	h.setServerDefault(5)
	h.plugin.RunDelivery(start.Add(-10 * time.Minute))
	if messages := h.reminders(); len(messages) != 0 {
		t.Fatalf("the old Server Default still applied:\n%s", allText(messages))
	}

	h.plugin.RunDelivery(start.Add(-6 * time.Minute))
	if messages := h.reminders(); len(messages) != 1 {
		t.Errorf("got %d reminders 6 minutes before a 5-minute Server Default, want one", len(messages))
	}
	if shown := h.command("/yacal settings", start).Text; !strings.Contains(shown, "5 minutes before each event (server default)") {
		t.Errorf("settings does not show the Server Default:\n%s", shown)
	}
}

func TestDefaultRestoresFollowingTheServerDefault(t *testing.T) {
	start := moment(2026, 9, 24, 10, 0)
	h := standupAt(t, start)
	h.command("/yacal reminders 30", start.Add(-time.Hour))

	response := h.command("/yacal reminders default", start.Add(-time.Hour))
	if !strings.Contains(response.Text, "10 minutes") {
		t.Errorf("going back to the Server Default was not confirmed: %s", response.Text)
	}

	h.plugin.RunDelivery(start.Add(-25 * time.Minute))
	if messages := h.reminders(); len(messages) != 0 {
		t.Errorf("the abandoned choice still applied:\n%s", allText(messages))
	}
	if shown := h.command("/yacal settings", start).Text; !strings.Contains(shown, "(server default)") {
		t.Errorf("settings does not show the Server Default:\n%s", shown)
	}
}

func TestDefaultLeavesRemindersOffIfTheyWereOff(t *testing.T) {
	now := moment(2026, 9, 24, 9, 0)
	h := connectedHarness(t, now)
	h.command("/yacal reminders 30", now)
	h.command("/yacal reminders off", now)

	h.command("/yacal reminders default", now)

	if shown := h.command("/yacal settings", now).Text; !strings.Contains(shown, "| Reminders | off") {
		t.Errorf("default turned reminders back on:\n%s", shown)
	}
}

func TestOffAndOnKeepTheChosenLeadTime(t *testing.T) {
	now := moment(2026, 9, 24, 9, 0)
	h := connectedHarness(t, now)
	h.command("/yacal reminders 30", now)

	h.command("/yacal reminders off", now)
	h.command("/yacal reminders on", now)

	if shown := h.command("/yacal settings", now).Text; !strings.Contains(shown, "on, 30 minutes before each event (your choice)") {
		t.Errorf("pausing reminders lost the chosen Lead Time:\n%s", shown)
	}
}

func TestChoosingALeadTimeTurnsRemindersBackOn(t *testing.T) {
	start := moment(2026, 9, 24, 10, 0)
	h := standupAt(t, start)
	h.command("/yacal reminders off", start.Add(-time.Hour))

	h.command("/yacal reminders 30", start.Add(-time.Hour))
	h.plugin.RunDelivery(start.Add(-25 * time.Minute))

	if messages := h.reminders(); len(messages) != 1 {
		t.Errorf("got %d reminders after choosing a Lead Time, want one", len(messages))
	}
}

func TestAnUnacceptableLeadTimeIsRefusedWithoutChangingAnything(t *testing.T) {
	now := moment(2026, 9, 24, 9, 0)
	for _, value := range []string{"75", "-1", "1.5", "soon", "61"} {
		h := connectedHarness(t, now)
		h.command("/yacal reminders off", now)

		response := h.command("/yacal reminders "+value, now)

		if !strings.Contains(response.Text, "0") || !strings.Contains(response.Text, "60") {
			t.Errorf("refusing %q does not say what is accepted: %s", value, response.Text)
		}
		shown := h.command("/yacal settings", now).Text
		if !strings.Contains(shown, "| Reminders | off, 10 minutes before each event (server default)") {
			t.Errorf("refusing %q changed the settings:\n%s", value, shown)
		}
	}
}

func TestShorteningAfterAReminderWasSentSendsNoSecondOne(t *testing.T) {
	start := moment(2026, 9, 24, 10, 0)
	h := standupAt(t, start)
	h.command("/yacal reminders 30", start.Add(-time.Hour))
	h.plugin.RunDelivery(start.Add(-30 * time.Minute))
	h.clearPosts()

	h.command("/yacal reminders 5", start.Add(-20*time.Minute))
	for offset := 6; offset >= 0; offset-- {
		h.plugin.RunDelivery(start.Add(-time.Duration(offset) * time.Minute))
	}

	if messages := h.reminders(); len(messages) != 0 {
		t.Errorf("shortening the Lead Time sent a second reminder:\n%s", allText(messages))
	}
}

func TestLengtheningRemindsOnTheNextRunWithTheRealTimeLeft(t *testing.T) {
	start := moment(2026, 9, 24, 10, 0)
	h := standupAt(t, start)
	h.plugin.RunDelivery(start.Add(-30 * time.Minute))

	h.command("/yacal reminders 45", start.Add(-30*time.Minute))
	h.plugin.RunDelivery(start.Add(-29 * time.Minute))

	messages := h.reminders()
	if len(messages) != 1 {
		t.Fatalf("got %d reminders on the run after lengthening, want one", len(messages))
	}
	if !strings.Contains(messages[0].Message, "29 minutes") {
		t.Errorf("the reminder does not say how long there really is: %q", messages[0].Message)
	}
}

func TestTheServerDefaultFallsBackAndIsCapped(t *testing.T) {
	now := moment(2026, 9, 24, 9, 0)
	for _, c := range []struct {
		minutes int
		want    string
	}{
		{0, "10 minutes"},
		{90, "an hour"},
		{60, "an hour"},
		{1, "a minute"},
	} {
		h := connectedHarness(t, now)
		h.setServerDefault(c.minutes)

		shown := h.command("/yacal settings", now).Text
		if !strings.Contains(shown, c.want+" before each event (server default)") {
			t.Errorf("a Server Default of %d should mean %s:\n%s", c.minutes, c.want, shown)
		}
	}
}

func TestSettingsDoesNotClaimOneLeadTimeForEverybody(t *testing.T) {
	now := moment(2026, 9, 24, 9, 0)
	h := connectedHarness(t, now)

	if shown := h.command("/yacal settings", now).Text; strings.Contains(shown, "whole server") {
		t.Errorf("settings still says an administrator sets the Lead Time for everybody:\n%s", shown)
	}
}

func TestAChosenLeadTimeSurvivesDeactivationAndReactivation(t *testing.T) {
	now := moment(2026, 9, 24, 9, 0)
	h := connectedHarness(t, now)
	h.command("/yacal reminders 1", now)

	if err := h.plugin.OnDeactivate(); err != nil {
		t.Fatalf("OnDeactivate: %v", err)
	}
	if err := h.plugin.OnActivate(); err != nil {
		t.Fatalf("OnActivate: %v", err)
	}

	if shown := h.command("/yacal settings", now).Text; !strings.Contains(shown, "a minute before each event (your choice)") {
		t.Errorf("the chosen Lead Time did not survive:\n%s", shown)
	}
}

func TestHelpListsTheLeadTimeCommands(t *testing.T) {
	now := moment(2026, 9, 24, 9, 0)
	h := connectedHarness(t, now)

	shown := h.command("/yacal help", now).Text
	for _, want := range []string{"/yacal reminders <minutes>", "/yacal reminders default"} {
		if !strings.Contains(shown, want) {
			t.Errorf("help does not list %q:\n%s", want, shown)
		}
	}
}

func TestTheMessageAfterConnectingStatesTheLeadTime(t *testing.T) {
	now := moment(2026, 9, 24, 9, 0)
	h := newHarness(t)
	h.consent(now)

	shown := allText(h.messages())
	for _, want := range []string{"10 minutes before each event", "/yacal reminders <minutes>"} {
		if !strings.Contains(shown, want) {
			t.Errorf("the message after connecting does not say %q:\n%s", want, shown)
		}
	}
}
