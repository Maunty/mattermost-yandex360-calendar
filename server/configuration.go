package main

import (
	"errors"
	"time"
)

// configuration is the System Console settings, as declared in plugin.json.
//
// It is treated as immutable: OnConfigurationChange replaces it wholesale
// rather than editing it in place, so that a job reading it halfway through a
// tick sees one coherent set of values.
type configuration struct {
	ClientID            string
	ClientSecret        string
	ReminderLeadMinutes int
	EncryptionKey       string
}

// defaultReminderLead is the Server Default an administrator gets without
// doing anything, and the one every user story is written against.
const defaultReminderLead = 10 * time.Minute

// maxLeadMinutes bounds every Lead Time, chosen or Server Default, so that a
// mistyped value cannot produce Reminders hours early.
const maxLeadMinutes = 60

// errNotConfigured is what every user-facing path gets while the Yandex
// application credentials are missing. It is deliberately a plain sentence:
// the person reading it can do nothing about it except tell an administrator.
var errNotConfigured = errors.New("the Yandex Calendar plugin is not configured yet")

func (c *configuration) Clone() *configuration {
	clone := *c
	return &clone
}

// IsConfigured reports whether an administrator has supplied everything the
// plugin needs to do anything at all.
func (c *configuration) IsConfigured() bool {
	return c != nil && c.ClientID != "" && c.ClientSecret != "" && c.EncryptionKey != ""
}

// Missing names what an administrator still has to supply, so that the message
// a user sees says what is wrong rather than that something is.
func (c *configuration) Missing() []string {
	var missing []string
	if c == nil || c.ClientID == "" {
		missing = append(missing, "the Yandex Client ID")
	}
	if c == nil || c.ClientSecret == "" {
		missing = append(missing, "the Yandex Client Secret")
	}
	if c == nil || c.EncryptionKey == "" {
		missing = append(missing, "the at-rest encryption key")
	}
	return missing
}

// ServerDefaultLead is the Lead Time for everybody who has not chosen their
// own. An empty or zero setting means the default, and it is capped at an
// hour.
func (c *configuration) ServerDefaultLead() time.Duration {
	if c == nil || c.ReminderLeadMinutes <= 0 {
		return defaultReminderLead
	}
	return time.Duration(min(c.ReminderLeadMinutes, maxLeadMinutes)) * time.Minute
}

// LeadTime is one person's Lead Time, and whether they chose it. Delivery and
// everything that tells the person about it ask here, so they cannot disagree.
func (c *configuration) LeadTime(settings Settings) (lead time.Duration, chosen bool) {
	if settings.LeadMinutes != nil {
		return time.Duration(*settings.LeadMinutes) * time.Minute, true
	}
	return c.ServerDefaultLead(), false
}

// getConfiguration returns the active configuration, never nil.
func (p *Plugin) getConfiguration() *configuration {
	p.configurationLock.RLock()
	defer p.configurationLock.RUnlock()

	if p.configuration == nil {
		return &configuration{}
	}
	return p.configuration
}

func (p *Plugin) setConfiguration(configuration *configuration) {
	p.configurationLock.Lock()
	defer p.configurationLock.Unlock()

	p.configuration = configuration
}

// OnConfigurationChange reloads the settings an administrator just saved.
func (p *Plugin) OnConfigurationChange() error {
	var configuration = new(configuration)

	if err := p.API.LoadPluginConfiguration(configuration); err != nil {
		return err
	}

	p.setConfiguration(configuration)
	return nil
}
