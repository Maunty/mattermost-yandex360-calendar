# Research: Mattermost + Yandex Calendar integration

**Date:** 2026-09-24. **Researcher:** primary-source sweep of developers.mattermost.com, the `mattermost/mattermost-plugin-*` repo sources (cloned at HEAD), yandex.ru/dev + Yandex 360 support docs, and RFC 4791.

**Most important finding:** Yandex publishes **no REST calendar API**. Calendar is absent from the Yandex developer index ([yandex.ru/dev/index](https://yandex.ru/dev/index/)) and absent from the Yandex 360 API reference's list of services ([yandex.ru/dev/api360/doc/ru/ref/index.html](https://yandex.ru/dev/api360/doc/ru/ref/index.html) lists 20 sections — mail, departments, domains, contacts, groups, organizations, employees, security, service applications — and no Календарь). The only programmatic access Yandex documents is **CalDAV at `https://caldav.yandex.ru/`** ([service applications doc](https://yandex.ru/support/yandex-360/business/admin/en/security-service-applications)). This inverts the architecture relative to every existing Mattermost calendar plugin: there is no `Events.Watch`-style subscription, so **the plugin must poll**, and the credential is either a per-user **app-specific password** ([app passwords doc](https://yandex.ru/support/id/ru/authorization/app-passwords)) or a **Yandex 360 for Business service-application OAuth token** that only an organization owner can provision. Both paths have hard consequences for a multi-user server-side plugin, detailed below.

---

## Open questions for the build

These are the decisions this research surfaces but cannot settle.

1. **Credential model: app password vs. service application.** These are not variants of one design, they are two different products.
   - *App password path:* every user individually creates a CalDAV app password in Yandex ID and pastes it into the plugin. Works for personal `@yandex.ru` accounts and for orgs. But the plugin then stores a **long-lived, non-expiring, non-scoped password** that grants full CalDAV access — not a revocable-by-us OAuth token. Mattermost's KV store is the only place to put it, so the AES-GCM `EncryptionKey` pattern from the Google plugin (see Prior art) becomes mandatory, not optional. Open: is asking every user to paste a password acceptable UX/security posture for this deployment?
   - *Service-application path:* one OAuth app, org owner registers it, plugin mints a 1-hour per-user token via token-exchange, never stores a user secret. Much better security story. But it requires **Yandex 360 for Business on a Основной/Продвинутый/Корпоративный tariff**, is **creatable only by the organization owner**, is capped at **20 service applications per org**, and carries a contractual user-notification obligation ([source](https://yandex.ru/support/yandex-360/business/admin/en/security-service-applications)). Open: is the target deployment a Yandex 360 org at all, or individuals on personal accounts?
   - Open: do we support **both**, behind a config switch? That doubles the auth surface but is the only way to serve both audiences.

2. **Is there an OAuth consent flow at all, or only credential entry?** The service-application flow is **admin-to-user impersonation** (`subject_token` = the user's uid or email), not user consent. There is no documented Yandex authorization-code flow that yields a calendar-capable token for an individual user (see Gaps §1). If we take the service-app path, the plugin's `/connect` command is *not* an OAuth redirect — it is a no-op or an admin-side mapping. That deletes the entire `oauth2connect` subsystem the prior-art plugins are built around. Decide early: this changes the shape of the whole connect flow.

3. **Polling interval and its cost.** There is no push. The mscalendar engine polls every **5 minutes** for status sync (`StatusSyncJobInterval`, `calendar/engine/availability.go:24`) and every **15 minutes** for daily summaries (`DailySummaryJobInterval`, `calendar/engine/daily_summary.go:24`). Against CalDAV that means N users × a `REPORT calendar-query` every 5 minutes. Open: what interval? Does Yandex rate-limit CalDAV (undocumented — see Gaps §4)? Can we cut cost with `getetag` + `calendar-multiget` (RFC 4791 §7.9, §7.10) or, if Yandex supports it, `sync-collection` (RFC 6578) — **support unverified**, see Gaps §3. A probe against a real account is required before fixing the interval.

4. **Build on mscalendar as a library, or greenfield?** The Google Calendar plugin is not a standalone plugin — it imports `github.com/mattermost/mattermost-plugin-mscalendar v1.6.1` and supplies ~15 files implementing `remote.Remote` + `remote.Client` (`gcal/server/main.go:9-13`). We could do the same and get jobs, KV, settings panel, welcome flow, commands, and rendering for free. But `remote.Client` is a **Graph/REST-shaped interface** (`GetEvent(remoteUserID, eventID)`, `CreateMySubscription`, `GetMailboxSettings`) that maps awkwardly onto CalDAV's collection/ETag/iCalendar model. Open: how many of the 30-odd interface methods can honestly return `remote.ErrNotImplemented` before the reuse stops paying for itself?

5. **Timezone source of truth.** mscalendar gets the timezone from the *provider* (`GetMailboxSettings` → `settings.TimeZone`, `calendar/engine/user.go:90-104`); Google serves it from a calendar-settings scope. CalDAV has no equivalent settings endpoint. Open: read `VTIMEZONE` out of the returned iCalendar, or fall back to the Mattermost user's own timezone preference? These disagree, and the daily-summary post time depends on getting it right.

6. **Minimum server version.** Both first-party calendar plugins currently declare `"min_server_version": "12.0.0"` (`mscal/plugin.json`, `gcal/plugin.json`), but **v12.0 ships October 2026** — next month ([Mattermost deprecation blog](https://mattermost.com/blog/what-will-be-deprecated-in-mattermost-v12-0-october-2026/)); the current ESR is v11.7, supported to 2027-05-15. Open: do we target v12 (matching first-party, but excluding every ESR deployment today) or v9/v11 (wider reach, older API surface)?

7. **Single-user convenience vs. org-wide deployment.** Related to (1) but distinct: does the plugin need a *shared/room calendar* mode (one credential, one channel, many readers), which sidesteps per-user credentials entirely? Yandex documents calendar sharing with view/create/edit permission levels ([sharing doc](https://yandex.com/support/yandex-360/business/calendar/en/sharing)), so a single service account subscribed to shared calendars is technically viable and drastically simpler.

8. **Write support, or read-only?** CalDAV `PUT` of an `.ics` is documented and works ([service applications doc](https://yandex.ru/support/yandex-360/business/admin/en/security-service-applications)), and Yandex supports the custom property `X-TELEMOST-REQUIRED` to auto-generate a Telemost conference link on event creation (same source). But RSVP/accept/decline over CalDAV means rewriting `PARTSTAT` on the attendee's copy — considerably harder than a REST `accept` call. Open: ship read-only + reminders first?

---

## Mattermost plugin API

### 1. Architecture and supported model

- Two plugin surfaces, both current: **server plugins in Go**, "launched and managed as RPC services" by the Mattermost server, and **webapp plugins** in JS/React that register UI components without forking the server ([plugin overview](https://developers.mattermost.com/integrate/plugins/)). Mobile plugin extension exists but is documented as limited (same page).
- The RPC transport is `hashicorp/go-plugin`: the server executes the plugin binary as a subprocess and speaks RPC to it. In practice you never touch it directly — you embed `plugin.MattermostPlugin` and call `plugin.ClientMain(...)`. Confirmed in real code: `gcal/server/main.go:26` calls `mattermostplugin.ClientMain(...)` with `mattermostplugin "github.com/mattermost/mattermost/server/public/plugin"`.
- **Minimum possible `min_server_version` is 5.6** ([manifest reference](https://developers.mattermost.com/integrate/plugins/manifest-reference/)). What first-party calendar plugins *actually* declare is `12.0.0` (both manifests, verified at repo HEAD 2026-09-16 / 2026-09-17).
- The Go module both plugins depend on is `github.com/mattermost/mattermost/server/public` (mscalendar pins `v0.4.4`, gcal pins `v0.3.0`; `mscal/go.mod`, `gcal/go.mod`). Go toolchain at HEAD: `go 1.26.7` (mscalendar), `go 1.25.8` (gcal).

### 2. Manifest and bundle layout

Full `plugin.json` schema is at the [manifest reference](https://developers.mattermost.com/integrate/plugins/manifest-reference/). Fields that matter here:

| Field | Note |
|---|---|
| `id` | `^[a-zA-Z0-9-_.]+$`, 3–190 chars, reverse-DNS recommended |
| `min_server_version` | ≥ 5.6 |
| `server.executables` | map of `os-arch` → binary path. Both plugins ship 5: `darwin-amd64`, `darwin-arm64`, `linux-amd64`, `linux-arm64`, `windows-amd64` |
| `webapp.bundle_path` | e.g. `webapp/dist/main.js` |
| `settings_schema.settings[]` | types: `bool`, `text`, `longtext`, `number`, `dropdown`, `radio`, `generated`, `username`, `custom`; `secret: true` sanitizes the value in the System Console and API |
| `icon_path` | SVG only, bitmaps unsupported |
| `props` | arbitrary map for inter-plugin communication |

The `generated` type is how the Google plugin provisions its encryption key: `{"key":"EncryptionKey","type":"generated","secret":true}` (`gcal/plugin.json`), with help text warning that regenerating it loses all stored user auth.

**Bundle layout** — read from the authoritative `bundle` target in the starter template's `Makefile:250-274` (github.com/mattermost/mattermost-plugin-starter-template):

```
dist/<plugin-id>.tar.gz
└── <plugin-id>/
    ├── plugin.json          # written by ./build/bin/manifest dist
    ├── assets/              # copied if assets/ exists
    ├── public/              # copied if public/ exists (statically served)
    ├── server/dist/         # the per-platform binaries
    └── webapp/dist/         # the JS bundle
```

Built with `tar -cvzf` from inside `dist/`, so the archive root is a single directory named for the plugin id. On macOS the target adds `--disable-copyfile` to keep AppleDouble files out.

### 3. Hooks and API methods relevant to a calendar integration

**Hooks actually implemented by a production calendar plugin** (`mscal/calendar/plugin/plugin.go`) — this is the minimal real-world set:

| Hook | Line | Use |
|---|---|---|
| `OnActivate() error` | `:67` | load config, check provider config, load templates, register slash command, init store |
| `OnDeactivate() error` | `:126` | close telemetry client and **close the job manager** (releases cluster mutexes) |
| `OnConfigurationChange() error` | `:144` | rebuild env, re-create store, re-encrypt user data on key change, (re)create the job manager |
| `ExecuteCommand(*plugin.Context, *model.CommandArgs)` | `:253` | slash command dispatch |
| `ServeHTTP(*plugin.Context, http.ResponseWriter, *http.Request)` | `:293` | all HTTP: OAuth redirect, webhooks, post actions, internal API |

**API methods** ([server reference](https://developers.mattermost.com/integrate/reference/server/server-reference/)):

- **KV store.** `KVSet(key string, value []byte) *model.AppError`; `KVSetWithExpiry(key string, value []byte, expireInSeconds int64)`; `KVSetWithOptions(key string, value []byte, options model.PluginKVSetOptions) (bool, *model.AppError)`; `KVGet`, `KVDelete`, `KVList(page, perPage int)`, `KVCompareAndSet(key string, oldValue, newValue []byte) (bool, *model.AppError)`. The docs warn that the prefix `mmi_` is reserved for helper/internal use.
  - **Limits.** The docs page does not state them. The server source does: `KeyValueKeyMaxRunes = 150` and `KeyValuePluginIdMaxRunes = 190`, enforced in `IsValid()` ([server/public/model/plugin_key_value.go](https://raw.githubusercontent.com/mattermost/mattermost/master/server/public/model/plugin_key_value.go)). **No maximum value size is validated in that model** — value size is bounded by the database column, not by an enforced constant (see Gaps §5).
  - The 150-rune key limit is why mscalendar wraps every store in a `hashedKeyStore` that MD5s the logical key and prefixes it: `fmt.Sprintf("%s%x", prefix, h.Sum(nil))` (`mscal/calendar/utils/kvstore/hashed_key.go:57-66`). Copy this; do not key KV directly on arbitrary strings.
- **Bots.** `CreateBot(bot *model.Bot)` and `EnsureBotUser(bot *model.Bot) (string, error)` (the latter "updates the bot if it exists, otherwise creates it", v7.1+).
- **Slash commands.** `RegisterCommand(command *model.Command) error`; fulfilled via the `ExecuteCommand` hook.
- **Posts.** `CreatePost`, `SendEphemeralPost(userID string, post *model.Post) *model.Post`, `UpdateEphemeralPost` (marked EXPERIMENTAL), `GetDirectChannel(userId1, userId2)` which creates the DM channel if absent.
- **Users/prefs.** `GetUser(userID)`, `GetPreferencesForUser(userID) ([]model.Preference, *model.AppError)`.
- **Websocket.** `PublishWebSocketEvent(event string, payload map[string]any, broadcast *model.WebsocketBroadcast)`; the event name is prefixed `custom_<pluginid>_`.
- **Cluster.** `PublishPluginClusterEvent(...)` broadcasts to other instances of the same plugin in the cluster.

**Scheduled/background jobs.** There is no cron hook. The supported mechanism is `cluster.Schedule` from `github.com/mattermost/mattermost/server/public/pluginapi/cluster`:

```go
func Schedule(pluginAPI JobPluginAPI, key string, nextWaitInterval NextWaitInterval, callback func()) (*Job, error)
type NextWaitInterval func(now time.Time, metadata JobMetadata) time.Duration
```

It guarantees **only one plugin instance executes the callback concurrently** across a cluster, by taking a distributed `Mutex` on `cronPrefix + key` before invoking the callback and persisting `LastFinished` in the KV store so all nodes schedule off the same timestamp ([pluginapi/cluster/job.go](https://raw.githubusercontent.com/mattermost/mattermost/master/server/public/pluginapi/cluster/job.go)). This is exactly the primitive a polling plugin needs, and mscalendar wraps it in a `JobManager` (see Prior art).

**Interactive posts.** Clicking a message-attachment button POSTs JSON to the action's `integration.url` with `user_id`, `post_id`, `channel_id`, `team_id` and a caller-defined `context` map; **relative URLs are accepted, which is what makes plugin-handled actions simple**; the response may contain `update` (rewrite the post) and/or `ephemeral_text` ([interactive messages doc](https://developers.mattermost.com/integrate/plugins/interactive-messages/)). Custom error responses are noted as available in v10.5+. That doc also states **Mattermost Blocks are now the recommended way to build interactive integration posts**, with attachment-based buttons described as legacy — but the shipping calendar plugins still use `model.MessageAttachment` + `model.PostAction` (see Prior art), and I could not retrieve a Blocks reference page (Gaps §6).

**OAuth/HTTP.** Everything goes through the single `ServeHTTP` hook; mscalendar mounts a `gorilla/mux` router under it and registers subrouters for `/oauth2`, `/notification/event`, `/postaction/*`, `/autocomplete/users`, and an internal API (`mscal/calendar/api/api.go:20-50`).

**Per-user settings.** Not a platform feature — the prior-art plugins implement it themselves as a bot-posted "settings panel" of interactive attachments backed by KV (`mscal/calendar/utils/settingspanel/`, wired in `mscal/calendar/engine/settings.go:29-70`).

---

## Yandex Calendar access

### 1. REST API: no

Stated plainly, with the pages that establish it:

- **Yandex's developer index does not list Calendar.** [yandex.ru/dev/index](https://yandex.ru/dev/index/) enumerates Maps, Translate, Disk, Metrica, Direct, Telemost, Yandex 360 for Business, Toloka, Weather, etc. Calendar is not among them. `https://yandex.ru/dev/calendar/` returns **HTTP 404**.
- **The Yandex 360 API reference does not contain a Calendar service.** [yandex.ru/dev/api360/doc/ru/ref/index.html](https://yandex.ru/dev/api360/doc/ru/ref/index.html) lists exactly 20 sections: antispam list, shared/delegated mailboxes, allowed & blocked senders, mail processing rules, employee mail settings, departments, DNS, domains, external contacts, groups, groups v2, organizations, employees, audit log, 2FA (v1 and v2), password management, authorization settings, external-OAuth restrictions, and service applications. The API is described there as managing "the structure and settings of organizations" ([intro](https://yandex.ru/dev/api360/doc/ru/)).
- **Where Yandex does document calendar programmatic access, it documents CalDAV.** The service-applications page says, of Yandex Calendar: OAuth tokens let you "interact with Yandex Calendar of users via the CalDAV protocol", and its worked examples are `PUT`/`GET` of `.ics` resources ([source](https://yandex.ru/support/yandex-360/business/admin/en/security-service-applications)).

**Conclusion: CalDAV is the only documented option.** Treat any claim of a Yandex Calendar REST API as unsubstantiated.

### 2. CalDAV endpoint and authentication

- **Endpoint: `https://caldav.yandex.ru/`.** Given in both the end-user sync docs ([desktop sync](https://yandex.ru/support/calendar/sync/sync-desktop.html)) and the service-applications developer doc.
  - *Discrepancy to flag:* DAVx⁵'s compatibility page gives the base URL as `https://yandex.ru` or `https://calendar.yandex.ru` for CalDAV ([davx5.com/tested-with/yandex](https://www.davx5.com/tested-with/yandex)). That is a third-party page and contradicts Yandex's own docs. **Prefer `caldav.yandex.ru`**; verify empirically.
- **Resource path shape**, from Yandex's own example: `https://caldav.yandex.ru/calendars/<user_email>/events-default/<event_uid>.ics`, with `Content-type: text/ics` on `PUT` ([service applications doc](https://yandex.ru/support/yandex-360/business/admin/en/security-service-applications)). Note the default collection is literally named `events-default`. Per RFC 4791 you should still discover it via the principal's `CALDAV:calendar-home-set` rather than hardcoding ([RFC 4791](https://datatracker.ietf.org/doc/html/rfc4791)).
- **Auth option A — app-specific password.** For ordinary end users this is what Yandex documents: create an app password in Yandex ID → Security → App passwords, selecting the **Calendar (CalDAV)** category, then use it in place of the account password in the CalDAV client ([desktop sync](https://yandex.ru/support/calendar/sync/sync-desktop.html), [app passwords](https://yandex.ru/support/id/ru/authorization/app-passwords)). App passwords are **scoped per service** — the app-passwords page states a password created for mail will not grant WebDAV access to Disk — and the categories include mail clients, WebDAV (Disk), CardDAV (contacts), and **CalDAV (Calendar)**. The password is displayed once and must be stored by the client. Yandex's desktop sync page also advises waiting 2–3 hours after creating the password before syncing.
- **Auth option B — OAuth token over CalDAV, via service applications.** Yandex documents `Authorization: OAuth <oauth_token>` against `caldav.yandex.ru` — note this is Yandex's `OAuth` auth scheme, **not** `Bearer` ([service applications doc](https://yandex.ru/support/yandex-360/business/admin/en/security-service-applications)). **So yes: OAuth tokens do work over Yandex CalDAV** — but the only flow Yandex documents for obtaining a calendar-capable token is the service-application token exchange (below).
- **Implication for a multi-user server-side plugin.** Either way the plugin holds a credential per user. With app passwords that credential is long-lived and effectively a password → it must be encrypted at rest in KV (AES-GCM, as the Google plugin does) and the plugin becomes a high-value target; revocation is only possible by the user, in Yandex ID. With the service-application flow the plugin stores only the *app's* client_id/secret and mints 1-hour user tokens on demand — strictly better, but gated on org ownership and a paid tariff.

### 3. Yandex OAuth (Yandex ID)

- **Registration** happens at [oauth.yandex.ru](https://oauth.yandex.ru/), via the [register-client doc](https://yandex.ru/dev/id/doc/ru/register-client). You pick an app type at registration and **cannot change it afterwards**: an *authorization app* (authenticate users on your service) or an *API-access app* (developer-only data access, no user involvement). Authorization apps may request **at most 3 permission groups**; a group is the prefix before the colon (`login:info` → group `login`). API-access apps cannot use the `login` group.
- **Token flow / lifetime.** Two token classes ([OAuth intro](https://yandex.ru/dev/id/doc/en/concepts/ya-oauth-intro)): *renewable* tokens that expire after some months but are renewed on each login with that token (minimum lifetime is shown at app registration), and *restricted* tokens that expire per the lifetime of the access rights granted, shortest right winning. The docs describe renewal-on-use rather than a conventional refresh-token exchange; a "Обновление токена" (token refresh) page exists in the doc index.
- **Token exchange (the service-application flow)** ([service applications doc](https://yandex.ru/support/yandex-360/business/admin/en/security-service-applications)):
  ```
  POST https://oauth.yandex.ru/token
    grant_type        = urn:ietf:params:oauth:grant-type:token-exchange
    client_id         = <service app ClientID>
    client_secret     = <service app secret>
    subject_token     = <target user's uid or email>
    subject_token_type= urn:yandex:params:oauth:token-type:uid
                      | urn:yandex:params:oauth:token-type:email
  ```
  The returned temporary token is **valid for 1 hour**. Note there is no user consent step — this is org-admin impersonation.
- **Calendar scope: not established.** This is the sharpest documentation gap. See Gaps §1.

### 4. Yandex 360 admin/organization APIs

- The Yandex 360 API is explicitly an **organization-management** API: it "manages the structure and settings of organizations" over REST ([intro](https://yandex.ru/dev/api360/doc/ru/)), covering employees, departments, groups, domains, mail settings, and security. Nothing user-calendar-facing.
- **Service applications are org-owner-only.** The API reference page for the service says "Доступен только владельцу организации" (available only to the organization owner) and requires an appropriate business tariff ([ServiceApplicationsService](https://yandex.ru/dev/api360/doc/ru/ref/ServiceApplicationsService/)). Endpoints: Get / Create / Delete / Activate / Deactivate. Managing them needs `ya360_security:service_applications_read` and `ya360_security:service_applications_write` on a separate "master" OAuth app.
- **Limits and tariffs** ([service applications doc](https://yandex.ru/support/yandex-360/business/admin/en/security-service-applications)): max **20 service applications per organization**; available on **Основной / Продвинутый / all Корпоративный** plans; downgrading to the minimum tier disables management and deletes the apps after one month; the org must notify users per the service agreement.
- Other resources reachable through a service app, for comparison: Yandex Disk via REST at `https://cloud-api.yandex.net/v1/disk` (`cloud_api:disk.read`, `cloud_api:disk.write`, `cloud_api:disk.info`), and Mail via IMAP/SMTP OAuth at `imap.yandex.com:993`. Calendar is the only one of the three with **no REST option**.

### 5. Push / change notifications

**No push. The plugin must poll.** Three independent facts establish this:

1. **CalDAV itself defines no notification mechanism.** RFC 4791 has no push; it assumes client-initiated polling and offers ETags for conditional detection of change, explicitly warning clients that "calendar data on the server [may] change between the time of last synchronization and when attempting an update" ([RFC 4791](https://datatracker.ietf.org/doc/html/rfc4791)).
2. **Yandex documents no webhook, subscription, or channel mechanism for Calendar** anywhere I could find — not in the service-applications doc, not in the 360 API reference, not in the developer index.
3. The only "push notifications" Yandex documents for Calendar are **end-user reminders** delivered to the Yandex app/browser ([mobile sync doc](https://yandex.com/support/calendar/common/sync/sync-mobile.html)) — a user-facing feature, not a developer integration point.

Efficiency levers available within polling, in order of preference: `sync-collection` (RFC 6578) if Yandex supports it (**unverified** — Gaps §3); otherwise `REPORT calendar-query` with a `time-range` filter returning only `getetag`, then `calendar-multiget` for the changed hrefs (RFC 4791 §7.8–7.10).

---

## Prior art

Both repos cloned at HEAD and read directly. `mattermost-plugin-mscalendar` @ `30f208f` (2026-09-16); `mattermost-plugin-google-calendar` @ `9aab035` (2026-09-17). Both are actively maintained.

### The single most reusable fact: mscalendar is a framework, not just a plugin

`gcal/go.mod` requires `github.com/mattermost/mattermost-plugin-mscalendar v1.6.1`. The entire Google Calendar plugin's server entrypoint is 39 lines (`gcal/server/main.go`):

```go
config.Provider = gcal.GetGcalProviderConfig()
mattermostplugin.ClientMain(plugin.NewWithEnv(engine.Env{ Config: &config.Config{...}, Dependencies: &engine.Dependencies{} }))
```

Everything provider-specific lives in **15 files** under `gcal/gcal/` (calendar.go, client.go, event.go, gcal.go, mailbox.go, meeting_times.go, notifications.go, remote.go, schedule.go, subscription.go, user.go, utils.go, views.go, webhook.go). A Yandex plugin could plausibly be the same shape.

**The provider seam** is two interfaces in `mscal/calendar/remote/`:

- `remote.Remote` (`remote/remote.go:22-28`) — 5 methods: `MakeUserClient`, `MakeSuperuserClient`, `NewOAuth2Config`, `HandleWebhook`, `CheckConfiguration`. Providers register themselves into `remote.Makers` (a `map[string]func(*config.Config, bot.Logger) Remote`) from an `init()` — see `gcal/gcal/remote.go:28-30`.
- `remote.Client` (`remote/client.go:11-58`) — composed of `Core` (`GetMe`), `Calendars` (`GetEvent`, `GetCalendars`, `GetDefaultCalendarView`, `DoBatchViewCalendarRequests`, `GetMailboxSettings`), `Events` (`CreateEvent`, `AcceptEvent`, `DeclineEvent`, `TentativelyAcceptEvent`, `GetEventsBetweenDates`), `Subscriptions` (5 methods), `Utils`, and an explicitly-named `Unsupported` group. The package ships `ErrSuperUserClientNotSupported` and `ErrNotImplemented` sentinels (`remote/remote.go:17-20`) — **the framework expects providers to not implement everything**, which is the escape hatch a CalDAV provider needs.

**Provider feature flags** (`mscal/calendar/config/config.go:29-51`) let a provider declare what it can do:

```go
type ProviderFeatures struct {
    EncryptedStore       bool
    EventNotifications   bool
    EnableExperimentalUI bool
    ForceOAuth2Consent   bool  // "for providers that require user consent to issue a refresh token (e.g. Google OAuth2)"
}
```

Google sets `{EncryptedStore: true, EventNotifications: false, ForceOAuth2Consent: true}` (`gcal/gcal/gcal.go:28-32`). **`EventNotifications: false` is the interesting one** — even though gcal *does* implement Google watch channels (`gcal/gcal/subscription.go`), the flag is false, so the notification processor is never constructed (`mscal/calendar/plugin/plugin.go:228-234`) and the per-user "notifications" setting is hidden (`mscal/calendar/engine/settings.go:61`, `engine/welcome_flow.go:95`). In effect **the shipping Google plugin runs on polling alone** — precisely the posture a Yandex/CalDAV provider needs, and it is a supported, first-party-exercised configuration. (Caveat: the renew job is added unconditionally at `plugin.go:245-249`, so it will still tick; it iterates the user index and calls `RenewMyEventSubscription`.)

### Per-user token storage

- `store.User` holds `OAuth2Token *oauth2.Token` alongside Mattermost identity, remote user, per-user `Settings`, `ActiveEvents []string`, and `ChannelEvents map[string]string` (`mscal/calendar/store/user_store.go:78-91`).
- Storage is layered KV wrappers (`mscal/calendar/store/store.go:61-79`):
  ```go
  basicKV  := kvstore.NewPluginStore(api)
  oauth2KV := kvstore.NewHashedKeyStore(kvstore.NewOneTimePluginStore(api, OAuth2KeyExpiration), OAuth2KeyPrefix)
  user2KV  := kvstore.NewHashedKeyStore(basicKV, UserKeyPrefix)
  if enableEncryption { oauth2KV = kvstore.NewEncryptedKeyStore(oauth2KV, encryptionKey); user2KV = ... }
  ```
  Prefixes: `user_`, `userindex_`, `mmuid_`, `oauth2_`, `sub_`, `ev_`, `welcome_`, `settings_panel_`, `cache_`. OAuth2 *state* keys expire after `OAuth2KeyExpiration = 15 * time.Minute` and are stored in a **one-time store** that deletes on first read (`mscal/calendar/utils/kvstore/ots.go:31-37`) — a clean CSRF-state pattern worth copying verbatim.
- **Encryption is AES-GCM with a random nonce prepended and the whole thing base64url-encoded** (`mscal/calendar/utils/kvstore/crypt.go:32-50`). The key comes from the `generated`-type manifest setting. On key rotation the plugin re-encrypts user data (`plugin.go:209`, `reEncryptUserData` at `plugin.go:361`).
- There is a **reverse index**: `LoadMattermostUserID(remoteUserID)` plus a `UserIndex []*UserShort` used to enumerate connected users for jobs without a KV scan (`user_store.go:29-58`). A polling plugin needs exactly this.
- Token refresh is a store concern, not a client concern: `RefreshAndStoreToken(token, oconf, mattermostUserID)` (`user_store.go:44`), called from `MakeUserClient` (`gcal/gcal/remote.go:43`). On refresh failure the user is marked inactive with the message "You have been marked inactive because your refresh token is expired..." (`user_store.go:23`). **For Yandex service-application tokens (1-hour, no refresh token) this hook is where a re-exchange would go.**

### Polling vs. subscribing

- **Jobs.** `mscal/calendar/jobs/job_manager.go` wraps `cluster.Schedule` with `cluster.MakeWaitForRoundedInterval(job.interval)` (`job_manager.go:~85`), storing active jobs in a `sync.Map` and closing them on deactivate to release the mutex. Three registered jobs (`plugin.go:245-249`):
  - `status_sync` — interval `StatusSyncJobInterval = 5 * time.Minute` (`engine/availability.go:24`)
  - `daily_summary` — interval `DailySummaryJobInterval = 15 * time.Minute` (`engine/daily_summary.go:24`); it refuses times that aren't a multiple of the interval (`daily_summary.go:56-58`)
  - `renew` — `24 * time.Hour`, renews each connected user's event subscription (`jobs/renew_job.go:14-19`)
- **The poll itself** is batched: `SyncAll` loads the user index and calls `syncUsers`, which ultimately issues `m.client.DoBatchViewCalendarRequests(params)` (`engine/availability.go:581`) — one batched request covering many users, with a `calendarViewTimeWindowSize = 10 * time.Minute` window (`availability.go:23`). **CalDAV has no batch equivalent**, so a Yandex provider is N sequential/concurrent requests per tick — the main scaling risk.
- Reminders for upcoming events fire at `upcomingEventNotificationTime = 10 * time.Minute` with a detection window of 110% of the sync interval (`availability.go:25-27`), and short/overlapping events are merged so sub-interval meetings don't flap the user's status (`availability.go:660-690`).
- **Google's subscription path, for contrast** (`gcal/gcal/subscription.go`): `service.Events.Watch("primary", &calendar.Channel{Type: "webhook", Address: notificationURL, Params: {"ttl": ...}})` with `subscribeTTL = 7 * 24 * time.Hour` (`:19`); renewal is delete-then-recreate (`:95-109`); `ListSubscriptions` is not implemented at all (`:112-114`). The webhook handler reads Google's `X-Goog-Resource-State` / `X-Goog-Channel-Id` / `X-Goog-Resource-Id` / `X-Goog-Channel-Token` headers, short-circuits the initial `sync` state, and returns a `remote.Notification` with `IsBare: true` (`gcal/gcal/webhook.go:29-58`). **None of this has a Yandex analogue** — a Yandex provider returns `ErrNotImplemented` for all five `Subscriptions` methods and sets `EventNotifications: false`.

### Rendering events into channels

- `views.RenderEventAsAttachment(event *remote.Event, timezone string, options ...Option) (*model.MessageAttachment, error)` (`mscal/calendar/engine/views/calendar.go:184-227`) builds a `model.MessageAttachment` with `Title` (HTML-entity-escaped via `MarkdownToHTMLEntities`), `TitleLink` set to the conference URL when present, `Text` as `"3:04PM - 4:05PM"` using `time.Kitchen` in the user's timezone, `Fields` for Location and the conference link, `Actions []*model.PostAction` for RSVP, and a `Fallback`. Events are grouped by `2006-01-02` date (`groupEventsByDate`, `:229`).
- So: **legacy message attachments + `model.PostAction`, not Blocks**, despite the docs' recommendation.
- Button clicks land on `ServeHTTP` routes registered in `mscal/calendar/api/api.go:33-38`: `/postaction/accept`, `/decline`, `/tentative`, `/respond`, `/confirmStatusChange`.
- Outbound HTML from providers is sanitized — mscalendar depends on `github.com/microcosm-cc/bluemonday v1.0.27` (`mscal/go.mod`). Relevant: iCalendar `DESCRIPTION` from Yandex is untrusted input headed for a post.

### Slash commands

Registered once in `OnActivate` via `client.SlashCommand.Register(&model.Command{...})` with full `AutocompleteData` (`mscal/calendar/command/command.go:80-107`). The trigger is provider-supplied (`config.Provider.CommandTrigger`, e.g. `gcal`), and the autocomplete icon is loaded from `assets/profile-<provider>.svg`. Subcommands (`command.go:41-77`): `connect`, `disconnect`, `summary [view|today|tomorrow|settings|time|enable|disable]`, `viewcal`, `event create`, `today`, `tomorrow`, `settings`, `info`, `help`. One handler file per subcommand under `mscal/calendar/command/`.

### Timezones

- The timezone comes from the **provider**, via `GetMailboxSettings(user.Remote.ID).TimeZone` (`mscal/calendar/engine/user.go:90-104`); Google implements it by reading a calendar setting value (`gcal/gcal/mailbox.go:31`).
- Microsoft returns **Windows** timezone names, so mscalendar carries a `windowsToIANA` conversion table and a `tz.Go(timeZone)` helper that tries `time.LoadLocation` first and falls back to the map (`mscal/calendar/utils/tz/conversion.go:11-18`, table in `tz/data.go`). Daily summary calls `tz.Go` then `time.LoadLocation` (`engine/daily_summary.go:266-270`).
- **A Yandex provider gets IANA names from `VTIMEZONE`/`TZID` in the iCalendar payload**, so `tz.Go` degenerates to a passthrough — but there is no `GetMailboxSettings` endpoint to source a *user default* from. Open question 5.

### Third-party prior art (NOT a primary source — flagged)

[github.com/ArtemIsmagilov/mm-yc-notify](https://github.com/ArtemIsmagilov/mm-yc-notify) is an existing "Mattermost — Yandex Calendar Integration (CalDAV)" project. Per its README it authenticates with a **Yandex login + app password** ("Аутентификация по логину яндекса и токену яндекс приложения"), detects changes by **long-polling on an admin-configured interval** ("Синхронизация через лонг-пуллинг, зависит от настройки администратора"), and uses the Python `caldav` library. Useful as corroboration that the app-password + polling shape is what people actually end up building, and as a source of empirical gotchas — **but it establishes no API facts**; treat its behaviour as a hypothesis to verify against Yandex's docs or a live account.

---

## Gaps and uncertainties

1. **No Yandex OAuth calendar scope could be located — this is the biggest open item.** I checked: the [register-client doc](https://yandex.ru/dev/id/doc/ru/register-client) (describes permission *groups* and the 3-group cap, gives `login:info` as its only example, does not enumerate scopes); the [Yandex ID doc index and its `llms.txt`](https://yandex.ru/dev/id/doc/ru/llms.txt) (no page titled "права доступа"/scopes, and nothing calendar-related); the [360 API reference](https://yandex.ru/dev/api360/doc/ru/ref/index.html) (no calendar service); and the [service applications page](https://yandex.ru/support/yandex-360/business/admin/en/security-service-applications) (names `ya360_security:service_applications_*` and `cloud_api:disk.*`, and says Calendar access happens over CalDAV, but **never names a calendar scope string**). Repeated fetches of the Calendar section of that page returned the CalDAV examples and tariff info but no scope names. **It therefore appears — but is not documented — that the full scope list is only visible in the oauth.yandex.ru app-registration UI** (which requires an authenticated Yandex account I do not have). *This must be resolved by logging into oauth.yandex.ru and reading the permissions tree before any architecture is committed.* If no calendar scope exists for an ordinary authorization app, the app-password path becomes the only option for non-360 users, and Open Question 1 resolves itself.
2. **Whether a plain (non-service-application) Yandex OAuth token authenticates against `caldav.yandex.ru` is not documented by Yandex.** Yandex only documents `Authorization: OAuth <token>` in the service-applications context. Secondary sources assert personal-account OAuth-over-CalDAV works, but I could not follow that back to a Yandex-owned page. Unverified.
3. **`sync-collection` / WebDAV-Sync (RFC 6578) support by Yandex is undocumented.** Nothing in Yandex's docs mentions it; DAVx⁵'s compatibility page is silent on it. This matters a lot for polling cost and must be probed with a live `PROPFIND` for `DAV: sync-collection` in the `OPTIONS`/`DAV:` header of a real account.
4. **No documented CalDAV rate limits, request quotas, or throttling behaviour for `caldav.yandex.ru`.** Yandex publishes none I could find. Polling interval cannot be safely chosen from documentation alone.
5. **No enforced maximum KV value size in Mattermost's model layer.** `plugin_key_value.go` validates key (150 runes) and plugin id (190 runes) only; the developer docs state no value-size limit either. The practical ceiling is the database column, which I did not verify. Do not design around storing large blobs (e.g. full iCalendar bodies) in KV without measuring.
6. **Mattermost Blocks: recommended but unread.** The interactive-messages doc calls Blocks "the recommended way to build interactive integration posts," but `developers.mattermost.com/integrate/plugins/blocks/` returned **404**, so I could not establish the Blocks API, its minimum server version, or its plugin-side ergonomics. Meanwhile both first-party calendar plugins still ship attachments. Unresolved: which to build on.
7. **Mattermost v12 timing.** Both first-party plugins declare `min_server_version: 12.0.0` at HEAD, yet v12.0 is described as shipping **October 2026** — after today's date. So these manifests target an imminent-but-unreleased release. I could not fetch `docs.mattermost.com/about/mattermost-server-releases.html` (returned empty); version/ESR facts come from search snippets of docs.mattermost.com and the Mattermost blog rather than a directly-read page. Treat the exact dates as soft.
8. **Yandex Telemost API:** `https://yandex.ru/dev/telemost/` returned **404**, though Telemost API is listed on the developer index. The only concrete Telemost hook I can source is the CalDAV custom property `X-TELEMOST-REQUIRED`, which the service-applications doc says auto-generates a conference link on event creation. Whether a standalone Telemost API exists is unresolved.
9. **Language.** The authoritative Yandex pages here are Russian-first. English versions exist for the service-applications page and the calendar support pages, but the 360 API reference and the app-passwords page were read in Russian; I have quoted the Russian where it is load-bearing. Some support pages exist at both `yandex.ru/support/...` and `yandex.com/support/...` with slightly different paths and, in one case (`yandex.ru/support/calendar/sync/sync-desktop.html` vs `.../common/sync/...`), different URL shapes — the docs have been reorganised and some links may be stale.
10. **Nothing here was tested against a live Yandex account.** Every CalDAV claim is documentation-derived. The endpoint hostname discrepancy (§Yandex 2), sync-collection support (§3), rate limits (§4) and OAuth-over-CalDAV for personal accounts (§2) all need an empirical probe. That probe is the cheapest next step and would settle four gaps at once.

---

## Sources

**Mattermost — documentation**
- https://developers.mattermost.com/integrate/plugins/ — plugin architecture overview
- https://developers.mattermost.com/integrate/plugins/manifest-reference/ — `plugin.json` schema
- https://developers.mattermost.com/integrate/reference/server/server-reference/ — server plugin API (KV, bots, posts, commands, cluster)
- https://developers.mattermost.com/integrate/plugins/interactive-messages/ — message attachment actions, Blocks recommendation
- https://developers.mattermost.com/integrate/plugins/developer-setup/ — developer setup (thin on packaging)
- https://developers.mattermost.com/integrate/plugins/blocks/ — **404, not read**
- https://docs.mattermost.com/product-overview/releases-lifecycle — release lifecycle (via search snippet)
- https://docs.mattermost.com/upgrade/extended-support-release.html — ESR policy (via search snippet)
- https://mattermost.com/blog/what-will-be-deprecated-in-mattermost-v12-0-october-2026/ — v12.0 ships October 2026

**Mattermost — source**
- https://github.com/mattermost/mattermost-plugin-mscalendar — cloned @ `30f208f` (2026-09-16)
- https://github.com/mattermost/mattermost-plugin-google-calendar — cloned @ `9aab035` (2026-09-17)
- https://github.com/mattermost/mattermost-plugin-starter-template — cloned; `Makefile:250-274` bundle target
- https://raw.githubusercontent.com/mattermost/mattermost/master/server/public/model/plugin_key_value.go — KV key/id limits
- https://raw.githubusercontent.com/mattermost/mattermost/master/server/public/pluginapi/cluster/job.go — `cluster.Schedule` semantics

**Yandex — official**
- https://yandex.ru/dev/index/ — developer product index (no Calendar)
- https://yandex.ru/dev/calendar/ — **404**
- https://yandex.ru/dev/api360/doc/ru/ — Yandex 360 API introduction
- https://yandex.ru/dev/api360/doc/ru/ref/index.html — Yandex 360 API reference index (20 sections, no Calendar)
- https://yandex.ru/dev/api360/doc/ru/ref/ServiceApplicationsService/ — service applications API, org-owner-only
- https://yandex.ru/support/yandex-360/business/admin/en/security-service-applications — **key page**: CalDAV URL, `Authorization: OAuth`, token-exchange, 1-hour tokens, 20-app cap, tariffs, `X-TELEMOST-REQUIRED`
- https://yandex.ru/support/yandex-360/business/admin/ru/security-service-applications — Russian original of the above
- https://yandex.ru/dev/id/doc/ru/register-client — OAuth app registration, app types, 3-permission-group cap
- https://yandex.ru/dev/id/doc/en/concepts/ya-oauth-intro — token classes, lifetimes, revocation
- https://yandex.ru/dev/id/doc/ru/ — Yandex ID doc index
- https://yandex.ru/dev/id/doc/ru/llms.txt — machine-readable doc index (searched for scope pages; none)
- https://yandex.ru/support/id/ru/authorization/app-passwords — app passwords, per-service scoping, CalDAV category
- https://yandex.ru/support/calendar/sync/sync-desktop.html — CalDAV desktop sync, app password required
- https://yandex.com/support/calendar/common/sync/sync-mobile.html — mobile sync, end-user push reminders
- https://yandex.com/support/yandex-360/business/calendar/en/sharing — calendar sharing permission levels
- https://yandex.ru/dev/telemost/ — **404**

**Standards**
- https://datatracker.ietf.org/doc/html/rfc4791 — CalDAV: MKCALENDAR, calendar-query, calendar-multiget, calendar-home-set, ETags; **no push mechanism**
- RFC 6578 (WebDAV-Sync / `sync-collection`) — referenced as a possible optimisation; **Yandex support unverified**

**Third-party (leads only, not a basis for any claim)**
- https://github.com/ArtemIsmagilov/mm-yc-notify — existing Mattermost↔Yandex Calendar CalDAV integration (app password + long-polling)
- https://www.davx5.com/tested-with/yandex — DAVx⁵ compatibility notes; gives a *different* CalDAV base URL than Yandex's own docs
