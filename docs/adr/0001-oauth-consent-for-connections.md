# Connections are established by Yandex OAuth consent

A Connection is established by sending the user through Yandex's OAuth consent flow and storing the resulting access and refresh tokens; the plugin never handles a user's password. Yandex's own documentation points elsewhere — it describes CalDAV access via per-user App Passwords, and OAuth-over-CalDAV only for Yandex 360 Service Applications — so a reader would reasonably expect one of those instead.

## Considered options

- **App Password per user.** The only path Yandex documents for standalone accounts, and the shape the existing third-party `mm-yc-notify` project uses. Rejected: the credential is long-lived, unscoped, revocable only by the user in Yandex ID, and must be pasted into Mattermost and then encrypted at rest — making the plugin's KV store a high-value target.
- **Yandex 360 Service Application.** No stored user secret and no per-user clicking. Rejected for v1: it requires Organization owner access and a paid tariff, excludes Standalone Accounts entirely, and issues 1-hour tokens with no refresh token. Kept on the roadmap for organizations wanting zero-touch onboarding.
- **OAuth consent (chosen).** One flow that serves Organizations and Standalone Accounts identically, a 1-year token with a refresh token, and no user secret in the plugin.

## Consequences

An early probe with `calendar:events.read` + `calendar:calendars.read` was rejected by CalDAV with 401; a broader calendar scope succeeded with the same token. The scopes that gate CalDAV are **not** the fine-grained read scopes, so the manifest must request the broad scope and the exact string must be pinned down before release. See [the probe findings](../research/caldav-probe-findings.md).

**The scope is broader than the feature set.** Probing isolated `calendar:all` as the only scope that opens CalDAV: `calendar:events.read` + `calendar:calendars.read` and `calendar:read_all` both return 401 with an otherwise-valid token. A read-only plugin must therefore request write-capable consent, which users and Organization administrators will see on the consent screen. This is a known and accepted cost of choosing OAuth over App Passwords.
