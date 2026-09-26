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
DARWIN_TIMEOUT ?= 110s

# MERGE_TIMEOUT is the per-package ceiling on the merge group's legs. 100 s is
# the linux number and is what that leg has always used; the darwin leg exports
# MERGE_TIMEOUT=$(make -s darwin-timeout), so that platform's ceiling lives in ONE
# place — DARWIN_TIMEOUT above — instead of being written again in the workflow.
# `?=` is what makes that environment value win.
MERGE_TIMEOUT ?= 100s

.PHONY: help build fmt vet vet-functional vet-laws vet-windows lint preflight test test-full test-short test-slow test-functional test-merge test-race test-e2e test-prewarm-done compile-lisp test-lisp verify-roadmap measure-roadmap check clean darwin-timeout map new-rule new-verb

help:
	@echo "make help        this list"
	@echo "make build       go build ./..."
	@echo "make fmt         report files that are not gofmt-clean"
	@echo "make vet         go vet PKGS (default ./...)"
	@echo "make vet-functional go vet -tags functional PKGS (the redis-backed test files compiled too)"
	@echo "make vet-laws    build tools/analyzers/cmd/vetlaw and vet ./cmd/... with it"
	@echo "make vet-windows GOOS=windows go vet ./... (the one Windows guard on the CL path)"
	@echo "make lint        fmt and vet"
	@echo "make preflight   gofmt, go vet, and go test -count=1 (PKGS)"
	@echo "make test        the unit tier: go test -p GOTEST_P PKGS plus the 2 s package / 1 s test slowtests budgets"
	@echo "make test-functional the functional tier: only the tests behind //go:build functional in PKGS, -p GOTEST_P"
	@echo "make test-full   go test -count=1 ./... (the whole tree)"
	@echo "make test-short  go test -short -count=1 -timeout 12m PKGS"
	@echo "make test-slow   go test -count=1 -tags slow ./... (the nightly tier: the tests too slow for a commit)"
	@echo "make test-merge  go test -count=1 -timeout MERGE_TIMEOUT -run RUN PKGS"
	@echo "make test-race   go test -race ./... (the certification tier)"
	@echo "make test-e2e    go test -count=1 -run TestFriendSequence ./cmd/..."
	@echo "make test-prewarm-done run the exact #2498 S3 test manifest"
	@echo "make test-lisp   ./lisp/nova-work/run-tests.sh"
	@echo "make compile-lisp compile nova-work and its tests without running them"
	@echo "make verify-roadmap test-lisp's suite, then the roadmap sexp's criteria judged against it (writes nothing)"
	@echo "make measure-roadmap the same, then :verification rewritten to what was measured at HEAD"
	@echo "make check       build, lint, test, test-e2e and verify-roadmap (CI's gates; the stream lander's batch test)"
	@echo "make clean       remove ./bin and ./scratch"
	@echo "make map         regenerate AGENTS.md and per-directory maps"
	@echo "make new-rule    scaffold a class rule skeleton (ARGS=<name>)"
	@echo "make new-verb    scaffold a CLI verb skeleton (ARGS='<tool> <verb>')"

map:
	$(GO) run ./tools/agentsmap

new-rule:
	$(GO) run ./tools/newrule $(ARGS)

new-verb:
	$(GO) run ./tools/newverb $(ARGS)


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

# THE FUNCTIONAL TIER (nova-tools #4328, Glenn 2026-09-26 11:20 AM ET: "we
# should run functional tests, not on every small PR being merged or worked on,
# but only as we merge whole work streams"). Every test that starts a
# redis-server is behind `//go:build functional`, so `vet` and `test` above do
# not compile it. vet-functional compiles those files on every change, so a PR
# that breaks one is red at once even though it does not run it;
# internal/ci's TestRedisBackedTestsCarryTheFunctionalTag keeps the tag on.
vet-functional:
	$(GO) vet -tags functional $(PKGS)

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

lint: fmt vet vet-functional vet-laws

