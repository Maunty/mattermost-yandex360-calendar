package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
)

const commandTrigger = "yacal"

// commandDefinition registers the command and the autocomplete data that lets
// somebody discover the subcommands without memorising them.
func commandDefinition() *model.Command {
	summaryTimes := []model.AutocompleteListItem{
		{Item: "07:00"}, {Item: "08:00"}, {Item: "09:00"},
	}
	onOff := []model.AutocompleteListItem{
		{Item: "on", HelpText: "Start sending these again"},
		{Item: "off", HelpText: "Stop sending these"},
	}

	root := model.NewAutocompleteData(commandTrigger, "[command]", "Your Yandex Calendar, in Mattermost")

	root.AddCommand(model.NewAutocompleteData("connect", "", "Connect your Yandex Calendar"))
	root.AddCommand(model.NewAutocompleteData("disconnect", "", "Disconnect it and stop all messages"))
	root.AddCommand(model.NewAutocompleteData("today", "", "List today's events"))
	root.AddCommand(model.NewAutocompleteData("settings", "", "Show your current choices"))

	reminders := model.NewAutocompleteData("reminders", "[on|off]", "Turn reminders before each event on or off")
	reminders.AddStaticListArgument("Whether to send reminders", true, onOff)
	root.AddCommand(reminders)

	summary := model.NewAutocompleteData("summary", "[on|off|HH:MM]", "Turn the daily summary on or off, or choose when it arrives")
	summary.AddStaticListArgument("on, off, or a time of day", true,
		append(append([]model.AutocompleteListItem{}, onOff...), summaryTimes...))
	root.AddCommand(summary)

	root.AddCommand(model.NewAutocompleteData("help", "", "List what this plugin can do"))

	return &model.Command{
		Trigger:              commandTrigger,
		AutoComplete:         true,
		AutoCompleteDesc:     "Your Yandex Calendar, in Mattermost",
		AutoCompleteHint:     "[connect|today|settings|help]",
		AutocompleteData:     root,
		AutocompleteIconData: "",
		DisplayName:          "Yandex Calendar",
		Description:          "Reminders and a daily summary from your Yandex Calendar. Read-only.",
	}
}

// ExecuteCommand is the hook Mattermost calls. It does nothing but supply the
// clock: everything below takes the current time as a value, so that a test
// can run the same code at any moment it likes.
func (p *Plugin) ExecuteCommand(_ *plugin.Context, args *model.CommandArgs) (*model.CommandResponse, *model.AppError) {
	response := p.executeCommand(context.Background(), args, time.Now())
	return response, nil
}

func (p *Plugin) executeCommand(ctx context.Context, args *model.CommandArgs, now time.Time) *model.CommandResponse {
	fields := strings.Fields(args.Command)
	var subcommand string
	if len(fields) > 1 {
		subcommand = strings.ToLower(fields[1])
	}
	rest := fields[min(len(fields), 2):]

	// Until an administrator has supplied the Yandex application's
	// credentials the plugin can do nothing, and says so rather than failing
	// in some way the person has to interpret.
	if !p.getConfiguration().IsConfigured() && subcommand != "help" {
		return ephemeral(fmt.Sprintf(
			"%s. An administrator needs to supply %s in the System Console, under Plugins → Yandex Calendar.",
			capitalise(errNotConfigured.Error()), englishList(p.getConfiguration().Missing())))
	}

	switch subcommand {
	case "", "help":
		return ephemeral(p.helpText())
	case "connect":
		return p.commandConnect(args)
	case "disconnect":
		return p.commandDisconnect(args)
	case "today":
		return p.commandToday(ctx, args, now)
	case "settings":
		return p.commandSettings(args)
	case "reminders":
		return p.commandReminders(args, rest)
	case "summary":
		return p.commandSummary(args, rest)
	default:
		return ephemeral(fmt.Sprintf("I do not know `%s`.\n\n%s", sanitise(subcommand), p.helpText()))
	}
}

func (p *Plugin) helpText() string {
	var b strings.Builder
	b.WriteString("### Yandex Calendar\n")
	b.WriteString("I send you a reminder shortly before each of your events, and a summary of your day each morning.\n")
	b.WriteString("I only ever read your calendar: I never create, change or delete anything in it.\n\n")
	b.WriteString("| Command | What it does |\n|---|---|\n")
	b.WriteString("| `/yacal connect` | Connect your Yandex Calendar |\n")
	b.WriteString("| `/yacal disconnect` | Disconnect it and stop all messages |\n")
	b.WriteString("| `/yacal today` | List today's events |\n")
	b.WriteString("| `/yacal settings` | Show your current choices |\n")
	b.WriteString("| `/yacal reminders on\\|off` | Turn reminders before each event on or off |\n")
	b.WriteString("| `/yacal summary on\\|off` | Turn the daily summary on or off |\n")
	b.WriteString("| `/yacal summary HH:MM` | Choose when the daily summary arrives |\n")
	b.WriteString("| `/yacal help` | Show this |\n")

	if !p.getConfiguration().IsConfigured() {
		b.WriteString(fmt.Sprintf("\nI am not set up yet: an administrator needs to supply %s in the System Console.",
			englishList(p.getConfiguration().Missing())))
	}
	return b.String()
}

