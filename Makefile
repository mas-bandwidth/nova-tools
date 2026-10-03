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
# CL_PKGS IS THE LIVING TREE, read the way ci.yml's test-packages job reads it:
# `go run ./tools/ci select-packages --all` lists every package under cmd/,
# internal/ and tools/ and drops the ones internal/pkgselect/DEPRECATED names (a
# deprecated package is never tested), or fails loudly when `go list` fails; it
# never selects nothing in silence. Recursive (`=`), and the `test` and
# `test-functional` lines that take it are recursive too, so the go list runs
# when one of those targets runs and not on every make call; `make test
# PKGS=<shard>` never runs it.
CL_PKGS = $(shell $(GO) run ./tools/ci select-packages --all)

# THE HOST GUARD, on for every target. internal/testguard makes every ssh, scp
# and rsync seam in this tree panic with its command line when this is 1, so a
# unit test that constructs production code and injects no fake refuses HERE
# instead of reaching a bench. It is exported once, at the top, rather than per
# target: a tier that forgot it would be the one tier where a test can reach the
# fleet, and ci.yml reaches every tier through make (the `make` class test), so
# one line covers the whole CI path. A test that installs its own fake ssh on
# PATH declares it with testguard.AllowHosts(). The rule that keeps the seams
# honest is TestNoTestReachesAHostThroughAnUnfakedSeam in internal/ci.
export NOVA_TEST_NO_HOST := 1

# WINDOWS_TIMEOUT is gone with the legs it bounded. It was the per-package
# ceiling on the hosted Windows PR leg and the merge group's windows leg,
# 300 s, measured rather than carried over — the number and the measurements
# behind it (testdata/ci/package-sizes-windows.tsv, integration-4, run
# 35354900090) are in git at dev 65e86175 if a Windows leg
# ever comes back. What stays is `vet-windows` below: a cross-vet that needs no
# Windows machine and no ceiling at all.
#
# DARWIN_TIMEOUT is the per-package ceiling on the merge group's darwin leg, and
# it is a MEASUREMENT and not a convention carried over from another platform.
# That leg used the linux 100 s until merge-group run 35369433950 (batch 7) had
# its darwin shards 0 and 1 CANCELLED at the five-minute leg cap on a macOS runner.
#
# READ THAT RUN BEFORE BELIEVING THE OBVIOUS STORY, because the ceiling is not
# what killed it: no package came near 100 s, and what ran out was the SHARD'S SUM
# over 27 packages. The real fix is the shard COUNT coming from darwin's own
# measurements in testdata/ci/package-sizes-darwin.tsv, and this number is the
# companion to it — the bound on ONE `go test`, which is a shard's share of a
# dealt package or the WHOLE of a package when the group opened a single slot. The
# whole of the largest package on a loaded host is therefore the case it covers:
# cmd/nova-wake measures 120.3 s on a quiet macOS runner; 300 s is that with the
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
# macOS runner and about 147 s on the loaded one of that run, which is 2.1x, and
# the same factor covers the unevenness of dealing tests by NAME instead of time. Neither number is a
# guess and neither is hidden inside the other; internal/ci's darwin class tests
# hold both.
DARWIN_TIMEOUT ?= 110s

# MERGE_TIMEOUT is the per-package ceiling on the merge group's legs. 100 s is
# the linux number and is what that leg has always used; the darwin leg exports
# MERGE_TIMEOUT=$(make -s darwin-timeout), so that platform's ceiling lives in ONE
# place — DARWIN_TIMEOUT above — instead of being written again in the workflow.
# `?=` is what makes that environment value win.
MERGE_TIMEOUT ?= 100s

.PHONY: help build fmt vet vet-functional vet-slow vet-shippedsmoke vet-novadisk vet-laws vet-windows lint preflight test test-full test-short test-slow test-functional test-functional-container test-merge test-race test-e2e test-prewarm-done compile-lisp test-lisp check clean darwin-timeout map new-rule new-verb