# preflight is the standard check for swarm cards and developers (#2498 S4):
# gofmt + go vet + go test -count=1
# No `preflight: PKGS ?= ...` line: under GNU make 3.81 (macOS /usr/bin/make) a
# target-specific `?=` on PKGS made `test: PKGS :=` beat the command line, so
# every studio shard of dev push run 35999520176 ran the whole tree instead of
# its PKGS; with PKGS ?= ./... above, that line was a no-op everywhere else.
preflight:
	./tools/preflight.sh $(if $(RUN),-run "$(RUN)",) $(PKGS)

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
# THE UNIT TIER'S BUDGETS (Glenn 2026-09-26 11:20 AM ET, nova-tools#4328: "unit
# tests be < 2s (ideally <1) but also they must not be so aggressive that they
# fill a whole machine cores"): a package over 2 s or a top-level test over 1 s
# is a CI-SLOW line and a red leg, unless internal/ci/slow-tests_allowlist.txt
# names a higher budget for exactly that row, and every row there names the
# time it was measured at and where (internal/ci:
# TestSlowAllowlistRowsNameTheirMeasurement).
#
# A BUDGET FAILS A TEST FOR WHAT IT DOES, NEVER FOR A BUSY RUNNER: run
# 36261817989 (PR #4409, 2026-09-26 14:15 ET) was red on all eight self-hosted
# shards with no test failing, twelve cmd/nova-review and cmd/nova-bus tests at
# 1.0-1.2 s that run near 0.9 s idle, on a Studio at load 16-18 of its 32 CPUs
# (0.50-0.56 a CPU; four darwin runners and the children building beside
# them). So slowtests reads the host's load average (the larger of its 1- and
# 5-minute figures) over its CPUs, prints it as a CI-LOAD line, and above
# --max-load-per-cpu 0.25 (half the 0.50 that already moved 0.9 s to 1.2 s)
# prints every CI-SLOW line as measured and does not fail the leg on them.
# What a test does stays red at any load: a failing test (the unit tier's
# redis-server shim fails an unmocked redis), and a test skipped with the
# SLEEPS marker that internal/ci/sleeps-skips_allowlist.txt does not name
# (internal/ci: TestSleepsLedgerIsTheTreesSleepsSkips).
#
# SLOWTESTS_FLAGS is the whole slowtests invocation, so the push run over the
# whole tree, which is not cut to these budgets yet, passes the old package
# budget instead (ci.yml), with SLOWTESTS_ENFORCE=0: its CI-SLOW lines print
# and do not fail the leg, as before. Everywhere else a CI-SLOW line fails the
# target unless the CI-LOAD line says the box was over the gate.
SLOWTESTS_FLAGS ?= --package-budget 2 --test-budget 1 --allowlist internal/ci/slow-tests_allowlist.txt --sleeps internal/ci/sleeps-skips_allowlist.txt --max-load-per-cpu 0.25
SLOWTESTS_ENFORCE ?= 1
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
# recompile and re-execute every shard (Glenn 2026-09-26 9:42 AM ET, the
# Studio at 100% on its own PR: "We aren't doing anything that should be this
# heavy in CPU use"). A cached pass is a real earlier pass on identical
# inputs; Go keys the cache on the package, its files, the env it reads and
# the files it opens.
GOTEST_COUNT_FLAG ?= -count=1
# GOTEST_LDFLAGS: empty by hand; CI passes -ldflags=-w so test binaries link
# without DWARF: on darwin every test binary otherwise runs dsymutil (seen at
# 51% CPU on the Studio, 2026-09-26 9:47 AM ET) and a debug-info-free binary
# is smaller for the malware scan that follows every fresh executable.
GOTEST_LDFLAGS ?=
# GOTEST_TAGS: empty in every CI leg of the unit tier (ci.yml never sets it;
# the functional tier is its own job and `make test-functional`). `nova-ci
# local --functional` sets it to build and run the redis-backed tests behind
# `//go:build functional` beside the unit tests, on a developer's machine.
GOTEST_TAGS ?=
test: PKGS := $(CL_PKGS)
test:
	@bash -o pipefail -c 'GOFLAGS=-json $(GO) test $(GOTEST_COUNT_FLAG) $(PKGS) -p $(GOTEST_P) -parallel $(GOTEST_P) -tags=$(GOTEST_TAGS) $(GOTEST_LDFLAGS) -timeout $(GOTEST_TIMEOUT) | tee "$${RUNNER_TEMP:-$${TMPDIR:-/tmp}}/test.json"; status=$${PIPESTATUS[0]}; $(GO) run ./cmd/nova-ci slowtests $(SLOWTESTS_FLAGS) < "$${RUNNER_TEMP:-$${TMPDIR:-/tmp}}/test.json" || { [ "$(SLOWTESTS_ENFORCE)" != 1 ] || [ "$$status" -ne 0 ] || status=2; }; exit $$status'

