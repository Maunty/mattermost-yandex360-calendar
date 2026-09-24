# Spike: is there a REST calendar API behind the fine-grained scopes?

**Date:** 2026-09-24. **Ticket:** [01](../../.scratch/yandex-calendar-v1/issues/01-spike-locate-rest-api.md).
**Verdict: no usable REST calendar API could be located. CalDAV is confirmed as
the transport, and recurrence must be expanded client-side.**

This settles the question the ticket was time-boxed to answer, though not by
every route it listed. What was and was not done is set out below, because a
negative result is only worth anything if its limits are stated.

## Why the question was asked

Yandex publishes eleven fine-grained `calendar:*` scopes — `events.read`,
`calendars.read`, `my_settings.read`, `free_busy.read`, `resources.read` and
their write counterparts. Something consumes them, and
[probe 4](caldav-probe-findings.md) proved it is not CalDAV: those scopes are
rejected by CalDAV with 401 while `calendar:all` succeeds with the same token.

If a REST API existed it would almost certainly expand recurrences server-side,
the way Microsoft Graph's `calendarView` and Google's `singleEvents=true` do.
That would delete the single largest piece of work in this project.

## What was checked

### 1. The Yandex 360 API reference — no calendar service

The reference at <https://yandex.ru/dev/api360/doc/ru/ref/index.html> documents
twenty services as of today:

> Антиспам, Общие и делегированные ящики, Разрешенные и запрещенные
> отправители, Правила обработки писем, Настройки почты сотрудников,
> Подразделения, DNS, Домены, Общие контакты, Группы, Группы v2, Организации,
> Сотрудники, Аудит-лог, Двухфакторная аутентификация v1, Двухфакторная
> аутентификация v2, Управление паролями, Настройки авторизации, Запрет на
> авторизацию во внешних OAuth-сервисах, Сервисные приложения.

**No calendar service is listed.** The earlier research file left open the
possibility that one had appeared since it was written; it has not.

This matters more than it looks. Probe 2 established that `api360.yandex.net`
does route real APIs and returns 403 rather than 404 for a route that exists
but is not authorised — so the 404s on every calendar path probed there were
genuine absence. The reference now confirms that absence is by design and not
an undocumented endpoint.

### 2. `yandex.ru/dev/calendar/` — gone

Returns 404. There is no developer documentation for Yandex Calendar at the
conventional location.

### 3. Third-party implementations — all CalDAV

An independent IntelliJ IDEA plugin for Yandex Calendar, published in January
2026, reads calendars over **CalDAV with an app password over HTTP Basic**,
issuing `REPORT` with a time-range filter and parsing iCalendar with `ical4j`.
It names `https://caldav.yandex.ru/calendars/${email}/events-default` as its
endpoint.

Two things follow. First, an independent developer solving the same problem
found no REST API either, which is weak evidence but evidence. Second, that
plugin hardcodes `events-default` — the collection name from Yandex's own
documentation that
[probe 1 proved does not exist on real accounts](caldav-probe-findings.md).
Every account observed carries `events-<numeric-id>` instead. That plugin
presumably works only for whoever tested it, and it is exactly the mistake this
project's ticket 04 was written to avoid.

## Halted pending access

**2026-09-24: the Yandex API is in prereview and access has been requested.** The routes below
that were not taken are suspended until it arrives, along with the description that comes with
it — which may answer the question on its own and make the rest unnecessary.

This does not put the verdict in doubt for now. Everything built on it is finished and works.
If the prereview description turns out to name a calendar surface that expands recurrences
server-side, that is a simplification to weigh later against working code.

## What was not done, and why

The ticket listed three approaches. The cheapest one — reading the Yandex
Calendar web client's network traffic in browser developer tools, and then
trying whatever hosts it calls with a token carrying the fine-grained scopes —
**was not attempted**, because it needs a browser signed in to a live Yandex
account and an OAuth token for that account, neither of which was available to
the agent doing this work.

That is the one approach that could still overturn this verdict. If someone
with an account spends fifteen minutes on it, the two things worth recording
are:

1. Which hosts and path shapes the web client calls. Whatever it is, it is the
   real surface.
2. Whether a token carrying `calendar:events.read` is accepted there. The web
   client may well use an internal session-authenticated API that OAuth scopes
   do not reach, in which case the scopes gate something else again and the
   answer is still no.

Until then this verdict rests on the absence of any documented or conventional
endpoint, confirmed twice, plus probe 2's finding that the obvious candidates
return genuine 404s.

## Consequence

None of the design changes. Specifically:

- **CalDAV remains the transport.** No ADR is written, because nothing
  supersedes [ADR 0001](../adr/0001-oauth-consent-for-connections.md).
- **Recurrence is expanded client-side**, as ticket 05 assumed. This is the
  largest piece of work in the project and it stays.
- The scope stays `calendar:all`, with the consequences documented in
  [the administrator guide](../admin-setup.md#why-a-read-only-plugin-asks-for-write-access).

## Sources

- [Yandex 360 API reference index](https://yandex.ru/dev/api360/doc/ru/ref/index.html)
- [API Яндекс 360 для бизнеса](https://yandex.ru/dev/api360/)
- [Разработка плагина для интеграции Яндекс-Календаря с IntelliJ IDEA (Habr)](https://habr.com/ru/articles/875464/)
- [Сервисные приложения (Yandex 360 support)](https://yandex.ru/support/yandex-360/business/admin/en/security-service-applications)