help:
	@echo "make tlc         bounded Linux TLC group (TLC_JAR, TLC_OUT, TLC_GROUP)"
	@echo "make tlc-full    manual Linux TLC experiment (also explicit TLC_BUDGET; forbidden in CI)"
	@echo "make tlc-groups  JSON list of required model groups"
	@echo "make tlc-test    the runner and checker tests: no Java, no Redis, no network"
	@echo "make help        this list"
	@echo "make build       go build ./..."
	@echo "make fmt         report files that are not gofmt-clean"
	@echo "make vet         go vet PKGS (default ./...)"
	@echo "make vet-functional go vet -tags functional PKGS (the redis-backed test files compiled too)"
	@echo "make vet-slow go vet -tags slow PKGS (the nightly tier's test files compiled on every change)"
	@echo "make vet-shippedsmoke go vet -tags shippedsmoke ./internal/shippedsmoke (the shipped binary's smoke tests compiled on every change)"
	@echo "make vet-novadisk GOOS=darwin go vet -tags novadisk ./cmd/nova-sandbox (the one real-disk e2e test compiled on every change)"
	@echo "make vet-laws    build tools/analyzers/cmd/vetlaw and vet ./cmd/... with it"
	@echo "make vet-windows GOOS=windows go vet ./... (the one Windows guard on the CL path)"
	@echo "make lint        fmt and vet"
	@echo "make preflight   gofmt, go vet, and go test -count=1 (PKGS)"
	@echo "make test        the unit tier: go test -p GOTEST_P PKGS plus the 2 s package / 1 s test slowtests budgets"
	@echo "make test-functional the functional tier: only the tests behind //go:build functional in PKGS, -p GOTEST_P"
	@echo "make test-functional-container the functional tier of PKGS inside one container (tools/functionalrun; TESTING.md)"
	@echo "make test-full   go test -count=1 ./... (the whole tree)"
	@echo "make test-short  go test -short -count=1 -timeout SHORT_TIMEOUT PKGS"
	@echo "make test-slow   go test -count=1 -tags slow ./... (the nightly tier: the tests too slow for a commit)"
	@echo "make test-merge  go test -count=1 -timeout MERGE_TIMEOUT -run RUN PKGS"
	@echo "make test-race   go test -race ./... (the certification tier)"
	@echo "make test-e2e    go test -count=1 -run TestFriendSequence ./cmd/..."
	@echo "make test-prewarm-done run the exact #2498 S3 test manifest"
	@echo "make test-lisp   go run ./tools/ci lisp-test (nothing to test while the live tree has no Lisp system)"
	@echo "make compile-lisp nothing to compile while lisp/ holds no system; refuses if one appears"
	@echo "make check       build, lint, test, test-e2e and test-lisp (CI's gates; the stream lander's batch test)"
	@echo "make clean       remove ./bin and ./scratch"
	@echo "make map         regenerate AGENTS.md and per-directory maps"
	@echo "make new-rule    scaffold a class rule skeleton (ARGS=<name>)"
	@echo "make new-verb    scaffold a CLI verb skeleton (ARGS='<tool> <verb>')"

map:
	$(GO) run ./tools/agentsmap

# TLC runs on Linux benches with an explicit installed jar and owned output path.
TLC_JAR ?=
TLC_OUT ?=
TLC_GROUP ?=
TLC_BUDGET ?= 110
.PHONY: tlc tlc-full tlc-groups tlc-test
tlc:
	@test -n "$(TLC_JAR)" && test -n "$(TLC_OUT)" && test -n "$(TLC_GROUP)" || { echo 'make tlc: set TLC_JAR, TLC_OUT and TLC_GROUP' >&2; exit 2; }
	$(GO) run ./tools/tlacheck run --root . --jar "$(TLC_JAR)" --dir "$(TLC_OUT)" --group "$(TLC_GROUP)" --timeout "$(TLC_BUDGET)s"

tlc-full:
	@test "$(origin TLC_BUDGET)" != "file" || { echo 'make tlc-full: supply TLC_BUDGET explicitly' >&2; exit 2; }
	@test -n "$(TLC_JAR)" && test -n "$(TLC_OUT)" && test -n "$(TLC_GROUP)" || { echo 'make tlc-full: set TLC_JAR, TLC_OUT, TLC_GROUP and an explicit TLC_BUDGET' >&2; exit 2; }
	$(GO) run ./tools/tlacheck run --root . --jar "$(TLC_JAR)" --dir "$(TLC_OUT)" --group "$(TLC_GROUP)" --timeout "$(TLC_BUDGET)s" --manual

tlc-test:
	$(GO) test -count=1 ./internal/tlc ./internal/tablemodel ./tools/tlacheck

tlc-groups:
	@$(GO) run ./tools/tlacheck groups --root .


new-rule:
	$(GO) run ./tools/newrule $(ARGS)

new-verb:
	$(GO) run ./tools/newverb $(ARGS)


build:
	$(GO) build ./...

fmt:
	@$(GO) run ./tools/ci gofmt

vet:
	$(GO) vet $(PKGS)

