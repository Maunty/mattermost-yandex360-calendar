package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/mattermost/mattermost/server/public/pluginapi"

	"github.com/maunty/mattermost-yandex360-calendar/server/internal/calendar"
)

// Key prefixes. Everything the plugin stores is per user except the index,
// which exists so that a job can enumerate connected people without reading
// every key the plugin owns.
const (
	connectionKeyPrefix = "conn_"
	settingsKeyPrefix   = "set_"
	syncKeyPrefix       = "sync_"
	oauthStateKeyPrefix = "oauth_"
	reminderKeyPrefix   = "sent_r_"
	summaryKeyPrefix    = "sent_d_"
	connectedIndexKey   = "idx_connected_users"
)

// errNotConnected is the absence of a Connection, which is an ordinary state
// and not a failure.
var errNotConnected = errors.New("no Yandex Calendar is connected")

// Connection is the link between one Mattermost user and the Yandex account
// the plugin reads for them. It is stored encrypted, because it holds tokens
// that open somebody's calendar.
type Connection struct {
	MattermostUserID string
	YandexLogin      string
	YandexUserID     string

	AccessToken  string
	RefreshToken string
	ExpiresAt    time.Time

	ConnectedAt time.Time

	// CalendarHome is where this account's CalDAV collections were found. It
	// is used only by the CalDAV fallback (ADR 0002), and it is always a value
	// the server returned, never one built from a login.
	CalendarHome string

	// Active is false once the provider has refused the credential twice. An
	// inactive Connection sends nothing and polls nothing until the person
	// reconnects.
	Active bool
	// AuthFailures counts consecutive refusals. One is not enough to retire a
	// Connection; a provider outage would otherwise disconnect everybody.
	AuthFailures int
	// InactiveNoticeSent stops the "please reconnect" message repeating.
	InactiveNoticeSent bool
}

// Settings are one person's choices. The zero value is the default behaviour,
// so a person who has never touched their settings needs no stored record.
type Settings struct {
	RemindersDisabled    bool
	DailySummaryDisabled bool
	// DailySummaryTime is "HH:MM" in the person's own timezone. Empty means
	// the default.
	DailySummaryTime string
}

const defaultSummaryTime = "08:00"

func (s Settings) RemindersEnabled() bool    { return !s.RemindersDisabled }
func (s Settings) DailySummaryEnabled() bool { return !s.DailySummaryDisabled }

// SummaryTime is when the Daily Summary is due, as an hour and minute in the
// person's own timezone.
func (s Settings) SummaryTime() (int, int) {
	hour, minute, err := parseClockTime(s.DailySummaryTime)
	if err != nil {
		hour, minute, _ = parseClockTime(defaultSummaryTime)
	}
	return hour, minute
}

// SummaryTimeText is the delivery time as a person would write it.
func (s Settings) SummaryTimeText() string {
	hour, minute := s.SummaryTime()
	return fmt.Sprintf("%02d:%02d", hour, minute)
}

func parseClockTime(value string) (int, int, error) {
	t, err := time.Parse("15:04", value)
	if err != nil {
		return 0, 0, fmt.Errorf("%q is not a time of day in 24-hour HH:MM form", value)
	}
	return t.Hour(), t.Minute(), nil
}

// SyncState is what the last poll learned. It holds no secrets, so unlike the
// Connection it is stored in the clear.
type SyncState struct {
	// Occurrences is the person's cached forward window, already expanded.
	// Reminders fire from this, at exact times, rather than from the poll.
	Occurrences []calendar.Occurrence
	// CachedThrough is how far forward Occurrences is trustworthy.
	CachedThrough time.Time
	// NextPollAt spreads polling out, because Events cluster on the hour and
	// without it every person's Reminder would come due in the same second.
	NextPollAt time.Time
	// LastSuccessAt lets a person tell an empty day from a broken Connection.
	LastSuccessAt time.Time
}

// Store is every piece of state the plugin keeps, and the only place that
// knows how it is encoded.
type Store struct {
	kv            *pluginapi.KVService
	encryptionKey func() string
}

func NewStore(kv *pluginapi.KVService, encryptionKey func() string) *Store {
	return &Store{kv: kv, encryptionKey: encryptionKey}
}

// Connection reads one person's Connection.
func (s *Store) Connection(userID string) (*Connection, error) {
	var sealed string
	if err := s.kv.Get(connectionKeyPrefix+userID, &sealed); err != nil {
		return nil, err
	}
	if sealed == "" {
		return nil, errNotConnected
	}

	plaintext, err := decrypt(s.encryptionKey(), sealed)
	if err != nil {
		return nil, err
	}

	var connection Connection
	if err := json.Unmarshal(plaintext, &connection); err != nil {
		return nil, err
	}
	return &connection, nil
}

