# Events are read over Yandex's REST calendar API; CalDAV is kept as a fallback

Status: accepted, 2026-09-25

The plugin reads a person's Events through Yandex's REST calendar API, which is currently in early access, not over CalDAV. This reverses the verdict of [the REST API spike](../research/rest-api-spike.md), which found no such API before early access was granted. The API expands recurring Events on the server and accepts a read-only scope. That removes the largest piece of client-side work and lets the consent screen finally match what the plugin does.

## Considered options

- **Stay on CalDAV until the API is public.** Rejected because the gains are too large to wait for, and the CalDAV code is kept anyway.
- **Let the administrator choose the transport.** Rejected because it makes every admin think about a safety net that exists only for the maintainers.
- **REST, with CalDAV kept in the tree behind the same seam (chosen).** The CalDAV client, its fake server and client-side recurrence stay compiled and tested but are not wired in. Switching back is a code change and a release.

## Consequences

- **The requested scope is read-only.** This supersedes the scope consequence of [ADR 0001](0001-oauth-consent-for-connections.md). Switching back to CalDAV would need the write-capable scope again, which means every Connection would have to be re-consented. That cost is accepted while early access has no users but the maintainers.
- **Early access is gated per OAuth application.** Until the API is generally available, a self-hosted administrator cannot simply register their own application and expect it to work.
- **Standalone Accounts may be lost.** If the API turns out to serve only Organizations, Standalone Accounts are dropped rather than served over CalDAV per person, because running two transports side by side doubles every failure path.
- **There is no change token.** Each poll reads the whole cached window, not just what changed.
