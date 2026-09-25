package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Ticket 02: the plugin installs and responds before anything is connected.

func manifest(t *testing.T) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "plugin.json"))
	if err != nil {
		t.Fatalf("reading plugin.json: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("plugin.json is not valid JSON: %v", err)
	}
	return parsed
}

func TestTheManifestTargetsTheElevenSeries(t *testing.T) {
	// Version 12 is not released, and requiring it would exclude every
	// currently supported extended-support deployment.
	version, _ := manifest(t)["min_server_version"].(string)
	if !strings.HasPrefix(version, "11.") {
		t.Errorf("min_server_version is %q, want something in the 11 series", version)
	}
}

func TestTheManifestIdMatchesTheCallbackPath(t *testing.T) {
	// The redirect registered on the Yandex application is built from this.
	// If they drift apart, every consent flow fails at the last step.
	id, _ := manifest(t)["id"].(string)
	if id != manifestID {
		t.Errorf("plugin.json declares id %q but the code builds URLs with %q", id, manifestID)
	}
}

func TestTheSystemConsoleOffersEverythingAnAdministratorMustSupply(t *testing.T) {
	schema, _ := manifest(t)["settings_schema"].(map[string]any)
	if schema == nil {
		t.Fatal("plugin.json declares no settings schema, so nothing can be configured")
	}
	settings, _ := schema["settings"].([]any)

	kinds := map[string]string{}
	for _, entry := range settings {
		setting, _ := entry.(map[string]any)
		key, _ := setting["key"].(string)
		kind, _ := setting["type"].(string)
		kinds[key] = kind
	}

	for _, key := range []string{"ClientID", "ClientSecret", "ReminderLeadMinutes", "EncryptionKey"} {
		if _, ok := kinds[key]; !ok {
			t.Errorf("the System Console does not offer %s", key)
		}
	}
	if kinds["EncryptionKey"] != "generated" {
		t.Errorf("the encryption key is a %q setting; it must be generated rather than typed", kinds["EncryptionKey"])
	}

	for _, entry := range settings {
		setting, _ := entry.(map[string]any)
		if setting["key"] == "ReminderLeadMinutes" {
			if lead, _ := setting["default"].(float64); lead != 10 {
				t.Errorf("the reminder lead time defaults to %v minutes, want 10", setting["default"])
			}
		}
	}
}

func TestTheDescriptionTellsPeopleThePluginOnlyReads(t *testing.T) {
	description, _ := manifest(t)["description"].(string)
	lowered := strings.ToLower(description)
	if !strings.Contains(lowered, "read") {
		t.Errorf("the plugin's own description does not say it only reads:\n%s", description)
	}
}

func TestActivationCreatesTheBotAndRegistersTheCommand(t *testing.T) {
	h := newHarness(t)
	h.plugin.botID = ""

	if err := h.plugin.OnActivate(); err != nil {
		t.Fatalf("OnActivate: %v", err)
	}

	if h.plugin.botID != testBotID {
		t.Errorf("no bot account was created: botID is %q", h.plugin.botID)
	}
}

func TestDeactivatingAndReactivatingNeedsNoRestart(t *testing.T) {
	h := newHarness(t)

	for range 3 {
		if err := h.plugin.OnActivate(); err != nil {
			t.Fatalf("OnActivate: %v", err)
		}
		if err := h.plugin.OnDeactivate(); err != nil {
			t.Fatalf("OnDeactivate: %v", err)
		}
	}
}

func TestTheCommandOffersAutocompleteForEverySubcommand(t *testing.T) {
	command := commandDefinition()

	if !command.AutoComplete {
		t.Fatal("the command does not offer autocomplete")
	}
	if command.AutocompleteData == nil {
		t.Fatal("the command carries no autocomplete data, so nothing can be discovered")
	}

	offered := map[string]bool{}
	for _, sub := range command.AutocompleteData.SubCommands {
		offered[sub.Trigger] = true
	}
	for _, want := range []string{"connect", "disconnect", "today", "settings", "reminders", "summary", "help"} {
		if !offered[want] {
			t.Errorf("autocomplete does not offer %q", want)
		}
	}
}

