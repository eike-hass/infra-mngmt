# infra-mngmt Makefile.
#
# All targets work both inside the devcontainer (where golangci-lint is
# pre-installed via the Dockerfile) and on a host where the dev has installed
# the same tools.
#
# Standard flow before committing: `make check` (fmt-clean + vet + lint + test).

GO    ?= go
BIN   := dist/infra-mngmt
PKG   := ./...

# Stamp the build with a Unix epoch so the running binary can report when it
# was compiled — used to confirm deploys actually picked up new code.
BUILD_EPOCH := $(shell date +%s)
LDFLAGS := -X main.buildEpoch=$(BUILD_EPOCH)

# Windows-side wake-proxy binary. Cross-compiled from this devcontainer
# into the host's %USERPROFILE%\.config\infra-mngmt directory (bind-mounted
# at /windows-config) so Windows process-compose can supervise it. Not part
# of `make build` because the artifact is Windows-only.
WAKE_PROXY_OUT ?= /windows-config/wake-proxy.exe

.PHONY: build build-wake-proxy test test-cover fmt fmt-check vet lint check tidy clean run

## build: compile the server binary into dist/
build:
	$(GO) build -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/infra-mngmt

## build-wake-proxy: cross-compile the Windows-side wake-proxy.exe
build-wake-proxy:
	GOOS=windows GOARCH=amd64 $(GO) build -o $(WAKE_PROXY_OUT) ./cmd/wake-proxy

## run: build and start the server (re-run after each code change)
run: build
	./$(BIN)

## test: run the full test suite
test:
	$(GO) test $(PKG)

## test-cover: run tests with per-package coverage summary
test-cover:
	$(GO) test -cover $(PKG)

# gofmt has no native ignore-list — walk the tree ourselves and skip vendored
# third-party clones under external/ (e.g. the process-compose upstream fork
# kept here for bug-fix verification; its files are governed by upstream
# style, not ours).
GOFMT_FILES := $(shell find . -name '*.go' -not -path './external/*' -not -path './vendor/*')

## fmt: rewrite all Go files with gofmt
fmt:
	@gofmt -w $(GOFMT_FILES)

## fmt-check: fail if any Go file needs reformatting (CI/pre-commit gate)
fmt-check:
	@unformatted=$$(gofmt -l $(GOFMT_FILES)); \
	if [ -n "$$unformatted" ]; then \
		echo "gofmt: the following files need formatting:"; \
		echo "$$unformatted"; \
		exit 1; \
	fi

## vet: run `go vet` (subset of staticcheck-class checks built into the toolchain)
vet:
	$(GO) vet $(PKG)

## lint: run golangci-lint with the project config (.golangci.yml)
# Prefer a user-local install in $HOME/go/bin if present (newer than the system
# binary in /usr/local/bin until the devcontainer is rebuilt).
lint:
	@if [ -x "$$HOME/go/bin/golangci-lint" ]; then \
		"$$HOME/go/bin/golangci-lint" run; \
	else \
		golangci-lint run; \
	fi

## check: full pre-commit gate — format, vet, lint, test
check: fmt-check vet lint test

## tidy: clean up the module graph
tidy:
	$(GO) mod tidy

## vendor-codemirror: regenerate internal/web/static/vendor/codemirror.bundle.js
##                    from upstream npm packages. Network: registry.npmjs.org.
##                    Re-run after bumping CodeMirror; commit the resulting bundle.
##                    The tool itself is not part of the production binary —
##                    `make build` only compiles ./cmd/infra-mngmt.
vendor-codemirror:
	$(GO) run ./cmd/vendor-codemirror

## clean: remove build artifacts and lint cache
clean:
	rm -rf $(BIN)
	golangci-lint cache clean 2>/dev/null || true

## help: list available targets and their descriptions
help:
	@grep -E '^## ' Makefile | sed 's/## /  /' | sort
