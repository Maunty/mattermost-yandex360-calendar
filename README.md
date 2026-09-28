# Yandex Calendar for Mattermost

Stop being surprised by your own meetings.

This plugin connects a person's Yandex Calendar to Mattermost and sends them
two kinds of direct message:

- **A reminder**, ten minutes before each event starts, naming it, when it
  runs, where it is, and linking to the Telemost call if there is one.
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

### In a container

[`.devcontainer/`](.devcontainer/) is the whole environment, not just a
toolchain: it composes the Go workspace together with a Mattermost 11 server
and its database, so opening the repo gives you somewhere to deploy the plugin
and watch it run.

Open the repo in VS Code and choose **Reopen in Container**, or use the
[devcontainer CLI](https://github.com/devcontainers/cli):

```sh
devcontainer up --workspace-folder .
devcontainer exec --workspace-folder . make dev-admin   # first system admin
devcontainer exec --workspace-folder . make deploy      # build, upload, enable
devcontainer exec --workspace-folder . make test
```

Nothing needs configuring first — every setting has a working default.
Mattermost is forwarded to port 8065. It wants about 2 GB of memory across the
containers.

The image tracks the `go` directive in `go.mod`, and `GOTOOLCHAIN` is left on
`auto`, so the exact patch release go.mod asks for is what compiles the plugin
whatever the image ships. The editor is configured to format with `gofmt` and
nothing else, because `make check` fails on any file `gofmt` would rewrite.

The server half can also run on its own, on a machine that never opens the dev
container — see [`dev/README.md`](dev/README.md). `make deploy` talks HTTP and
takes `MM_SERVER_URL`, so building here and deploying there is an ordinary
thing to do.

It is a development server: plain HTTP, an admin socket in the container,
signature checks off, a default database password. `dev/README.md` says what
that means and where not to put it.

The setting that matters most is `MM_SITE_URL`. The plugin builds its OAuth
redirect from it, and Yandex refuses a callback that is not
character-for-character what the application has registered, so getting it
wrong produces a connect flow that works until its very last step.

## How it is put together

Events come from Yandex's REST calendar API, which is in early access. It
returns each occurrence of a recurring Event as its own item, with moved and
cancelled occurrences already applied, so nothing on this side works out a
recurrence rule. Why the plugin moved off CalDAV is in
[ADR 0002](docs/adr/0002-rest-api-replaces-caldav.md).

| Package | What it is responsible for |
|---|---|
| `server/internal/calendarapi` | Reading a window of a person's Events from the REST API, every page of it, and keeping only the Events they organise or are invited to. Read-only by construction: it has no method that writes. |
| `server/internal/calendar` | The domain: an Event as stored, an Occurrence as experienced. |
| `server/sync.go` | The event source, which is the one place the transport is chosen, and each person's cached window of occurrences, re-read in full on every poll. |
| `server/deliver.go` | Deciding which reminders and summaries are due. |
| `server/render.go` | Turning occurrences into messages, in the reader's own timezone. |
| `server/store.go` | Connections, settings and everything else that is remembered. |

### The CalDAV fallback

The CalDAV path is still in the tree, compiled and tested, but not wired in.
It is kept in case early access goes badly.

| Package | What it is responsible for |
|---|---|
| `server/caldav_source.go` | The CalDAV event source: discovery, querying each collection, and expanding what comes back. |
| `server/internal/caldav` | Speaking the protocol. Read-only by construction: it refuses any HTTP method that is not a read. |
| `server/internal/calendar` | Also reads iCalendar into Events, which only this path needs. |
| `server/internal/recurrence` | Turning Events into Occurrences. A pure function: no network, no storage, no dependency on the rest of the plugin. |

Over CalDAV, recurrence has to be expanded on the plugin's side because
[Yandex accepts a request to expand it and silently ignores it](docs/research/caldav-probe-findings.md).
That is why `server/internal/recurrence` has the densest tests.

### Switching back to CalDAV

1. Set the plugin's `readEvents` to `readFromCalDAV` when the plugin
   activates. That one assignment is the whole switch.
2. Put the consent scope back to the write-capable one, because CalDAV refuses
   every narrower scope. See
   [ADR 0001](docs/adr/0001-oauth-consent-for-connections.md) and the setup
   guide.
3. Release.

Every existing Connection then has to be consented again under the broader
scope. Reminders already sent are not sent again, because both paths identify
an occurrence the same way.

## How it is tested

There are exactly two substitution points, and the clock is a parameter rather
than an interface.

- **Below the wire**: a [fake of the REST calendar API](server/internal/fakecalendarapi).
  Tests still describe calendars as iCalendar objects in named calendars. The
  fake answers the way the API does: one item per occurrence, declined Events
  left out, Watched Events marked as such, and the whole list paged.
- **Above the plugin**: the standard Mattermost plugin test mock. Assertions
  are made on the posts that were created.

Everything in between is the real implementation: the request, paging, reading
each item, deciding which Events are the person's, scheduling and rendering. A
test says *given this calendar and this wall-clock time, these messages are
sent, with this content*. No test reaches into intermediate state or names an
internal function, so a refactor that preserves behaviour breaks nothing.

The CalDAV fallback is tested the same way. A few end-to-end tests switch the
event source to CalDAV and run against a [fake CalDAV server](server/internal/fakecaldav)
that serves the shapes a real account returned during probing, quirks included.
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
- [docs/adr/0002](docs/adr/0002-rest-api-replaces-caldav.md) — why Events are
  read over the REST API, and why CalDAV is kept.
- [docs/research/caldav-probe-findings.md](docs/research/caldav-probe-findings.md)
  — four probes of CalDAV against a live account. Two of them overturned conclusions
  reached from documentation alone; several decisions in this codebase only
  make sense once you have read it.
- [docs/research/rest-api-spike.md](docs/research/rest-api-spike.md) — the
  search for a REST API before early access, since superseded by ADR 0002.
