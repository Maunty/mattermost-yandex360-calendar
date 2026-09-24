# Probe: Yandex CalDAV against a live account

**Date:** 2026-09-24. **Method:** six read-only requests (`OPTIONS`, `PROPFIND` ×3, `REPORT` ×2) against a live standalone `@yandex.ru` account using a CalDAV app password over HTTP Basic auth. No writes. Credentials were supplied for this probe and are not recorded here.

This file settles four gaps left open by [mattermost-yandex-calendar-apis.md](./mattermost-yandex-calendar-apis.md) and adds two findings that materially change the design.

## Settled

### 1. Endpoint: `caldav.yandex.ru` — confirmed

`OPTIONS https://caldav.yandex.ru/` → `200`, with:

```
DAV: 1,addressbook,calendar-access,calendar-auto-schedule,calendar-availability,
     calendar-schedule,calendar-proxy,calendarserver-private-comments,
     calendarserver-principal-property-search
Allow: OPTIONS, GET, HEAD, POST, TRACE, PROPFIND, PROPPATCH, MKCOL, COPY, PUT,
       DELETE, MOVE, LOCK, UNLOCK, REPORT, MKCALENDAR
```

`OPTIONS https://calendar.yandex.ru/` → `Allow: GET,HEAD` only; it is the web UI. **DAVx⁵'s compatibility page is wrong**; Yandex's own docs were right.

Note `calendar-auto-schedule` and `calendar-schedule` are advertised, and the home set contains `inbox/` (`schedule-inbox`) and `outbox/` (`schedule-outbox`) — RFC 6638 scheduling exists, which is where RSVP would eventually live.

### 2. `sync-collection` (RFC 6578) — SUPPORTED

**It is absent from the `DAV:` header but present in every calendar collection's `supported-report-set`.** The header is not authoritative; do not infer capability from it.

A `REPORT` with an empty `<D:sync-token/>` returns the full collection plus a token:

```
<D:sync-token>sync-token:1 1780000000000</D:sync-token>
```

The token's numeric part equals the collection's `getctag`, and etags are millisecond epoch timestamps. Caveat: RFC 6578 has **no time-range filter**, so an initial sync returns the entire collection, which on a long-lived account is years of events.

### 3. `getctag` — supported, and cheaper than sync

Each calendar collection exposes `http://calendarserver.org/ns/:getctag`. Critically, **one `PROPFIND` with `Depth: 1` on the calendar-home returns the ctag for every collection at once**.

This collapses the scaling risk the first research file flagged. Per user per poll tick the cost is **one request**, not N×M: fetch all ctags, compare to stored, and issue a `calendar-query` only for collections that changed.

### 4. Collection naming — `events-default` is wrong

Actual collections on this account:

| href | displayname | resourcetype | component set | getctag |
|---|---|---|---|---|
| `/calendars/<user>/` | (user's name) | `collection` | — | *404* |
| `/calendars/<user>/inbox/` | — | `schedule-inbox` | — | *404* |
| `/calendars/<user>/outbox/` | — | `schedule-outbox` | — | *404* |
| `/calendars/<user>/events-1000001/` | Мои события | `calendar` | `VEVENT` | `1780000000000` |
| `/calendars/<user>/todos-1000002/` | Не забыть | `calendar` | `VTODO` | `1790000000000` |

The collection name is `events-<numeric-id>`, **not** the `events-default` in Yandex's own documentation example. Hardcoding it would fail on every real account. Discovery via `current-user-principal` → `calendar-home-set` → `PROPFIND Depth: 1` is mandatory, and collections must be filtered by `resourcetype` containing `calendar` **and** `supported-calendar-component-set` containing `VEVENT` (otherwise you poll the VTODO list for meetings).

`principal-URL` returns 404; only `current-user-principal` works.

## New findings

### 5. Server-side recurrence expansion is IGNORED — the biggest cost in the build

A `calendar-query` carrying `<C:expand start="..." end="..."/>` inside `<C:calendar-data>` returns the **master events unchanged**: `RRULE` still present, zero `RECURRENCE-ID` instances, VEVENT count equal to the number of stored resources rather than occurrences. The server accepted the request (`207`) and silently ignored the expand.

**Consequence: the plugin must expand recurrence client-side** — `RRULE`, `RDATE`, `EXDATE`, and `RECURRENCE-ID` overrides, evaluated against each event's `VTIMEZONE` so DST transitions land correctly.

This is the single largest piece of work in the project, and **neither prior-art plugin has code to steal for it**: Microsoft Graph's `calendarView` and Google's `singleEvents=true` both expand server-side, so mscalendar and gcal never needed it.

### 6. Timezones are IANA — the Windows conversion table is dead weight

`TZID` values observed include `Europe/Moscow` and `Etc/UTC`. Every event carries a full inline `VTIMEZONE` with `TZOFFSETFROM`/`TZOFFSETTO`/`TZNAME`/`RDATE` historical transitions (62 offset records across 4 events — payloads are not small).

Go's `time.LoadLocation` takes these directly. mscalendar's `windowsToIANA` table and `tz.Go` helper have no purpose here.

## Still unverified

- **Whether a plain (non-service-application) OAuth token authenticates over CalDAV.** Untestable without a token, which requires knowing whether a calendar scope exists — still the top open item.
- **Rate limits.** Six requests drew no throttling; that establishes nothing about sustained polling. Needs a deliberate load probe before the interval is fixed.
- **Whether `sync-collection` accepts a `limit`**, which would make initial sync affordable.

---

# Probe 2: OAuth authorization-app token

**Date:** 2026-09-24. A live OAuth token from an *authorization app* registered at `oauth.yandex.ru` with `calendar:events.read` + `calendar:calendars.read`. Token was valid for 1 year with a refresh token — notably better than the service application's 1 hour.

## The headline: an OAuth consent token does NOT open CalDAV

| Attempt | Result |
|---|---|
| `Authorization: OAuth <token>` → `caldav.yandex.ru` | **401** |
| `Authorization: Bearer <token>` → `caldav.yandex.ru` | **401** |
| HTTP Basic, token as password | **401** |
| App-specific password over Basic (probe 1) | **207 OK** |

Yandex documents `Authorization: OAuth <token>` against CalDAV **only** in the Yandex 360 service-application context. That wording now looks deliberate: CalDAV appears to accept OAuth tokens only from service applications, not from ordinary authorization apps.

## The token works elsewhere

`GET https://login.yandex.ru/info?format=json` with `Authorization: OAuth <token>` → **200**, returning `id`, `login`, `client_id`, `psuid`. **Identity is available without a `login:*` scope**, which removes the need for one.

## No REST calendar API could be located

Probed with the token; all failed:

```
api360.yandex.net/calendar/v1/calendars              404
api360.yandex.net/calendar/v1/events                 404
api360.yandex.net/calendar/v1/users/me/calendars     404
api360.yandex.net/calendar/                          404
calendar.yandex.ru/api/ , /api/calendars, /api/v1/events, /web-api/calendars   404
cloud-api.yandex.net/v1/calendar                     404
api.calendar.yandex.net , api.calendar.yandex.ru     DNS failure
api.yandex.ru/calendar/v1/calendars                  301 → yandex.ru/dev/calendar/... → 404 (docs site)
```

Routing discrimination: `api360.yandex.net/directory/v1/org` returns **403** (route exists, token lacks scope) while `directory/v1/users` returns 404. So `api360` does route real APIs and the 404s on calendar paths are genuine absence, not blanket rejection.

**This does not prove no REST API exists** — the eleven granular `calendar:*` scopes imply *something* consumes them. It proves only that it is not reachable by guessing, and is not at any documented or conventional Yandex endpoint.

## Consequence for the design

The "one OAuth consent flow for every account" plan is **not achievable with what is verified**. Current state:

- **Standalone account**: app-specific password over CalDAV Basic — the only verified working path.
- **Yandex 360 org**: service-application token over CalDAV — documented, still untested.
- **OAuth consent token**: proves identity, reaches no calendar data.

## Cheapest things left to try

1. **Re-register the authorization app with `calendar:all`** (or `calendar:read_all`) and retry CalDAV. The 401 may be a scope problem rather than an app-type problem — `events.read`/`calendars.read` may simply not be the scopes CalDAV checks.
2. **Open the Yandex Calendar web UI with browser devtools** and read the network tab. Whatever host and paths it calls is the real API surface. Caveat: the web UI may use an internal session-authenticated API that OAuth scopes do not cover.

---

# Probe 3: OAuth over CalDAV — WORKS with a broad scope

**Date:** 2026-09-24. **This supersedes Probe 2's headline.**

The app was re-registered with a broader calendar scope. Yandex returned the **byte-identical access token** (only the refresh token changed), so the grant was widened server-side against the existing token. That same token now authenticates:

| Request | Probe 2 (narrow scopes) | Probe 3 (broad scope) |
|---|---|---|
| `PROPFIND /` → `current-user-principal` | 401 | **207** |
| `OPTIONS /` | — | **200** |
| `PROPFIND` principal → `calendar-home-set` | — | **207** |
| `PROPFIND` home `Depth: 1` → collections + ctags | — | **207** |
| `REPORT calendar-query` with `time-range` | — | **207** |

**Conclusion: CalDAV does accept authorization-app OAuth tokens.** Probe 2's 401 was a **scope** problem, not an app-type problem. `calendar:events.read` + `calendar:calendars.read` are not the scopes CalDAV checks — they presumably gate a REST surface that remains undiscovered.

This makes the single-OAuth-flow design viable for every Yandex account, standalone or 360, and removes app-specific passwords from the plugin entirely. Token lifetime was **1 year with a refresh token**, versus the service application's 1 hour.

## Gotcha: principal href casing is inconsistent

- Basic auth (app password, login `samtester@yandex.ru`) → `/principals/users/samtester%40yandex.ru/`
- OAuth token → `/principals/users/SamTester%40yandex.ru/`, and `calendar-home-set` → `/calendars/SamTester%40yandex.ru/`

Both cases resolve. **Never compare principal or home hrefs case-sensitively, and never reconstruct them from the login** — always use the href the server returned.

## Still open

- **Which exact scope opens CalDAV.** `calendar:all` is the likely candidate but was not isolated; `calendar:read_all` and `calendar:write_all` were not tested separately. This matters: the plugin is read-only, and requesting write access it never uses is what makes an admin refuse to approve it.
- The REST API behind `calendar:events.read` etc. remains unlocated.

---

# Probe 4: isolating the scope — `calendar:all` is required

**Date:** 2026-09-24. Same OAuth app (`client_id` unchanged), re-granted with **`calendar:read_all` only**.

| Request | `calendar:events.read` + `calendar:calendars.read` | `calendar:read_all` | `calendar:all` |
|---|---|---|---|
| `login.yandex.ru/info` | 200 | 200 | 200 |
| CalDAV `PROPFIND` `current-user-principal` | 401 | **401** | **207** |
| CalDAV `PROPFIND` home `Depth: 1` | — | **401** | **207** |

**`calendar:all` is the only scope observed to open CalDAV.** Neither the fine-grained read scopes nor the broad read-only scope work, even though the token is valid in all three cases.

## Consequence, and it is not a small one

The plugin is read-only in v1 but **must request a scope that also grants write access**. This is a real cost, not a footnote:

- The consent screen a user sees will say the plugin can modify their calendar. Some will decline.
- A Yandex 360 administrator evaluating the plugin sees a write-capable grant for a read-only feature set, which is exactly the kind of mismatch that gets an integration refused.
- It removes "we cannot possibly damage your calendar" as a claim. The plugin must earn that by never issuing a write, not by lacking permission.

Document this prominently in the admin setup instructions, with the probe table as evidence that the narrower scopes were tried and rejected by Yandex, not skipped for convenience.

## Unresolved

`calendar:write_all` was not tested in isolation, so it is not strictly proven that write capability specifically is what CalDAV checks — only that `calendar:all` works and the two read-only scopes do not.