# THE FUNCTIONAL TIER (nova-tools#4328; Glenn 2026-09-26 11:20 AM ET: "we should
# run functional tests, not on every small PR being merged or worked on, but
# only as we merge whole work streams"). A functional test is one in a _test.go
# built only under `//go:build functional`: it starts a real redis-server, a
# real binary, a real process. ci.yml's `functional` job runs this target on
# merge_group, schedule and workflow_dispatch only, never on a pull request.
# `nova-ci functional` picks, among PKGS, the packages holding such files and a
# -run pattern naming exactly their tests, so the unit tests of those packages
# are not run a second time; PKGS with no functional file run nothing.
FUNCTIONAL_TIMEOUT ?= 100s
test-functional: PKGS := $(CL_PKGS)
test-functional:
	@bash -o pipefail -c 'sel=$$($(GO) run ./cmd/nova-ci functional $(PKGS)) || exit 2; if [ -z "$$sel" ]; then echo "functional: no test in these packages carries the functional build tag; nothing to run"; exit 0; fi; pkgs=$$(printf "%s\n" "$$sel" | sed -n 1p); run=$$(printf "%s\n" "$$sel" | sed -n 2p); echo "functional: $$pkgs"; $(GO) test -tags functional -p $(GOTEST_P) -count=1 -timeout $(FUNCTIONAL_TIMEOUT) -run "$$run" $$pkgs'

test-full:
	$(GO) test -count=1 $(if $(RUN),-run "$(RUN)",) $(PKGS)

test-short:
	$(GO) test -short -count=1 -timeout 12m $(PKGS)

# The nightly tier (go-test-slow): every test behind `//go:build slow`, with the
# whole tree around it. A test lands there when the per-commit run cannot pay it
# -- over five seconds on the Linux bench, or a deadline proved by waiting it out
# (internal/ci/slowwaits_class_test.go) -- and .github/workflows/nightly-slow.yml
# runs the same command every night. The per-commit legs, go-test-cmd and
# go-test-internal, do not build these files.
test-slow:
	$(GO) test -count=1 -tags slow ./...

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

test-prewarm-done:
	$(GO) test -count=1 ./tools/testmanifest
	$(GO) run ./tools/testmanifest --go "$(GO)" --package ./internal/swarm -- TestASDFMappingReusesCompiledOutputAcrossFreshJobClone TestPrewarmFailedRerunInvalidatesPriorReceipt TestPrewarmGitChildrenDropSecrets

test-lisp:
	./lisp/nova-work/run-tests.sh

compile-lisp:
	./lisp/nova-work/compile.sh

# The roadmap sexp's :verification block against the suite at HEAD (#3459):
# verification runs ./lisp/nova-work/run-tests.sh itself, so verify-roadmap is
# test-lisp plus the judgement -- every verified criterion's named test is still
# green -- and check runs it in test-lisp's place rather than the suite twice.
# CI's lisp job keeps make test-lisp: its runners carry sbcl, not Go.
verify-roadmap:
	$(GO) run ./cmd/nova-work verification --sexp docs/roadmaps/nova-work.sexp --repo . --check

measure-roadmap:
	$(GO) run ./cmd/nova-work verification --sexp docs/roadmaps/nova-work.sexp --repo . --write

# What CI runs on a pull request: the self-hosted lint job, the sharded test
# job, the friend sequences and the nova-work acceptance suite (run once, by
# verify-roadmap, with the roadmap's verified criteria judged against it).
# The stream lander's batch test lands a whole stream, so it runs the
# functional tier too (#4328). Over the whole tree the unit budgets are
# printed, not enforced (the tree is not cut to 2 s / 1 s yet); the target
# variables ride into `test` as its prerequisite.
check: SLOWTESTS_FLAGS := --budget 60
check: SLOWTESTS_ENFORCE := 0
check: build lint test test-functional test-e2e verify-roadmap

# An explicit list, never a computed path: clean removes the two directories a
# local build and a worker's notes land in, and nothing else.
clean:
	rm -rf ./bin ./scratch

-include make/*.mk
