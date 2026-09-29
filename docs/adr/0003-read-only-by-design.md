# The plugin only ever reads calendars

Status: accepted, 2026-09-29. This records a decision in force since v1.

The plugin reads a person's Events and sends them messages about them. It never creates, changes or deletes anything in anyone's calendar, and that includes responding to invitations. This is the promise an administrator is asked to trust. So it is kept in two places that do not depend on each other: the permission the plugin asks for, and the shape of its calendar client.

## Considered options

- **Read and write, used sparingly.** For example, accept an invitation from a Reminder. This was rejected. Every feature built on writing makes the consent screen ask for more than reading, and one bug can then change somebody's calendar. A plugin that can write is also far harder for a Yandex 360 administrator to approve.
- **Write behind a setting an administrator turns on.** This was rejected. The permission has to be requested up front, so the consent screen would ask for write access even on servers that never turn the setting on.
- **Read only (chosen).** The only permission requested is `calendar:events.read`. The calendar client has no method that writes, and a test fails if the client ever sends anything other than a read of Events.

## Consequences

- **Responding to invitations is out.** Accepting or declining means changing the person's copy of an Event, which is a write.
- **Adding a write is a deliberate, visible change.** It needs a new client method, a change to the read-only test, a broader scope in the admin guide, and every person to reconnect. That is the intended cost.
- **Falling back to CalDAV breaks the permission half of the promise.** Yandex's CalDAV service refuses every read-only scope, so the fallback would have to ask for `calendar:all` ([ADR 0002](0002-rest-api-replaces-caldav.md), [CalDAV probe findings](../research/caldav-probe-findings.md)). Even then, the code would still only read.
