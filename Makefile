GO ?= go
PLUGIN_ID = $(shell sed -n 's/^[[:space:]]*"id"[[:space:]]*:[[:space:]]*"\(.*\)",/\1/p' plugin.json)
PLUGIN_VERSION = $(shell sed -n 's/^[[:space:]]*"version"[[:space:]]*:[[:space:]]*"\(.*\)",/\1/p' plugin.json)
BUNDLE_NAME = $(PLUGIN_ID)-$(PLUGIN_VERSION).tar.gz

HOST_OS = $(shell $(GO) env GOOS)
HOST_ARCH = $(shell $(GO) env GOARCH)

# plugin.json declares an executable per platform, and the server refuses to
# run the plugin on a platform whose executable is missing from the bundle. A
# release therefore needs `dist-all`; day-to-day work only needs this machine,
# because cross-compiling five targets is minutes of work for no answer.
PLATFORMS = linux-amd64 linux-arm64 darwin-amd64 darwin-arm64 windows-amd64

.PHONY: all
all: check test

## build compiles the plugin for this machine only. This is the one to run
## while working: it answers "does it compile" in seconds.
.PHONY: build
build:
	$(GO) build -trimpath -o server/dist/plugin-$(HOST_OS)-$(HOST_ARCH) ./server

## build-all cross-compiles every platform the manifest declares. Slow, and
## only needed to cut a release.
.PHONY: build-all
build-all:
	@set -e; for platform in $(PLATFORMS); do \
		os=$${platform%-*}; arch=$${platform#*-}; \
		suffix=""; [ "$$os" = "windows" ] && suffix=".exe"; \
		echo "building $$platform"; \
		GOOS=$$os GOARCH=$$arch $(GO) build -trimpath -o server/dist/plugin-$$platform$$suffix ./server; \
	done

## dist packages what has already been built into the bundle an administrator
## uploads. Run build-all first for a release.
.PHONY: dist
dist:
	@test -n "$$(ls server/dist/plugin-* 2>/dev/null)" || \
		{ echo "Nothing built yet: run 'make build' or 'make build-all' first."; exit 1; }
	rm -rf dist/$(PLUGIN_ID)
	mkdir -p dist/$(PLUGIN_ID)/server/dist dist/$(PLUGIN_ID)/assets
	cp plugin.json dist/$(PLUGIN_ID)/
	cp -r assets/. dist/$(PLUGIN_ID)/assets/
	cp server/dist/plugin-* dist/$(PLUGIN_ID)/server/dist/
	cd dist && tar -czf $(BUNDLE_NAME) $(PLUGIN_ID)
	@echo "Built dist/$(BUNDLE_NAME)"

## release is the full, slow path: every platform, then the bundle.
.PHONY: release
release: check test build-all dist

.PHONY: test
test:
	$(GO) test ./... -count=1

.PHONY: check
check:
	$(GO) vet ./...
	@test -z "$$(gofmt -l .)" || { echo "These files need formatting:"; gofmt -l .; exit 1; }

.PHONY: clean
clean:
	rm -rf dist server/dist

# --- A server to develop against --------------------------------------------
# See dev/README.md. The stack is Mattermost plus PostgreSQL, and it can run on
# a different machine from the one you build on: everything below except the
# dev-* targets talks to it over HTTP.

ENV_FILE = dev/.env
COMPOSE = docker compose --env-file $(ENV_FILE) -f dev/compose.yml

# Depending on this gives a useful message instead of a compose parse error.
$(ENV_FILE):
	@echo "$(ENV_FILE) is missing. Copy the example and edit it:"; \
	 echo "    cp dev/.env.example $(ENV_FILE)"; \
	 exit 1

## dev-up starts the server. First start runs migrations and takes a minute.
.PHONY: dev-up
dev-up: $(ENV_FILE)
	$(COMPOSE) up -d
	@set -a; . ./$(ENV_FILE); set +a; \
	 echo "Mattermost is starting at $${MM_SITE_URL:-http://localhost:8065}"; \
	 echo "Next: make dev-admin, then make deploy"

## dev-down stops the server and keeps its data.
.PHONY: dev-down
dev-down: $(ENV_FILE)
	$(COMPOSE) down

## dev-destroy stops the server and deletes its data, including the database,
## the uploaded plugin and the server config.
.PHONY: dev-destroy
dev-destroy: $(ENV_FILE)
	$(COMPOSE) down -v

.PHONY: dev-logs
dev-logs: $(ENV_FILE)
	$(COMPOSE) logs -f

## dev-admin creates the first system admin and a team to put it in, through
## the container's local socket, because there is no admin yet to authenticate
## as. Safe to re-run: it says so if they already exist.
.PHONY: dev-admin
dev-admin: $(ENV_FILE)
	@set -a; . ./$(ENV_FILE); set +a; \
	 : "$${MM_ADMIN_USERNAME:?set MM_ADMIN_USERNAME in $(ENV_FILE)}"; \
	 : "$${MM_ADMIN_PASSWORD:?set MM_ADMIN_PASSWORD in $(ENV_FILE)}"; \
	 echo "==> Creating $$MM_ADMIN_USERNAME"; \
	 $(COMPOSE) exec -T mattermost mmctl --local user create \
		--email "$${MM_ADMIN_EMAIL:-$$MM_ADMIN_USERNAME@example.com}" \
		--username "$$MM_ADMIN_USERNAME" \
		--password "$$MM_ADMIN_PASSWORD" \
		--system-admin || echo "    (already exists — carrying on)"; \
	 echo "==> Creating the team"; \
	 $(COMPOSE) exec -T mattermost mmctl --local team create \
		--name dev --display-name "Dev" --private || echo "    (already exists — carrying on)"; \
	 $(COMPOSE) exec -T mattermost mmctl --local team users add dev "$$MM_ADMIN_USERNAME" \
		|| echo "    (already a member — carrying on)"; \
	 echo "Sign in at $${MM_SITE_URL:-http://localhost:8065} as $$MM_ADMIN_USERNAME"

## deploy builds the bundle and installs it on the server, wherever it is.
.PHONY: deploy
deploy: build dist
	./dev/deploy.sh