# THE FUNCTIONAL TIER is the redis-backed tests. Every test that starts a
# redis-server is behind `//go:build functional`, so `vet` and `test` above do
# not compile it. vet-functional compiles those files on every change, so a PR
# that breaks one is red at once even though it does not run it;
# internal/ci's TestRedisBackedTestsCarryTheFunctionalTag keeps the tag on.
vet-functional:
	$(GO) vet -tags functional $(PKGS)

# THE SLOW TIER, the mirror of vet-functional: a test behind `//go:build
# slow` is compiled by no plain `go vet` and runs only in the nightly job, so a
# PR that breaks one stayed green until the morning. vet-slow compiles them on
# every change; internal/ci's TestEveryTestBuildTagIsVettedByCIVetSteps keeps
# the tag on.
vet-slow:
	$(GO) vet -tags slow $(PKGS)

# The shippedsmoke and novadisk tags are the two opt-in tags left, both compiled
# only by a scheduled job: shippedsmoke by certification.yml, novadisk (a darwin
# file, `//go:build darwin && novadisk`) by nightly-slow.yml on a mac. vet-shippedsmoke
# compiles the one package on every change; vet-novadisk is a GOOS=darwin cross-vet
# like vet-windows, because the file it guards is darwin-only and the lint job is
# linux.
vet-shippedsmoke:
	$(GO) vet -tags shippedsmoke ./internal/shippedsmoke

vet-novadisk:
	GOOS=darwin GOARCH=amd64 CGO_ENABLED=0 $(GO) vet -tags novadisk ./cmd/nova-sandbox

# THE VERB-LAW GUARD, its own target rather than folded into `vet` because
# `vet` is also the SHARDED per-package leg (ci.yml's test-packages job calls
# `make vet PKGS=<shard>` once per shard): vetlaw's checks are a whole-tree
# analysis over the verbs, and folding it into `vet` would rebuild and
# re-run it once per shard for no extra coverage. This runs once.
#
# ./cmd/..., not ./..., because tools/analyzers/fixtures deliberately trips
# all three checks on purpose (read directly by tools/analyzers' own tests)
# and is never meant to pass this gate.
vet-laws:
	@mkdir -p bin
	$(GO) build -o bin/vetlaw ./tools/analyzers/cmd/vetlaw
	$(GO) vet -vettool=$(CURDIR)/bin/vetlaw ./cmd/...

# THE ONE WINDOWS GUARD ON THE CL PATH is a cross-vet, not a Windows leg.
# `GOOS=windows go vet ./...` builds the Windows standard library
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

lint: fmt vet vet-functional vet-slow vet-laws

# preflight is the standard check for swarm cards and developers:
# gofmt + go vet + go test -count=1, run by tools/preflight. The tool is built into
# this checkout's bin/ and exec'd, so make's own signal and the tool's exit code are
# the ones the caller sees.
# No `preflight: PKGS ?= ...` line: under GNU make 3.81 (macOS /usr/bin/make) a
# target-specific `?=` on PKGS made `test: PKGS :=` beat the command line, so
# every macOS shard of dev push run 35999520176 ran the whole tree instead of its
# PKGS; with PKGS ?= ./... above, that line was a no-op everywhere else.
preflight:
	$(GO) build -o bin/preflight ./tools/preflight && exec ./bin/preflight $(if $(RUN),-run "$(RUN)",) $(PKGS)