func (p *Plugin) commandConnect(args *model.CommandArgs) *model.CommandResponse {
	// A working Connection is not quietly replaced by a second flow. A
	// retired one is exactly what this command is for: every message about a
	// broken Connection tells the person to run it.
	if connection, err := p.store.Connection(args.UserId); err == nil && connection.Active {
		return ephemeral(fmt.Sprintf(
			"Your Yandex Calendar is already connected as **%s**. Run `/yacal disconnect` first if you want to connect a different account.",
			sanitise(connection.YandexLogin)))
	} else if err != nil && !isNotConnected(err) {
		p.client.Log.Warn("Could not read a Connection before connecting", "error", err.Error())
	}

	link, err := p.connectLink(args.UserId)
	if err != nil {
		p.client.Log.Error("Could not start a consent flow", "error", err.Error())
		return ephemeral("Something went wrong starting the connection. Please try again in a moment.")
	}

	return ephemeral(fmt.Sprintf(
		"[Connect your Yandex Calendar](%s)\n\n"+
			"Yandex will ask you to approve access. It asks for permission to change your calendar as well as read it, "+
			"because Yandex does not offer a read-only permission that works here — but this plugin never writes anything. "+
			"If the window does not open, copy the link above into your browser.", link))
}

func (p *Plugin) commandDisconnect(args *model.CommandArgs) *model.CommandResponse {
	if _, err := p.store.Connection(args.UserId); err != nil {
		if isNotConnected(err) {
			return ephemeral(notConnectedMessage)
		}
		// The stored Connection is unreadable — for instance because the
		// encryption key was regenerated. Disconnecting must still work.
		p.client.Log.Warn("Unreadable Connection during disconnect", "error", err.Error())
	}

	if err := p.store.DeleteConnection(args.UserId); err != nil {
		p.client.Log.Error("Could not delete a Connection", "error", err.Error())
		return ephemeral("Something went wrong disconnecting. Please try again in a moment.")
	}
	if err := p.store.DeleteSettings(args.UserId); err != nil {
		p.client.Log.Warn("Could not delete settings during disconnect", "error", err.Error())
	}

	return ephemeral("Your Yandex Calendar is disconnected. I have forgotten your tokens and will send you nothing further.")
}

func (p *Plugin) commandToday(ctx context.Context, args *model.CommandArgs, now time.Time) *model.CommandResponse {
	connection, err := p.store.Connection(args.UserId)
	if err != nil {
		return ephemeral(p.connectionProblemMessage(err))
	}
	if !connection.Active {
		return ephemeral(reconnectMessage)
	}

	loc := p.userLocation(args.UserId)
	from, to := dayBounds(now, loc)

	occurrences, err := p.readCalendars(ctx, connection, from, to, now)
	if err != nil {
		p.recordFailure(connection, err)
		p.client.Log.Warn("Could not read a calendar for /yacal today",
			"user_id", args.UserId, "error", err.Error())
		return ephemeral(p.readFailureMessage(err))
	}
	p.recordSuccess(connection)
	p.noteCalendarRead(args.UserId, now)

	post := todayPost(now, occurrences, loc)
	post.ChannelId = args.ChannelId
	p.client.Post.SendEphemeralPost(args.UserId, post)
	return &model.CommandResponse{}
}

func (p *Plugin) commandSettings(args *model.CommandArgs) *model.CommandResponse {
	connection, err := p.store.Connection(args.UserId)
	if err != nil {
		return ephemeral(p.connectionProblemMessage(err))
	}

	settings, err := p.store.Settings(args.UserId)
	if err != nil {
		p.client.Log.Error("Could not read settings", "error", err.Error())
		return ephemeral("Something went wrong reading your settings. Please try again in a moment.")
	}

	loc := p.userLocation(args.UserId)
	lead := humaniseLead(p.getConfiguration().ReminderLead())

	var b strings.Builder
	b.WriteString("### Your Yandex Calendar settings\n")
	b.WriteString(fmt.Sprintf("| Connected account | %s |\n|---|---|\n", sanitise(connection.YandexLogin)))
	b.WriteString(fmt.Sprintf("| Reminders | %s, %s before each event |\n", onOffText(settings.RemindersEnabled()), lead))
	b.WriteString(fmt.Sprintf("| Daily summary | %s, at %s |\n", onOffText(settings.DailySummaryEnabled()), settings.SummaryTimeText()))
	b.WriteString(fmt.Sprintf("| Your timezone | %s |\n", loc.String()))
	b.WriteString(fmt.Sprintf("| Calendar last read | %s |\n", p.lastReadText(args.UserId, loc)))

	if !connection.Active {
		b.WriteString("\n" + reconnectMessage)
	}
	b.WriteString("\nThe reminder lead time is set for the whole server by an administrator.")
	return ephemeral(b.String())
}

