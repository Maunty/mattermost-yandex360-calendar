# Setting up Yandex Calendar for Mattermost

This plugin sends each person who connects their Yandex Calendar two kinds of
direct message: a reminder shortly before each of their events, and a summary
of their day each morning.

It only reads. It never creates, changes or deletes anything in anyone's
calendar. **It nevertheless has to ask each user for a permission that includes
write access**, and the section [Why a read-only plugin asks for write
access](#why-a-read-only-plugin-asks-for-write-access) explains why, with the
evidence. Please read it before you decide: it is the one thing about this
plugin that will look wrong.

## What you need

- A Mattermost server in the 11 series or later.
- Permission to register an application at [oauth.yandex.ru](https://oauth.yandex.ru/).
  Any Yandex account can do this; it does not require Yandex 360 for Business,
  and it does not require an organization.

Users do not need anything. They do not need a Yandex 360 subscription, they do
not create app passwords, and they never type a password or a token into
Mattermost.

## 1. Register the Yandex application

1. Go to <https://oauth.yandex.ru/client/new>.
2. Give the application a name your users will recognise on the consent screen.
   They will see this name when they are asked to approve access, so something
   like "Mattermost" is better than the plugin's own name.
3. Under **Platforms**, choose **Web services**.
4. In **Redirect URI**, enter exactly:

   ```
   https://<your-mattermost-site-url>/plugins/yandex-calendar/oauth/complete
   ```

   Use the same Site URL that is configured in your System Console, including
   the scheme, and with no trailing slash before `/plugins`. If these do not
   match character for character, every connection attempt fails at the last
   step.
5. Under **Data access**, add the scope **`calendar:all`**.

   This is the only scope that works. See below. Do not substitute
   `calendar:read_all` or the fine-grained `calendar:events.read` and
   `calendar:calendars.read` — the plugin will install and users will be able
   to approve consent, but every attempt to read a calendar will be refused.
6. Save. Yandex shows you a **ClientID** and a **Client secret**.

## 2. Configure the plugin

In the System Console, under **Plugins → Yandex Calendar**:

| Setting | What to put in it |
|---|---|
| Yandex Client ID | The ClientID from step 1 |
| Yandex Client Secret | The Client secret from step 1 |
| Reminder Lead Time | How long before an event its reminder is sent. Ten minutes by default. This applies to everyone on the server; it is not a per-user setting. |
| At Rest Encryption Key | Generated for you. Leave it alone. |

Until the Client ID and the Client Secret are both set, the plugin does
nothing: any `/yacal` command answers with what is missing and where to supply
it.

**The encryption key.** Users' Yandex tokens are encrypted with it before they
are written to the database. Regenerating it makes every stored token
unreadable, which disconnects every user; they each have to run
`/yacal connect` again. There is no reason to regenerate it except a suspected
compromise, and if you do, tell people first.

## 3. Tell people it exists

Each person runs:

```
/yacal connect
```

They get a link, approve access on Yandex's own consent screen, and the window
closes. `/yacal help` lists everything else.

## Why a read-only plugin asks for write access

The consent screen a user sees will say this application can manage their
calendar — create, change and delete events. The plugin does none of that. It
asks anyway because Yandex's CalDAV service refuses every narrower calendar
scope.

This was established by probing a live account, not assumed. Each row used a
token that was valid — the same token succeeded against Yandex's own identity
endpoint in every case:

| Scope granted | `login.yandex.ru/info` | CalDAV `current-user-principal` | CalDAV collection listing |
|---|---|---|---|
| `calendar:events.read` + `calendar:calendars.read` | 200 | **401** | — |
| `calendar:read_all` | 200 | **401** | **401** |
| `calendar:all` | 200 | **207** | **207** |

The two read-only options are not partially supported or rate-limited. They are
rejected outright. `calendar:all` is the only scope observed to open CalDAV at
all.

The full record, including the requests and responses, is in
[the probe findings](research/caldav-probe-findings.md) — see "Probe 4:
isolating the scope". The decision to use OAuth consent rather than app
passwords, and what was given up to get there, is in
[ADR 0001](adr/0001-oauth-consent-for-connections.md).

### What is done about it

The guarantee that this plugin never writes is carried by the code, not by the
permission:

- The CalDAV client has no method that creates, changes or deletes anything.
  Its entire surface is three calls: find the calendar home, list the
  calendars, query events in a time range.
- The one place that sends a request refuses any HTTP method other than
  `OPTIONS`, `PROPFIND` and `REPORT` before the request is built. A write
  cannot be issued by mistake, only by removing that check on purpose.
- Tests assert both: one fails if the client grows a method that is not on the
  read-only list, and another drives a full read and fails if anything but a
  read method reached the wire.

If you want to verify this rather than take it on trust, the checks are in
`server/internal/caldav/client_test.go`, `readonly_test.go` and `guard_test.go`,
and the whole client is about 250 lines in `server/internal/caldav/`.

### What this plugin cannot promise

It cannot promise that Yandex will never be asked for write access, because it
has to be. If your organization's policy is that no integration may hold a
write-capable grant on user calendars, this plugin cannot comply with that
policy, and that is a legitimate reason to refuse it. A path that would not
require it — a REST API behind the fine-grained scopes — was looked for and
could not be found; see [the spike record](research/rest-api-spike.md).

## What the plugin reads, and what it sends

It reads, for each connected person:

- The list of their calendar collections, and each one's change tag.
- The events in those collections that fall in a window covering roughly the
  next two days.

Collections are filtered to those that hold events. **The task list is never
read.** Neither is the scheduling inbox or outbox.

It sends only direct messages from its own bot account, only to the person
whose calendar it read. An event's **description is never included in a
message** — descriptions are long, often HTML, and frequently hold private
notes. What a message carries is the title, the start and end time, the
location if there is one, and the conference link if there is one.

## Load and rate limits

One poll of one person's calendars costs **one request** when nothing has
changed: a single depth-one read of their calendar home returns every
collection's change tag at once, and only a collection whose tag has moved is
queried. Each person is polled every ten minutes, offset by up to four minutes
either side so that people do not bunch up.

For a server with 100 connected users that is roughly 600 requests an hour at
rest.

**Yandex's rate limits on sustained polling are undocumented and have not been
measured.** Probing drew no throttling, but six requests prove nothing about
hundreds of users. If you are deploying to more than a handful of people,
measure before you do. The poll interval and jitter are constants at the top of
`server/sync.go`.

## Running more than one node

Both background jobs are scheduled cluster-wide, so a multi-node installation
polls each person once and sends each message once. Nothing extra is needed.

## Troubleshooting

**"The Yandex Calendar plugin is not configured yet."** The Client ID or the
Client Secret is missing. The message names which.

**Everyone's connection fails immediately after consent.** Almost always the
redirect URI. It must match what is registered at Yandex character for
character, including the scheme and the Site URL.

**A user consented but gets nothing, and `/yacal today` says it could not read
their calendar.** Check the scope on the Yandex application is `calendar:all`.
The narrower scopes let consent succeed and then refuse every read.

**A user is told their access was withdrawn but says they did not withdraw
it.** The plugin marks a connection inactive only after two consecutive
refusals of the credential itself; being unable to reach Yandex never does it.
Check whether the user revoked access at
<https://id.yandex.ru/security/app-passwords>, and check the server log for
what the provider actually answered.

**Nothing arrives, and nothing is in the log.** `/yacal settings` shows each
person when their calendar was last read successfully. "Not yet" with a live
connection points at the poll job; a recent time with no messages points at
their reminder and summary switches, which the same output shows.