// SaveConnection stores a Connection and keeps the index of connected people
// in step with it.
func (s *Store) SaveConnection(connection *Connection) error {
	plaintext, err := json.Marshal(connection)
	if err != nil {
		return err
	}
	sealed, err := encrypt(s.encryptionKey(), plaintext)
	if err != nil {
		return err
	}
	if _, err := s.kv.Set(connectionKeyPrefix+connection.MattermostUserID, sealed); err != nil {
		return err
	}
	return s.indexConnected(connection.MattermostUserID, true)
}

// DeleteConnection forgets a person's tokens and everything derived from them.
// Disconnecting has to mean what it says.
func (s *Store) DeleteConnection(userID string) error {
	if err := s.kv.Delete(connectionKeyPrefix + userID); err != nil {
		return err
	}
	if err := s.kv.Delete(syncKeyPrefix + userID); err != nil {
		return err
	}
	return s.indexConnected(userID, false)
}

// ConnectedUserIDs is the index a job enumerates. It exists so that polling
// does not have to read every key the plugin owns.
func (s *Store) ConnectedUserIDs() ([]string, error) {
	var userIDs []string
	if err := s.kv.Get(connectedIndexKey, &userIDs); err != nil {
		return nil, err
	}
	return userIDs, nil
}

func (s *Store) indexConnected(userID string, connected bool) error {
	return s.kv.SetAtomicWithRetries(connectedIndexKey, func(old []byte) (any, error) {
		var userIDs []string
		if len(old) > 0 {
			if err := json.Unmarshal(old, &userIDs); err != nil {
				return nil, err
			}
		}

		next := make([]string, 0, len(userIDs)+1)
		for _, existing := range userIDs {
			if existing != userID {
				next = append(next, existing)
			}
		}
		if connected {
			next = append(next, userID)
		}
		return next, nil
	})
}

func (s *Store) Settings(userID string) (Settings, error) {
	var settings Settings
	if err := s.kv.Get(settingsKeyPrefix+userID, &settings); err != nil {
		return Settings{}, err
	}
	return settings, nil
}

func (s *Store) SaveSettings(userID string, settings Settings) error {
	_, err := s.kv.Set(settingsKeyPrefix+userID, settings)
	return err
}

func (s *Store) DeleteSettings(userID string) error {
	return s.kv.Delete(settingsKeyPrefix + userID)
}

// SyncState reads what the last poll left behind. A person who has never been
// polled has an empty one, which is not an error.
func (s *Store) SyncState(userID string) (*SyncState, error) {
	state := &SyncState{}
	if err := s.kv.Get(syncKeyPrefix+userID, state); err != nil {
		return nil, err
	}
	return state, nil
}

func (s *Store) SaveSyncState(userID string, state *SyncState) error {
	_, err := s.kv.Set(syncKeyPrefix+userID, state)
	return err
}

// StoreOAuthState remembers a one-time value for the length of a consent flow.
// It expires on its own, so an abandoned flow leaves nothing behind.
func (s *Store) StoreOAuthState(state, userID string, ttl time.Duration) error {
	_, err := s.kv.Set(oauthStateKeyPrefix+state, userID, pluginapi.SetExpiry(ttl))
	return err
}

// ConsumeOAuthState reads a one-time value and deletes it in the same breath.
// A state that is missing, expired or already used yields nothing, which is
// what stops a callback from being replayed or forged.
func (s *Store) ConsumeOAuthState(state string) (string, error) {
	if state == "" {
		return "", errors.New("the sign-in could not be verified")
	}

	key := oauthStateKeyPrefix + state
	var userID string
	if err := s.kv.Get(key, &userID); err != nil {
		return "", err
	}
	if userID == "" {
		return "", errors.New("the sign-in could not be verified")
	}

	// Deleting atomically against the value just read is what makes this
	// one-time: a second caller racing for the same state finds nothing.
	deleted, err := s.kv.Set(key, nil, pluginapi.SetAtomic(userID))
	if err != nil {
		return "", err
	}
	if !deleted {
		return "", errors.New("the sign-in could not be verified")
	}
	return userID, nil
}

// MarkReminderSent records a Reminder as sent and reports whether this caller
// is the one that got to send it. It is atomic, so two nodes racing over the
// same occurrence produce one message, not two.
func (s *Store) MarkReminderSent(userID, instanceKey string, ttl time.Duration) (bool, error) {
	return s.markSent(reminderKeyPrefix+userID+"_"+shortHash(instanceKey), ttl)
}

// MarkSummarySent records that a person's Daily Summary has gone out for a
// given day, so that a restart does not send it again.
func (s *Store) MarkSummarySent(userID string, day time.Time, ttl time.Duration) (bool, error) {
	return s.markSent(summaryKeyPrefix+userID+"_"+day.Format("20060102"), ttl)
}

func (s *Store) markSent(key string, ttl time.Duration) (bool, error) {
	// SetAtomic against no previous value means "write this only if nothing
	// is there", which is exactly once-only semantics.
	return s.kv.Set(key, time.Now().Unix(), pluginapi.SetAtomic(nil), pluginapi.SetExpiry(ttl))
}

func shortHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:8])
}