# The fast tier. The package set is CL_PKGS, the living tree exactly as
# ci.yml's test-packages job selects it with --all; a caller that wants a shard
# passes PKGS explicitly.
#
# The run is teed through `-json` so cmd/nova-ci's slowtests verb reads the
# stream once and reports every package over the budget, and the same bytes
# stay on disk if a reader wants them. The pipeline is under `pipefail` and the
# go test status is carried out, so a red test still fails the target even
# though slowtests runs after it — a failing go test stops the verdict before
# slowtests, exactly as the workflow did when this lived inline in ci.yml.
#
# THE UNIT TIER'S BUDGETS: a package over 2 s or a top-level test over 1 s
# is a CI-SLOW line unless internal/ci/slow-tests_allowlist.txt names a higher
# budget for exactly that row, and every row there names the time it was
# measured at and where, `<seconds>s@run<id>` or `@<bench>` (internal/ci:
# TestSlowAllowlistRowsNameTheirMeasurement).
#
# A BUDGET VERDICT IS THE SAME ON ANY MACHINE.
# What is ENFORCED on every leg is static: no unit test waits on
# the wall clock (internal/ci: TestNoUnitTestWaitsOnTheWallClock), and a test
# skipped with the SLEEPS marker that internal/ci/sleeps-skips_allowlist.txt does
# not name is a CI-SLEEPS line (slowtests exits 1) and fails the target here,
# whatever SLOWTESTS_ENFORCE says.
# The wall times are MEASUREMENTS: slowtests prints every CI-SLOW line and a
# CI-LOAD line (the host's load, never read by the verdict) and exits 0 on
# them, unless SLOWTESTS_ENFORCE=1 passes --enforce, which one caller does: the
# nightly whole-tree run on the space legs (ci.yml, the test step's schedule
# branch). SLOWTESTS_FLAGS is the whole slowtests invocation, so the push run
# over the whole tree passes the old 60 s package budget instead (ci.yml).
SLOWTESTS_FLAGS ?= --package-budget 2 --test-budget 1 --allowlist internal/ci/slow-tests_allowlist.txt --sleeps internal/ci/sleeps-skips_allowlist.txt
SLOWTESTS_ENFORCE ?= 0
#
# GOTEST_P IS THE LEG'S CORES: `go test -p` (packages at once) and `-parallel`
# (tests at once in a package) are both held to it, and ci.yml caps the leg's
# GOMAXPROCS at the same two, so a leg never takes more than two cores however
# many runners share the box (internal/ci: TestUnitLegTakesAtMostTwoCores).
GOTEST_P ?= 2
#
# GOTEST_TIMEOUT is `go test -timeout` for this target; the default is Go's own
# 10m. The workflow sets it per leg to fit the leg's job cap.
GOTEST_TIMEOUT ?= 110s
# GOTEST_COUNT_FLAG is go test's -count=1 by hand (every test runs, nothing
# is taken from the cache), EMPTY in CI (.github/workflows/ci.yml passes
# GOTEST_COUNT_FLAG=) so Go's test cache serves a package whose inputs did not
# change: a landing runs the suite three times (pull request, merge group,
# push to dev) on trees that differ by nothing, and -count=1 made every run
# recompile and re-execute every shard (the coordinator's machine at 100% CPU
# on its own PR). A cached pass is a real earlier pass on identical
# inputs; Go keys the cache on the package, its files, the env it reads and
# the files it opens.
GOTEST_COUNT_FLAG ?= -count=1
# GOTEST_LDFLAGS: empty by hand; CI passes -ldflags=-w so test binaries link
# without DWARF: on darwin every test binary otherwise runs dsymutil (seen at
# 51% CPU on the coordinator's machine) and a debug-info-free binary
# is smaller for the malware scan that follows every fresh executable.
GOTEST_LDFLAGS ?=
# GOTEST_TAGS: empty in every CI leg of the unit tier (ci.yml never sets it;
# the functional tier is its own job and `make test-functional`). `nova-ci
# local --functional` sets it to build and run the redis-backed tests behind
# `//go:build functional` beside the unit tests, on a developer's machine.
GOTEST_TAGS ?=
test: PKGS = $(CL_PKGS)
test:
	@bash -o pipefail -c 'GOFLAGS=-json $(GO) test $(GOTEST_COUNT_FLAG) $(PKGS) -p $(GOTEST_P) -parallel $(GOTEST_P) -tags=$(GOTEST_TAGS) $(GOTEST_LDFLAGS) -timeout $(GOTEST_TIMEOUT) | tee "$${RUNNER_TEMP:-$${TMPDIR:-/tmp}}/test.json"; status=$${PIPESTATUS[0]}; $(GO) run ./cmd/nova-ci slowtests $(SLOWTESTS_FLAGS) $(if $(filter 1,$(SLOWTESTS_ENFORCE)),--enforce,) < "$${RUNNER_TEMP:-$${TMPDIR:-/tmp}}/test.json" || { [ "$$status" -ne 0 ] || status=2; }; exit $$status'

# This target is the functional tier. A functional test is one in a _test.go
# built only under `//go:build functional`: it starts a real redis-server, a
# real binary, a real process. ci.yml's `functional` job runs this target on
# merge_group, schedule and workflow_dispatch only, never on a pull request.
# `nova-ci functional` picks, among PKGS, the packages holding such files and a
# -run pattern naming exactly their tests, so the unit tests of those packages
# are not run a second time; PKGS with no functional file run nothing,
# and say so on one `CI FUNCTIONAL OK packages=0 reason=<why>` line. The
# selection and the `go test` that follows it are `tools/ci functional-run`.
FUNCTIONAL_TIMEOUT ?= 100s
test-functional: PKGS = $(CL_PKGS)
test-functional:
	@$(GO) run ./tools/ci functional-run --go $(GO) --p $(GOTEST_P) --timeout $(FUNCTIONAL_TIMEOUT) $(PKGS)

