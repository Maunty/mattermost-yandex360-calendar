# Mattermost Yandex Calendar

A Mattermost plugin that surfaces a person's Yandex Calendar inside Mattermost: a Reminder shortly before each Event, and a once-daily list of that day's Events.

## Language

### People and accounts

**Organization**:
A Yandex 360 for Business organization. Its owner provisions the plugin's access to members' calendars.
_Avoid_: tenant, workspace, company, org

**Standalone Account**:
A Yandex account that belongs to no Organization.
_Avoid_: personal account, private account, free account

**Service Application**:
The Yandex 360 credential an Organization owner registers, which lets the plugin act for a member without that member's involvement.
_Avoid_: service account, app, integration credential

**Connection**:
The link between one Mattermost user and the Yandex Calendar the plugin reads for them, established by OAuth consent and held as a refreshable token.
_Avoid_: account, link, integration, binding

### Calendars and events

**Own Calendar**:
A calendar belonging to a single person, read for them through their Connection. A person may have several.
_Avoid_: personal calendar, my calendar, private calendar

**Subscribed Calendar**:
Someone else's calendar, or an external feed, that a person follows without taking part in its Events. Not an Own Calendar; out of scope, like a Shared Calendar.
_Avoid_: followed calendar, colleague's calendar

**Shared Calendar**:
A calendar readable by several people and surfaced to a group rather than an individual. Out of scope for v1.
_Avoid_: team calendar, room calendar, group calendar

**Event**:
A single scheduled occurrence in a calendar.
_Avoid_: meeting, appointment, entry, booking

**Watched Event**:
Someone else's Event that a person has put on their own calendar without being invited to it. They follow it but are not a participant.
_Avoid_: subscribed event, followed event, bookmarked event

**A person's Events**:
The Events a person organises, is invited to (required or optional), or watches, that they have not declined. Only these produce Reminders and appear in a Daily Summary. An Event marked as free time still counts.
_Avoid_: my meetings, relevant events

### What the plugin sends

**Reminder**:
A one-off message sent to one person shortly before one Event begins.
_Avoid_: notification, alert, ping, heads-up

**Daily Summary**:
The once-a-day message listing that person's Events for the day.
_Avoid_: agenda, daily agenda, digest, brief
