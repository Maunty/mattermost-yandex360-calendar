# Yandex Calendar for Mattermost

Stop being surprised by your own meetings.

This plugin connects a person's Yandex Calendar to Mattermost and sends them
two kinds of direct message:

- **A reminder**, ten minutes before each event starts, naming it, when it
  runs, where it is, and linking to the conference if there is one.
- **A daily summary**, once each morning, listing that day's events.

Connecting is one command. You run `/yacal connect`, approve access on Yandex's
own consent screen, and the window closes. You never type a password or a token
into Mattermost, and you can disconnect at any time.

**The plugin only reads.** It never creates, changes or deletes anything in
anyone's calendar. It does have to ask Yandex for a permission that includes
write access, because Yandex refuses every narrower one — see
[why](docs/admin-setup.md#why-a-read-only-plugin-asks-for-write-access), with
the evidence.

## Commands

| Command | What it does |
|---|---|
| `/yacal connect` | Connect your Yandex Calendar |
| `/yacal disconnect` | Disconnect it and stop all messages |
| `/yacal today` | List today's events |
| `/yacal settings` | Show your current choices |
| `/yacal reminders on\|off` | Turn reminders on or off |
| `/yacal summary on\|off` | Turn the daily summary on or off |
| `/yacal summary HH:MM` | Choose when the daily summary arrives |
| `/yacal help` | List all of this |

## Installing it

Administrators: [docs/admin-setup.md](docs/admin-setup.md). It takes about five
minutes — register an application at `oauth.yandex.ru`, paste two values into
the System Console.

## Building it

```sh
make build   # compile for this machine — the one to run while working
make test    # the whole suite
make check   # vet and formatting
make release # every platform, then the installable bundle in dist/
```

`make build` is deliberately the default working target: cross-compiling all
five platforms the manifest declares takes minutes and answers nothing you do
not already know from `make build` and `make test`.

## How it is put together

Seven pieces, each with a small interface:

| Package | What it is responsible for |
|---|---|
| `server/internal/caldav` | Speaking the protocol. Read-only by construction: it has no method that writes, and refuses any HTTP method that is not a read. |
| `server/internal/calendar` | The domain: an Event as stored, an Occurrence as experienced, and reading iCalendar into them. |
| `server/internal/recurrence` | Turning Events into Occurrences. A pure function: no network, no storage, no dependency on the rest of the plugin. |
| `server/sync.go` | Deciding what to fetch, skipping collections whose change tag has not moved, and keeping each person's cached window. |
| `server/deliver.go` | Deciding which reminders and summaries are due. |
| `server/render.go` | Turning occurrences into messages, in the reader's own timezone. |
| `server/store.go` | Connections, settings and everything else that is remembered. |

Recurrence is expanded here rather than at the provider because
[Yandex accepts a request to expand it and silently ignores it](docs/research/caldav-probe-findings.md).
That is the largest single piece of work in the project, and it is why
`server/internal/recurrence` has the densest tests.

## How it is tested

There are exactly two substitution points, and the clock is a parameter rather
than an interface.

- **Below the wire**: a [fake CalDAV server](server/internal/fakecaldav) serving
  the response shapes a real account returned during probing, quirks included —
  the principal comes back with different letter casing than the login, the
  events collection carries a numeric suffix rather than the documented name,
  and the calendar home answers 404 for properties it does not hold.
- **Above the plugin**: the standard Mattermost plugin test mock. Assertions
  are made on the posts that were created.

Everything in between is the real implementation: discovery, collection
filtering, change-tag comparison, query construction, iCalendar parsing,
recurrence expansion, scheduling and rendering. A test says *given this
calendar and this wall-clock time, these messages are sent, with this content*.
No test reaches into intermediate state or names an internal function, so a
refactor that preserves behaviour breaks nothing.

Recurrence expansion is tested directly, with no substitution of any kind.

```sh
go test ./...
```

## What is deliberately not here

Writing to calendars, responding to invitations, status and do-not-disturb
synchronisation, shared calendars, tasks, free/busy, a calendar view inside
Mattermost, and localisation. The reasoning for each is in
[the spec](.scratch/yandex-calendar-v1/spec.md).

## Documents worth reading before changing anything

- [CONTEXT.md](CONTEXT.md) — what the words mean here, and which ones to avoid.
- [docs/adr/0001](docs/adr/0001-oauth-consent-for-connections.md) — why OAuth
  consent rather than app passwords.
- [docs/research/caldav-probe-findings.md](docs/research/caldav-probe-findings.md)
  — four probes against a live account. Two of them overturned conclusions
  reached from documentation alone; several decisions in this codebase only
  make sense once you have read it.
- [docs/research/rest-api-spike.md](docs/research/rest-api-spike.md) — why
  there is no REST API to use instead.
