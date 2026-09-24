# A Mattermost server to develop against

The plugin runs inside Mattermost, so there is a limit to what the test suite
can tell you. This brings up a real server in the 11 series with a real
database, so the bundle can actually be installed and the consent flow actually
walked through.

**This is a development server.** Plain HTTP, an unauthenticated admin socket
inside the container, plugin signature checks off, email verification off. It
is fine on a machine you control and on a network you trust, and it is not fine
anywhere else.

It wants about **2 GB of memory** between the two containers, and Mattermost
takes a minute or so to run its migrations on first start.

## Getting it up

```sh
cp dev/.env.example dev/.env
$EDITOR dev/.env          # at minimum: POSTGRES_PASSWORD and MM_SITE_URL
make dev-up
```

Then create the first account. Mattermost has a chicken-and-egg problem here —
you need an admin to do anything and there is no admin yet — so this goes
through the container's local socket, which needs no credentials:

```sh
make dev-admin
```

That creates a system admin from `MM_ADMIN_USERNAME` / `MM_ADMIN_PASSWORD` in
`dev/.env`, and a team to put it in. Sign in at whatever you set `MM_SITE_URL`
to.

## Getting the plugin in

```sh
make deploy
```

Builds for this machine, packages the bundle, uploads it and enables it. It
talks to the server over HTTP, so the server does not have to be on the same
machine as the build — point `MM_SERVER_URL` at it.

Repeat it whenever you change the code. The upload replaces what is there and
Mattermost restarts the plugin.

## Pointing it at Yandex

[docs/admin-setup.md](../docs/admin-setup.md) is the real guide and is written
for administrators, not just for this. The short version:

1. Register an application at <https://oauth.yandex.ru/client/new>, as a web
   service, with the scope **`calendar:all`** — and read
   [why that scope](../docs/admin-setup.md#why-a-read-only-plugin-asks-for-write-access),
   because it is the surprising part.
2. Register the redirect URI **exactly**:
   `<MM_SITE_URL>/plugins/yandex-calendar/oauth/complete`
3. Put the client ID and secret into the System Console, under
   **Plugins → Yandex Calendar**.

Then, in any channel, `/yacal connect`.

## Watching what it does

```sh
make dev-logs
```

The plugin logs what it skipped and why — an event it could not read, a
recurrence rule it could not expand, a provider that refused it. None of that
is ever shown to a person, by design, so the log is the only place it exists.
`MM_LOG_LEVEL=DEBUG` in `dev/.env` is what makes it visible.

## Starting over

```sh
make dev-down          # stop, keep the data
make dev-destroy       # stop and delete the volumes
```

`dev-destroy` throws away the database, the uploaded plugin and the server
config. It is the fastest way back to a server that has never seen this plugin,
which is worth doing before believing that a fresh install works.

## When it goes wrong

**Connecting works until the last step, then fails.** Almost always
`MM_SITE_URL`. The plugin builds its redirect from it, and Yandex refuses a
callback that is not character-for-character what the application has
registered. `localhost` when the server is on another machine is the usual
version of this.

**Yandex will not accept the redirect URI.** Yandex may refuse a plain `http://`
callback for a non-localhost host. If it does, the options are running this
behind something that terminates TLS, or tunnelling to it — this compose file
does not do either, deliberately: guessing at your TLS setup would be worse
than leaving it to you.

**`make deploy` says the server never answered.** `MM_SERVER_URL` is where the
*deploy script* reaches the server, which is not always the same string as
`MM_SITE_URL` — the latter is where a *browser* reaches it.

**Mattermost will not start.** First start runs migrations and needs the
database to be up; compose waits for Postgres to pass its health check, but a
machine short on memory can still have it killed. `make dev-logs` says which.

**Sign-in fails during `make deploy`.** The account does not exist yet. Run
`make dev-admin`.
