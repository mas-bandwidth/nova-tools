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

# WINDOWS_TIMEOUT LIVED HERE, and it is gone with the legs it bounded (Glenn
# 2026-09-18: "drop the native windows CI runners. WSL only from now on."). It
# was the per-package ceiling on the hosted Windows PR leg and the merge group's
# windows leg, 300 s, measured rather than carried over — the number and the
# measurements behind it (testdata/ci/package-sizes-windows.tsv, #1332,
# integration-4, run 35354900090) are in git at dev 65e86175 if a Windows leg
# ever comes back. What stays is `vet-windows` below: a cross-vet that needs no
# Windows machine and no ceiling at all.
#
# DARWIN_TIMEOUT is the per-package ceiling on the merge group's darwin leg, and
# it is a MEASUREMENT and not a convention carried over from
# another platform. That leg used the linux 100 s until 2026-09-18, when
# merge-group run 35369433950 (batch 7, PR #1360) had its darwin shards 0 and 1
# CANCELLED at the five-minute leg cap on superman.
#
# READ THAT RUN BEFORE BELIEVING THE OBVIOUS STORY, because the ceiling is not
# what killed it: no package came near 100 s, and what ran out was the SHARD'S SUM
# over 27 packages. The real fix is the shard COUNT coming from darwin's own
# measurements in testdata/ci/package-sizes-darwin.tsv, and this number is the
# companion to it — the bound on ONE `go test`, which is a shard's share of a
# dealt package or the WHOLE of a package when the group opened a single slot. The
# whole of the largest package on a loaded host is therefore the case it covers:
# cmd/nova-wake measures 120.3 s on a quiet superman, and 300 s is that with the
# stated margin and a little over. The leg's ten-minute job cap still fires above
# it, so a real hang is named by Go rather than by the runner killing the job.
#
# TWO NUMBERS MAKE IT AND BOTH ARE WRITTEN DOWN. The sizes in that table were read
# on a QUIET host — no runner busy, no merge group in flight — because the cancel
# happened on a machine in its post-power-on state, Spotlight and XprotectService
# still working and sixteen runners live, and a number read then is the state's
# number rather than the machine's. The measurement is therefore a FLOOR, and this
# ceiling is that floor times a STATED MARGIN of two. The margin is measured on
# the same host both ways, not chosen: cmd/nova-merge is 68.8 s whole on a quiet
# superman and about 147 s on the loaded superman of that run, which is 2.1x, and
# the same factor covers the unevenness of dealing tests by NAME instead of time. Neither number is a
# guess and neither is hidden inside the other; internal/ci's darwin class tests
# hold both.
DARWIN_TIMEOUT ?= 300s

# MERGE_TIMEOUT is the per-package ceiling on the merge group's legs. 100 s is
# the linux number and is what that leg has always used; the darwin leg exports
# MERGE_TIMEOUT=$(make -s darwin-timeout), so that platform's ceiling lives in ONE
# place — DARWIN_TIMEOUT above — instead of being written again in the workflow.
# `?=` is what makes that environment value win.
MERGE_TIMEOUT ?= 100s

.PHONY: help build fmt vet vet-windows lint test test-full test-short test-merge test-race test-e2e test-lisp check clean darwin-timeout

help:
	@echo "make help        this list"
	@echo "make build       go build ./..."
	@echo "make fmt         report files that are not gofmt-clean"
	@echo "make vet         go vet PKGS (default ./...)"
	@echo "make vet-windows GOOS=windows go vet ./... (the one Windows guard on the CL path)"
	@echo "make lint        fmt and vet"
	@echo "make test        go test -count=1 PKGS plus the 60s slowtests budget (the fast tier)"
	@echo "make test-full   go test -count=1 ./... (the whole tree)"
	@echo "make test-short  go test -short -count=1 -timeout 12m PKGS"
	@echo "make test-merge  go test -count=1 -timeout MERGE_TIMEOUT -run RUN PKGS"
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

# THE ONE WINDOWS GUARD ON THE CL PATH, since the native windows runners were
# dropped (Glenn 2026-09-18: "drop the native windows CI runners. WSL only from
# now on."). `GOOS=windows go vet ./...` builds the Windows standard library
# into the cache and then type-checks every package AND every _test.go for
# Windows — which is what catches the class a cross-platform Go tree actually
# breaks: a *_windows.go that stopped compiling, a syscall used without a build
# tag, a constant that only exists on unix. It runs on a Linux runner in seconds
# and needs no Windows machine.
#
# ALWAYS ./..., never $(PKGS): a shard of the tree cross-vetted is not the
# guard. It is the whole tree or it is nothing, and it is cheap enough to be the
# whole tree. CGO_ENABLED=0 because there is no Windows C toolchain here and
# none is wanted; GOARCH=amd64 is the platform the release builds.
#
# It is the same step nova-merge's batch gate runs under the name `vet-windows`
# (cmd/nova-merge/batch.go, docs/SPEC-MERGE.md edge 25), so a GOOS=windows break is
# refused by the gate before the batch and by ci.yml's lint job on the PR.
vet-windows:
	GOOS=windows GOARCH=amd64 CGO_ENABLED=0 $(GO) vet ./...

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

# `test-pr` LIVED HERE, the sharded hosted PR leg's entry, and it went with
# test-windows-pr on 2026-09-18: it had exactly one caller, and the caller is
# gone. The hosted PR leg that remains — test-hosted-pr's Linux sandbox entry —
# runs `make test-short`, which it always did.

# The merge-group hosted legs: full tests (no -short) for the packages the group
# changes, one shard at a time. RUN is the shard's test-name regex; empty means
# every test in PKGS. MERGE_TIMEOUT is the per-package ceiling — 100 s for linux,
# and DARWIN_TIMEOUT on the darwin leg, which exports it.
test-merge:
	$(GO) test -count=1 -timeout $(MERGE_TIMEOUT) -run "$(RUN)" $(PKGS)

# darwin-timeout is the interface for the darwin merge leg: the workflow takes
# the ceiling FROM HERE rather than carrying a second copy of the number. One target, one
# number, read by the workflow: a ceiling written twice is a ceiling that drifts.
darwin-timeout:
	@echo $(DARWIN_TIMEOUT)

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