func TestHelpListsWhatThePluginCanDo(t *testing.T) {
	h := newHarness(t)

	shown := h.command("/yacal help", moment(2026, 9, 24, 9, 0)).Text

	for _, want := range []string{"connect", "disconnect", "today", "settings", "reminders", "summary"} {
		if !strings.Contains(shown, want) {
			t.Errorf("help does not mention %q:\n%s", want, shown)
		}
	}
	if !strings.Contains(strings.ToLower(shown), "only ever read") {
		t.Errorf("help does not say the plugin only reads:\n%s", shown)
	}
}

func TestAnUnconfiguredPluginSaysSoAndNamesWhatIsMissing(t *testing.T) {
	h := newHarness(t)
	h.plugin.setConfiguration(&configuration{})

	for _, command := range []string{"/yacal connect", "/yacal today", "/yacal settings"} {
		shown := h.command(command, moment(2026, 9, 24, 9, 0)).Text

		if !strings.Contains(shown, "not configured") {
			t.Errorf("%s did not say the plugin is unconfigured:\n%s", command, shown)
		}
		if !strings.Contains(shown, "Client ID") || !strings.Contains(shown, "Client Secret") {
			t.Errorf("%s did not name what an administrator must supply:\n%s", command, shown)
		}
		if !strings.Contains(shown, "System Console") {
			t.Errorf("%s did not say where to supply it:\n%s", command, shown)
		}
	}
}

func TestHelpStillWorksWhileUnconfigured(t *testing.T) {
	h := newHarness(t)
	h.plugin.setConfiguration(&configuration{})

	shown := h.command("/yacal help", moment(2026, 9, 24, 9, 0)).Text

	if !strings.Contains(shown, "/yacal connect") {
		t.Errorf("help stopped working while unconfigured:\n%s", shown)
	}
	if !strings.Contains(shown, "not set up yet") {
		t.Errorf("help does not mention that the plugin is not set up:\n%s", shown)
	}
}

func TestAnUnconfiguredPluginDoesNoBackgroundWork(t *testing.T) {
	now := moment(2026, 9, 24, 9, 0)
	h := connectedHarness(t, now)
	h.calendar("events-1000001")
	h.plugin.setConfiguration(&configuration{})
	h.provider.Reset()

	h.plugin.RunPoll(now)
	h.plugin.RunDelivery(now)

	if requests := h.provider.Requests(); len(requests) != 0 {
		t.Errorf("an unconfigured plugin made %d requests", len(requests))
	}
}

func TestAnUnknownSubcommandIsAnsweredWithHelp(t *testing.T) {
	h := newHarness(t)

	shown := h.command("/yacal wibble", moment(2026, 9, 24, 9, 0)).Text

	if !strings.Contains(shown, "wibble") || !strings.Contains(shown, "/yacal connect") {
		t.Errorf("an unknown subcommand was not answered helpfully:\n%s", shown)
	}
}

func TestTheReminderLeadTimeFallsBackToTenMinutes(t *testing.T) {
	for _, configured := range []int{0, -5} {
		c := &configuration{ReminderLeadMinutes: configured}
		if c.ReminderLead() != defaultReminderLead {
			t.Errorf("a lead time of %d gave %v, want ten minutes", configured, c.ReminderLead())
		}
	}
	if got := (&configuration{ReminderLeadMinutes: 100000}).ReminderLead(); got != maxReminderLead {
		t.Errorf("an absurd lead time gave %v, want it capped at a day", got)
	}
}

func TestAConfigurationMissingAnythingIsNotUsable(t *testing.T) {
	full := configuration{ClientID: "a", ClientSecret: "b", EncryptionKey: "c"}
	if !full.IsConfigured() {
		t.Error("a complete configuration was rejected")
	}

	for _, missing := range []configuration{
		{ClientSecret: "b", EncryptionKey: "c"},
		{ClientID: "a", EncryptionKey: "c"},
		{ClientID: "a", ClientSecret: "b"},
	} {
		if missing.IsConfigured() {
			t.Errorf("an incomplete configuration was accepted: %+v", missing)
		}
		if len(missing.Missing()) == 0 {
			t.Errorf("an incomplete configuration named nothing missing: %+v", missing)
		}
	}
}
