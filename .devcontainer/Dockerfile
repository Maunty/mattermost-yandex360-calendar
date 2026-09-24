# The tag tracks the `go` directive in go.mod. Go's own toolchain mechanism
# (GOTOOLCHAIN, left at its default of `auto`) fetches the exact patch release
# go.mod asks for if this image ships an older one, so go.mod stays the single
# source of truth for which compiler builds this plugin.
FROM mcr.microsoft.com/devcontainers/go:1.26-bookworm

# The repo drives everything through make. The base image is not guaranteed to
# carry it, and finding out at `make test` is a worse time to find out.
RUN apt-get update \
    && apt-get install -y --no-install-recommends make \
    && apt-get clean \
    && rm -rf /var/lib/apt/lists/*
