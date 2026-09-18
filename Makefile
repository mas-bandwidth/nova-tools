# The one entry for build, test and lint. CI and the worker cards call these
# same targets, so the shell never drifts between them: a flag tightened here is
# tightened in every leg, and a card that runs `make test` runs exactly what the
# CL tier runs. The fast tier is the default `test`; the full tier is
# `test-full`; `check` is what CI runs on a pull request.
#
# PKGS is the package set. It defaults to ./... and every caller may narrow it
# with `make test PKGS=./cmd/nova-bus ./internal/bus`, which is how the sharded
# CI legs hand their shard to the same target.

GO ?= go
PKGS ?= ./...
CL_PKGS := ./cmd/... ./internal/...

.PHONY: help build fmt vet lint test test-full test-short test-merge test-race test-e2e test-lisp check clean

help:
	@echo "make help        this list"
	@echo "make build       go build ./..."
	@echo "make fmt         report files that are not gofmt-clean"
	@echo "make vet         go vet PKGS (default ./...)"
	@echo "make lint        fmt and vet"
	@echo "make test        go test -count=1 PKGS plus the 60s slowtests budget (the fast tier)"
	@echo "make test-full   go test -count=1 ./... (the whole tree)"
	@echo "make test-short  go test -short -count=1 -timeout 12m PKGS"
	@echo "make test-merge  go test -count=1 -timeout 100s -run RUN PKGS"
	@echo "make test-race   go test -race ./... (the certification tier)"
	@echo "make test-e2e    go test -count=1 -run TestFriendSequence ./cmd/..."
	@echo "make test-lisp   ./lisp/nova-work/run-tests.sh"
	@echo "make check       build, lint, test, test-e2e and test-lisp (what CI runs)"
	@echo "make clean       remove ./bin and ./scratch"

build:
	$(GO) build ./...

fmt:
	@unformatted="$$(gofmt -l .)"; \
	if [ -n "$$unformatted" ]; then \
		echo "not gofmt-clean:"; \
		echo "$$unformatted"; \
		exit 1; \
	fi

vet:
	$(GO) vet $(PKGS)

lint: fmt vet

# The fast tier. The package set is the one ci.yml's test-packages job reads out
# of the source with `go list ./cmd/... ./internal/...`, minus the darwin-only
# packages that only the studio legs carry; a caller that wants a shard passes
# PKGS explicitly.
#
# The run is teed through `-json` so cmd/nova-ci's slowtests verb reads the
# stream once and reports every package over the budget, and the same bytes
# stay on disk if a reader wants them. The pipeline is under `pipefail` and the
# go test status is carried out, so a red test still fails the target even
# though slowtests runs after it — a failing go test stops the verdict before
# slowtests, exactly as the workflow did when this lived inline in ci.yml.
#
# The budget is 60 s (the two-minute law) except, until cards 9352 to 9354 make
# cmd/nova-swarm, cmd/nova-pulse and internal/swarm fast, on the Intel Mac
# benches (about 3x slower per core than the Studio), where the alert still
# prints every CI-SLOW line but the budget is 300 s. Dated exception,
# 2026-09-18; remove with those cards.
test: PKGS := $(CL_PKGS)
test:
	@bash -o pipefail -c 'budget=60; case "$$(uname -m)" in x86_64) [ "$$(uname -s)" = Darwin ] && budget=300;; esac; GOFLAGS=-json $(GO) test -count=1 $(PKGS) | tee "$${RUNNER_TEMP:-$${TMPDIR:-/tmp}}/test.json"; status=$${PIPESTATUS[0]}; $(GO) run ./cmd/nova-ci slowtests --budget "$$budget" < "$${RUNNER_TEMP:-$${TMPDIR:-/tmp}}/test.json"; exit $$status'

test-full:
	$(GO) test -count=1 $(PKGS)

test-short:
	$(GO) test -short -count=1 -timeout 12m $(PKGS)

# The merge-group hosted leg: full tests (no -short) for the packages the group
# changes, one shard at a time. RUN is the shard's test-name regex; empty means
# every test in PKGS, and -timeout 100s is the per-package ceiling.
test-merge:
	$(GO) test -count=1 -timeout 100s -run "$(RUN)" $(PKGS)

test-race:
	$(GO) test -race $(PKGS)

test-e2e:
	$(GO) test -count=1 -run TestFriendSequence ./cmd/...

test-lisp:
	./lisp/nova-work/run-tests.sh

# What CI runs on a pull request: the self-hosted lint job, the sharded test
# job, the friend sequences and the nova-work acceptance suite.
check: build lint test test-e2e test-lisp

# An explicit list, never a computed path: clean removes the two directories a
# local build and a worker's notes land in, and nothing else.
clean:
	rm -rf ./bin ./scratch