# THE FUNCTIONAL TIER IN A CONTAINER. test-functional-container runs the target
# above inside one container per run (tools/functionalrun, TESTING.md): the
# image built from FUNCTIONAL_CONTEXT or reused, the tree mounted read-only,
# tmpfs scratch, this user's own Go cache volumes, no network, and
# FUNCTIONAL_DEADLINE enforced from outside the container by the runtime. The
# container is removed whatever happens, and every container of an earlier run
# past its deadline is reaped first. The tool is built into this checkout's bin/
# and exec'd, never `go run`, so a signal to make reaches the tool itself and
# the tool's exit code is make's to report (make reports any failure as 2).
FUNCTIONAL_DEADLINE ?= 10m
FUNCTIONAL_CONTEXT ?= infra/functional-image
# FUNCTIONAL_FLAGS: more flags for the tool, such as --fresh-gocache.
FUNCTIONAL_FLAGS ?=
test-functional-container: PKGS = $(CL_PKGS)
test-functional-container:
	$(GO) build -o bin/functionalrun ./tools/functionalrun && exec ./bin/functionalrun run --deadline $(FUNCTIONAL_DEADLINE) --context $(FUNCTIONAL_CONTEXT) $(FUNCTIONAL_FLAGS) $(PKGS)

test-full:
	$(GO) test -count=1 $(if $(RUN),-run "$(RUN)",) $(PKGS)

# SHORT_TIMEOUT is test-short's `go test -timeout`, per test binary. The hosted
# legs spend 55-75 s on setup before their test step (run 36367639661), and the
# slowest hosted package runs 24 s (cmd/nova-review on macos-latest), so 50 s
# fires before the two-minute job cap kills the leg without a stack.
SHORT_TIMEOUT ?= 50s
test-short:
	$(GO) test -short -count=1 -timeout $(SHORT_TIMEOUT) $(PKGS)

# The nightly tier (go-test-slow): every test behind `//go:build slow`, with the
# whole tree around it. A test lands there when the per-commit run cannot pay it
# -- over five seconds on the Linux bench, or a deadline proved by waiting it out
# (internal/ci/slowwaits_class_test.go) -- and .github/workflows/nightly-slow.yml
# runs the same command every night. The per-commit legs, go-test-cmd and
# go-test-internal, do not build these files.
test-slow:
	$(GO) test -count=1 -tags slow ./...

# `test-pr` lived here, the sharded hosted PR leg's entry: it had exactly
# one caller, and the caller is
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

test-prewarm-done:
	$(GO) test -count=1 ./tools/testmanifest
	$(GO) run ./tools/testmanifest --go "$(GO)" --package ./internal/swarm -- TestASDFMappingReusesCompiledOutputAcrossFreshJobClone TestPrewarmFailedRerunInvalidatesPriorReceipt TestPrewarmGitChildrenDropSecrets

# The Lisp tier. The old nova-work's Lisp kernel, the one Lisp system, lives in
# the repository nova-work-old, for reference only, so lisp/ holds no system:
# test-lisp runs CI's verb (tools/ci lisp-test), which prints "nothing to test"
# and exits 0, and compile-lisp (the swarm prewarm's lisp phase) prints
# "nothing to compile".
# verify-roadmap and measure-roadmap ran cmd/nova-work's verification verb and went with it.
test-lisp:
	$(GO) run ./tools/ci lisp-test

compile-lisp:
	@if [ -e lisp ]; then echo "compile-lisp: lisp/ exists and no compile step names it; write one" >&2; exit 1; fi
	@echo "compile-lisp: nothing to compile: lisp/ holds no system (the old nova-work kernel lives in the nova-work-old repository)"

# What CI runs on a pull request: the self-hosted lint job, the sharded test
# job, the friend sequences and the lisp tier (test-lisp; nothing to test while
# nova-work is parked).
# The stream lander's batch test lands a whole stream, so it runs the
# functional tier too. Over the whole tree the times are printed
# against the old 60 s package budget and a SLEEPS skip off the ledger is red;
# the target variables ride into `test` as its prerequisite.
check: SLOWTESTS_FLAGS := --budget 60 --sleeps internal/ci/sleeps-skips_allowlist.txt
check: build lint test test-functional test-e2e test-lisp

# An explicit list, never a computed path: clean removes the two directories a
# local build and a worker's notes land in, and nothing else.
clean:
	rm -rf ./bin ./scratch

-include make/*.mk
