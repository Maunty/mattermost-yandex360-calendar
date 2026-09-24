# A Mattermost server to develop against

The plugin runs inside Mattermost, so there is a limit to what the test suite
can tell you. This is a real server in the 11 series with a real database, so
the bundle can actually be installed and the consent flow actually walked
through.

**This is a development server.** Plain HTTP, an unauthenticated admin socket
inside the container, plugin signature checks off, email verification off, a
default database password. It is fine on a machine you control and on a network
you trust, and it is not fine anywhere else.

It wants about **2 GB of memory** across the containers, and Mattermost takes a
minute or two to run its migrations the first time.

## The usual way: in the dev container

`.devcontainer/` composes this stack together with a workspace container, so
opening the repo gives you the toolchain and a server to deploy into at once.

Open the repo in VS Code and choose **Reopen in Container**, or:

```sh
devcontainer up --workspace-folder .
```

Then, from a terminal inside it:

```sh
make dev-admin    # the first system admin, and a team
make deploy       # build, package, upload, enable
```

No `dev/.env` is needed — everything has a working default. Mattermost is on
port 8065, forwarded to your browser.

Inside the container, `MM_SERVER_URL` is already set to `http://mattermost:8065`,
the service name on the compose network, because that is how one container
reaches another. Your browser still uses `MM_SITE_URL`. Those two being
different is normal and is the point of having both.

## The other way: the server on its own

A machine that only hosts the server does not need the dev container:

```sh
cp dev/.env.example dev/.env   # optional; set MM_SITE_URL if not localhost
make dev-up
make dev-admin
```

And then deploy to it from wherever you build, which does not have to be that
machine:

```sh
MM_SERVER_URL=http://dev-box.lan:8065 make deploy
```

## Pointing it at Yandex

[docs/admin-setup.md](../docs/admin-setup.md) is the real guide and is written
for administrators. The short version:

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
`MM_LOG_LEVEL=DEBUG` is what makes it visible, and it is the default here.

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

**Yandex will not accept the redirect URI.** It may refuse a plain `http://`
callback for a host that is not localhost. If it does, the options are putting
something that terminates TLS in front, or tunnelling to it. This compose file
does neither, deliberately: guessing at your TLS setup would be worse than
leaving it to you.

**`make deploy` says the server never answered.** `MM_SERVER_URL` is where the
*scripts* reach the server, which is not always the same string as
`MM_SITE_URL`, where a *browser* reaches it. Inside the dev container the first
is a service name and the second is a host address.

**`make dev-admin` says the account exists but cannot sign in.** The account
predates the password now in `dev/.env`. Either use the real one, or
`make dev-destroy` and start clean. If you would rather keep the data, the
container has mmctl and a local admin socket:

```sh
docker compose -f dev/compose.yml exec mattermost \
  mmctl --local user change-password admin --password 'NewPassword123!'
```

**Mattermost will not start.** First start runs migrations and needs the
database up; compose waits for Postgres to pass its health check, but a machine
short on memory can still have it killed. `make dev-logs` says which.
