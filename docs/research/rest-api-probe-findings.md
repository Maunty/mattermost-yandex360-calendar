# REST calendar API: first probe against a real account

**Date:** 2026-09-28. **Ticket:** [rest-transport 01](../../.scratch/rest-transport/issues/01-day-one-probe.md).
**Account:** a member account in a Yandex 360 Organization, with a
realistic amount of history on it. **Token:** issued through an OAuth application registered by
the Organization's owner, granted `calendar:read_all` and nothing else.

This records what the API was *observed* to do. It deliberately doesn't
restate the API's documentation, which is early-access material and not
public.

## What was settled

### The read-only scope reads Events, and identity still works

With a `calendar:read_all` token:

- The Event listing answered 200 with the account's Events.
- Yandex's user-info lookup, the one the plugin uses to name the connected
  account, answered 200 with the account's id and login. It needs no scope of
  its own, as it didn't over CalDAV.
- CalDAV refused the same token with 401. That matches
  [probe 4](caldav-probe-findings.md): CalDAV only opens for `calendar:all`.

The narrower `calendar:events.read` wasn't tried. It needs an application
granted only that scope.

### Recurring Events come back one item per occurrence

A series comes back as one item per occurrence in the window. A moved occurrence
comes back once, at its new time, still carrying its original start as its
occurrence id. Every moved occurrence seen behaved this way. So nothing on the plugin's side needs to know a recurrence rule.

Cancelled occurrences weren't identified, because nothing on the account
could be pointed to as one. The plugin doesn't treat them specially: an
occurrence that doesn't come back produces no Reminder.

### An occurrence's original start is UTC

**This one overturned the first implementation.** The original start has no
offset written on it, and it reads naturally as local time. It isn't local
time. Asking for the same day's Events in Europe/Moscow and in Asia/Tokyo moved
every start by six hours and left every original start exactly as it was. A
09:30 Moscow meeting carries an original start of 06:30.

Read as local time, the key the plugin records a sent Reminder against would
still have been stable from poll to poll. But it would have disagreed with the
key the CalDAV path builds, so switching transports could have sent a Reminder
twice. Fixed in `14dcf91`, with a test that fails on the old reading.

### Paging works as described

Pages of up to 100 items, and a continuation key until the last page. A window of several years ran to dozens of pages.

### Declined Events are left out unless asked for

The listing leaves declined Events out by default. The plugin never asks for
them, and it relies on that default. Asking for them added many items, in two groups:

- **Other people's Events on a Shared Calendar**, almost all of them. By
  default, only the Events the person created on that calendar themselves
  come back.
- One Event on the person's own calendar.

The default leaves out the Events that belong to other people, and keeps the
person's own. That is exactly the rule in CONTEXT.md, and the plugin depends
on it.

### The "subscriber" relation is a Watched Event, not a Subscribed Calendar

A few items carried the subscriber relation. All were on the person's own
main calendar and organised by someone else, with several participants. The participation lookup says the person isn't a
participant. They are Events the person put on their own calendar without
being invited: **Watched Events** in CONTEXT.md. They are now reminded
(`193cac2`). The account has no Subscribed Calendars, so how one would appear
is still unknown.

### Personal and shared calendars look the same

Nothing on an Event says whether its calendar is personal or shared. An Event the person created on a Shared Calendar, and a routine Event with no attendees
on their personal calendar, come back identically: the person as organiser,
no organiser address recorded, no participants. The per-Event "am I a
participant?" lookup is no help. It answered "not a participant" for every
Event tried, including meetings the person organised with several participants.
So the plugin can't apply a rule that depends on which calendar an Event is
on. So far it hasn't needed to, because the default listing already gives
the right answer.

### Calendars can't be listed

There is no route for listing calendars. Each Event does say which calendar
it's on, and the Events seen came from two calendars: the main one, and a Shared
Calendar.

## Still open

- Whether `calendar:events.read` alone is enough, including for the identity
  lookup.
- Whether a token carrying `calendar:all` (the CalDAV scope) is accepted by the
  REST API.
- Whether Standalone Accounts are served.
- How a Subscribed Calendar appears, if it does at all.
- Whether a Watched Event on a Shared Calendar comes back in the default
  listing. If it does, it would be reminded, and the plugin couldn't tell.
