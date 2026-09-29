# Setting up Yandex Calendar for Mattermost

This plugin sends each person who connects their Yandex Calendar two kinds of
direct message: a reminder shortly before each of their events, and a summary
of their day each morning.

It only reads. It never creates, changes or deletes anything in anyone's
calendar, and the permission it asks each user for says the same thing: read
their calendar's events, and nothing else.

**Yandex's calendar API is in early access.** Before anyone can connect, Yandex
has to approve the application you register in step 1. Nothing works until it
is approved.

## What you need

- A Mattermost server in the 11 series or later.
- Permission to register an application at [oauth.yandex.ru](https://oauth.yandex.ru/).
- Your users' accounts in a Yandex 360 for Business organization. Standalone
  Accounts (Yandex accounts that belong to no organization) are not supported
  for now.
- Yandex's approval of your application for early access to the calendar API.
  Your Yandex 360 contact can tell you how to request it.

Users do not need anything else. They do not create app passwords, and they
never type a password or a token into Mattermost.

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
   the scheme and any port, and with no trailing slash before `/plugins`.

   Register it exactly. Yandex doesn't reject an address that doesn't match.
   Instead it ignores the one the plugin asks for and sends people to the
   first Redirect URI registered on the application. If that address doesn't
   reach this Mattermost server, people approve access and then aren't
   connected. If you register more than one Redirect URI, put this one first.
5. Under **Data access**, add the scope **`calendar:events.read`** and nothing
   else. It is all the plugin needs, including for naming the account a person
   connected.

   Don't substitute `calendar:all`. It is write-capable, and the calendar API
   refuses it: users could approve consent, but every read would fail.
6. Save. Yandex shows you a **ClientID** and a **Client secret**.
7. Request Yandex's approval of the application for the calendar API. Nothing
   works until it is approved.

## 2. Configure the plugin

In the System Console, under **Plugins → Yandex Calendar**:

| Setting | What to put in it |
|---|---|
| Yandex Client ID | The ClientID from step 1 |
| Yandex Client Secret | The Client secret from step 1 |
| Default Reminder Lead Time | How long before an event its reminder is sent, for people who haven't chosen their own with `/yacal reminders <minutes>`. From 1 to 60 minutes; empty or 0 means ten, and anything above 60 is treated as 60. A person's own choice is kept when you change this. |
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

## Why the plugin can be trusted to only read

Two things say so, and they don't depend on each other:

- **The permission.** `calendar:events.read` grants the application the right
  to read events, and no right to create, change or delete them.
- **The code.** The calendar client has no method that creates, changes or
  deletes anything. Its whole surface is one call: read the events in a time
  window. A test drives a full read, across several pages, and fails if the
  client sent anything other than a read of events. The client is about 300
  lines in `server/internal/calendarapi/`.

### Why earlier versions of this guide asked for write access

The plugin used to read calendars over CalDAV. Yandex's CalDAV service refuses
every read-only scope, so the plugin had to ask for `calendar:all`. The evidence
is in [the CalDAV probe findings](research/caldav-probe-findings.md). Yandex
has since given early access to a calendar API that accepts a read-only scope,
and the plugin moved to it
([ADR 0002](adr/0002-rest-api-replaces-caldav.md),
[probe findings](research/rest-api-probe-findings.md)). The CalDAV code is
still in the plugin as a fallback, unused. If it is ever switched back on, this
guide will ask for `calendar:all` again, and every user will have to reconnect.

## What the plugin reads, and what it sends

It reads, for each connected person, the events in a window from a day ago to
two days ahead: across all of their calendars, with recurring events already
expanded by Yandex into their individual occurrences.

Of those, it keeps only the person's own events: ones they organise, are
invited to, or have added to their calendar themselves. Events they declined
are left out, and so are colleagues' events on a shared calendar.

It sends only direct messages from its own bot account, only to the person
whose calendar it read. An event's **description is never included in a
message**, because descriptions often hold private notes. What a message
carries is the title, the start and end time, the location if there is one,
and the Telemost link if the event has one. The Telemost link is the one thing
taken from the description: the labelled call link Yandex writes there, and
nothing else.

## Load and rate limits

One poll of one person's calendar costs **one request**, plus one per extra
page of events. A page holds up to a hundred, so a busy three-day window rarely
needs a second. There is no way to ask Yandex only for what changed, so every
poll reads the whole window. Each person is polled every ten minutes, offset by
up to four minutes either side so that people don't bunch up.

For a server with 100 connected users, that is roughly 600 requests an hour.

Yandex applies a per-user rate limit to the calendar API. Polling one person
every ten minutes is far below it. If Yandex does push back, the plugin treats
it like an outage: it tries again at the next poll and never disconnects
anybody over it. The poll interval and jitter are constants at the top of
`server/sync.go`.

## Running more than one node

Both background jobs are scheduled cluster-wide, so a multi-node installation
polls each person once and sends each message once. Nothing extra is needed.

## Troubleshooting

**"The Yandex Calendar plugin is not configured yet."** The Client ID or the
Client Secret is missing. The message names which.

**Everyone's connection fails immediately after consent.** Almost always the
redirect URI. When the plugin's address doesn't exactly match one registered
at Yandex, Yandex sends the browser to the first registered Redirect URI
instead, so look at where the browser lands after approving. It has to be this
server's `/plugins/yandex-calendar/oauth/complete`, at an address the person's
browser can reach. A registered `localhost` address sends everybody on other
machines to their own computer.

**A user consented but gets nothing, and `/yacal today` says it could not read
their calendar.** Check two things. The scope on the Yandex application must be
`calendar:events.read`, and `calendar:all` doesn't work. And Yandex must have
approved the application for the calendar API. Either problem makes every read
fail.

**A user is told their access was withdrawn but says they did not withdraw
it.** The plugin marks a connection inactive only after two consecutive
refusals of the credential itself; being unable to reach Yandex never does it.
A refusal looks the same whatever its cause, so check three things:

- The scope on the Yandex application is `calendar:events.read`. With any
  other scope, Yandex refuses every read, and reconnecting won't help.
- Yandex has approved the application for the calendar API. Until it does,
  every read is refused, and reconnecting won't help.
- The user hasn't revoked access at
  <https://id.yandex.ru/security/app-passwords>. If they have, reconnecting
  fixes it.

The server log shows what Yandex actually answered.

**Nothing arrives, and nothing is in the log.** `/yacal settings` shows each
person when their calendar was last read successfully. "Not yet" with a live
connection points at the poll job; a recent time with no messages points at
their reminder and summary switches, which the same output shows.
