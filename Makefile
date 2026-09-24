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