func (p *Plugin) lastReadText(userID string, loc *time.Location) string {
	state, err := p.store.SyncState(userID)
	if err != nil || state.LastSuccessAt.IsZero() {
		return "not yet"
	}
	return state.LastSuccessAt.In(loc).Format("Mon 2 Jan, 15:04")
}

func (p *Plugin) commandReminders(args *model.CommandArgs, rest []string) *model.CommandResponse {
	return p.updateSettings(args, func(settings *Settings) (string, error) {
		on, err := parseOnOff(rest)
		if err != nil {
			return "", err
		}
		settings.RemindersDisabled = !on
		return fmt.Sprintf("Reminders are now **%s**. Your daily summary is unchanged.", onOffText(on)), nil
	})
}

func (p *Plugin) commandSummary(args *model.CommandArgs, rest []string) *model.CommandResponse {
	return p.updateSettings(args, func(settings *Settings) (string, error) {
		if len(rest) == 0 {
			return "", fmt.Errorf("say `/yacal summary on`, `/yacal summary off`, or a time such as `/yacal summary 08:00`")
		}
		value := strings.ToLower(rest[0])

		if on, err := parseOnOff(rest); err == nil {
			settings.DailySummaryDisabled = !on
			return fmt.Sprintf("Your daily summary is now **%s**. Reminders are unchanged.", onOffText(on)), nil
		}

		if _, _, err := parseClockTime(value); err != nil {
			return "", fmt.Errorf("`%s` is not `on`, `off`, or a time of day such as `08:00`", sanitise(value))
		}
		settings.DailySummaryTime = value
		settings.DailySummaryDisabled = false
		return fmt.Sprintf("Your daily summary will arrive at **%s** in your own timezone.", value), nil
	})
}

func (p *Plugin) updateSettings(args *model.CommandArgs, change func(*Settings) (string, error)) *model.CommandResponse {
	if _, err := p.store.Connection(args.UserId); err != nil {
		return ephemeral(p.connectionProblemMessage(err))
	}

	settings, err := p.store.Settings(args.UserId)
	if err != nil {
		p.client.Log.Error("Could not read settings", "error", err.Error())
		return ephemeral("Something went wrong reading your settings. Please try again in a moment.")
	}

	message, err := change(&settings)
	if err != nil {
		return ephemeral(err.Error())
	}
	if err := p.store.SaveSettings(args.UserId, settings); err != nil {
		p.client.Log.Error("Could not save settings", "error", err.Error())
		return ephemeral("Something went wrong saving your settings. Please try again in a moment.")
	}
	return ephemeral(message)
}

func parseOnOff(rest []string) (bool, error) {
	if len(rest) == 0 {
		return false, fmt.Errorf("say `on` or `off`")
	}
	switch strings.ToLower(rest[0]) {
	case "on", "enable", "enabled", "yes":
		return true, nil
	case "off", "disable", "disabled", "no":
		return false, nil
	default:
		return false, fmt.Errorf("`%s` is not `on` or `off`", sanitise(rest[0]))
	}
}

func onOffText(on bool) string {
	if on {
		return "on"
	}
	return "off"
}

func ephemeral(message string) *model.CommandResponse {
	return &model.CommandResponse{
		ResponseType: model.CommandResponseTypeEphemeral,
		Text:         message,
	}
}

// dayBounds is the person's own day, in their own timezone.
func dayBounds(now time.Time, loc *time.Location) (time.Time, time.Time) {
	local := now.In(loc)
	start := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
	return start, start.AddDate(0, 0, 1)
}

func capitalise(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// englishList joins items the way a sentence does, because this text is read
// by an administrator deciding what to go and do.
func englishList(items []string) string {
	switch len(items) {
	case 0:
		return "nothing"
	case 1:
		return items[0]
	case 2:
		return items[0] + " and " + items[1]
	default:
		return strings.Join(items[:len(items)-1], ", ") + " and " + items[len(items)-1]
	}
}
