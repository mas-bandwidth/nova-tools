RESULT tools22-pre-1478-r1 sha=5298f6be12ea — a pre-read of mas-bandwidth/nova-tools#1478 at head 3c2bb6d8bb64: swarm: a gate that did not execute its command cannot return OK, and read_roots reaches both fe
PREREAD 1478 claims=8 proven=8 unproven=0 defects=4 high=0

PR 1478
HEAD 3c2bb6d8bb64075fe81fe75fe87ca5057f5d65d9
BASE dev
MERGE-BASE 6ae36df260c107b501549fb03eeaf98f33123d9a
BEHIND 33
FILES 5 production, 4 test
LINES +918 -2

NOTE ON THE BASE: the PR merged origin/dev at 6ae36df2 into its branch (737e09b0), and the
merge-base of origin/dev and this head is that same 6ae36df2 — OLDER than the card's stated
base dev@5298f6be12ea, which is not an ancestor of the head. The net diff 6ae36df2..head is
the true PR work (9 files); the 408-file diff against 5298f6be12ea is other people's landed
work and is not this PR. The head is 33 commits behind dev. Notably, the read_roots fence
(issue #1463) that commit c5f67631 claims to "fix" is already implemented and tested on dev
at the merge-base (integration-16c #1719: internal/swarm/sandbox.go, nativeReadRoots in
cmd/nova-swarm/native.go, cmd/nova-swarm/fence_readroots_test.go); the net diff changes no
production code for read_roots, only adds a test and docs.

CLAIMS

1. A `native` run whose own capture holds a shell's `Permission denied` on an absolute path
   is refused — `NATIVE REFUSED`, exit 2, and there is no `NATIVE OK` line at all.
   PROVEN-BY cmd/nova-swarm/gate_test.go:37 TestNativeGateThatCouldNotRunIsNeverOK — a
   walled run with the fixture denial must print no "NATIVE OK", exit non-zero, and carry one
   "NATIVE REFUSED" line naming the class, the step, the path and the job.

2. The refused path is taken WHOLE between the shell's own `: ` delimiters, spaces and
   parentheses included, and is named whole in the refusal.
   PROVEN-BY cmd/nova-swarm/gate_hold_test.go:25 TestDeniedPathWithASpaceStillRefuses —
   "/opt/sdk tool/bin/go" must refuse and the refusal must contain the complete path; also
   internal/swarm/shelldenial_test.go:22, which reads the space, parenthesis and
   multi-space spellings whole.

3. The refusal asserts no cause it cannot prove: the operation is labelled
   `operation=unverified`, the shell's own line is quoted verbatim, and the words "never
   executed", "nothing compiled", "the program" and "the gate never" never appear.
   PROVEN-BY internal/swarm/shelldenial_test.go:143 TestShellDenialReasonAssertsNoCauseItCannotProve
   — forbids those phrases on both walled and unwalled spellings and requires
   operation=unverified, "Permission denied" and step=3; also gate_hold_test.go:62.

4. A `--no-wall` run's refusal blames no wall and offers no read set, saying in so many words
   that the run had no sandbox.
   PROVEN-BY internal/swarm/shelldenial_test.go:175 TestShellDenialReasonAttributesNothingToAWallThatWasNotThere
   — the unwalled reason must not contain read_roots or "the wall refused" and must contain
   "no sandbox"; also gate_hold_test.go:62.

5. A walled run's refusal offers the read set as ONE candidate worked out of the denied path
   — two roots for a path reached through a symlink — and says it is a candidate, not the
   diagnosis.
   PROVEN-BY internal/swarm/shelldenial_test.go:201 TestDeniedPathRootsNameBothEntries — for a
   symlinked launcher the candidate set names both the launcher directory and the resolved
   tree; and gate_hold_test.go:109, which requires the walled refusal to offer the complete
   candidate root while still calling the operation unverified.

6. A line that holds the words without a SHELL saying them — the harness's own refusal prose,
   another program's complaint, a signal, a relative program, prose — is not this class, and
   such a run still returns OK.
   PROVEN-BY internal/swarm/shelldenial_test.go:22 TestShellDeniedReadsTheShellsOwnWords (the
   six negative spellings) and cmd/nova-swarm/gate_test.go:91 TestNativeOrdinaryRunIsStillOK —
   a run whose capture holds the fake's refusal prose still prints "NATIVE OK " and exits 0.

7. A run refused for an unread denial names its job directory and leaves the card's own
   published result in place, so the spend stays harvestable.
   PROVEN-BY cmd/nova-swarm/gate_test.go:37 — the refusal must name
   <slot>/jobs/<label> and the card's RESULT.md must still exist under it after the refusal.

8. The worker description's read_roots reach both fences — the OS wall's `--read` list and
   the harness's own permission block — on a walled run as much as an unwalled one.
   PROVEN-BY-EXISTING cmd/nova-swarm/fence_readroots_test.go:39 TestNativeConfigNamesTheWorkerReadRootsOnAWalledRun
   (and :73 for the wall argv), both already in the tree at the merge-base. The PR's own
   cmd/nova-swarm/gate_test.go:123 TestNativeWalledRunOpensTheWorkersReadRoots asserts the
   same, but the production code for this is unchanged by this diff — see the NOTE above.

DEFECT low docs/CLI.md:2467 — the example renders `denied_path=/opt/sdk tool/bin/go` with a
literal space, but ShellDenialReason passes the path through oneline.Field, which escapes
every space to `\x20`, so the field the tool actually prints is
`denied_path=/opt/sdk\x20tool/bin/go` — a reader of the docs would expect a field the tool
never prints, and any future parser built from the doc would miss the token — show the escaped
spelling in the example, or render the denied_path token unescaped in the code.

DEFECT low internal/swarm/wall.go:423 — the `fork/exec ` branch scans every `: `-delimited
part of the line and returns a match without checking the first field is a shell or Go's own
os/exec, contradicting the comment's own invariant ("the first field must be a SHELL ... or
the words must be Go's fork/exec") — a non-shell program's line such as
`python: fork/exec /opt/x: permission denied` would turn a card that routed around it into a
refusal, and no negative test covers the shape — gate the fork/exec branch on parts[0] being
a shell word, or require the whole line to begin with `fork/exec `.

DEFECT low cmd/nova-swarm/native.go:589 — the shell-denial check reads the WHOLE
`<job>/harness-output.log`, a file opened O_APPEND and never truncated (native.go:334), so a
denial printed by an earlier launch in the same run, or an earlier dispatch that wrote into
the same job directory, would refuse a later run whose own capture is clean — it is the one
denial check that fires WITH a published result and has no "result first" guard the way
wallRefused does — scan only the bytes this run's launches appended (the retry loop already
records `before := fileSize(outLog)`), or clear the capture between dispatches.

DEFECT low docs/SPEC-SWARM.md:1219 — the added bullet re-states the read_roots fence rule
that already stands 13 lines below it (SPEC-SWARM.md:1232) and cites #1463 as if this PR
shipped it, when the production fix and its red test are already on dev at the merge-base —
a doc redundancy that implies a change the diff does not make — drop the added bullet and
keep the existing one, which already names the red test.

QUESTIONS

1. Issue #1463's read_roots fence is already on dev at the merge-base (integration-16c,
   #1719): the PR's commit message and SPEC-SWARM doc present it as new work, while the net
   diff adds only a test and a duplicated doc bullet. Was this PR cut before #1719 landed and
   simply merged over it, and is the duplicated test+doc deliberate?
2. The shell-denial check reads the whole O_APPEND capture. On the benches, do re-dispatched
   runs of the same label actually reuse <slot>/jobs/<label>/harness-output.log, which would
   let a stale denial from an earlier dispatch refuse a later clean run?
3. `denied_path=` is rendered through oneline.Field (spaces escaped to \x20) while
   `line=%q` keeps literal spaces and the CLI.md example shows a raw space: which spelling is
   the intended machine field, and does anything downstream parse `denied_path=`?
4. For a WRITE denial the card recovered from (Stella's P2 witness), the walled refusal still
   offers read_roots candidates for the denied path; is offering a read set for what may be a
   failed write the wording you want, or should the candidate be offered only when the wall
   could have caused the denial?

Left owed: I read the full new internal/swarm/wall.go additions (lines 317-549), the three
new test files in full, the fakeharness and fakesandbox in full, and the relevant regions of
cmd/nova-swarm/native.go (128-392, 545-989, 1101-1170) and main.go (1740-1845). I did not
read the whole 1322-line native.go, nor internal/swarm/batch.go, run.go or supervise.go
beyond greps for the refusal/requeue/usage paths, nor fence.go's every caller. I did not run
the new tests (none required and none expected); `go build` and `go vet` of internal/swarm
and cmd/nova-swarm both pass at the head.

git status --short:
git rev-parse HEAD: 3c2bb6d8bb64075fe81fe75fe87ca5057f5d65d9===FILE=== card-tools22-pre-1478-r1/usage.tsv
job	attempt	started	ended	rc	provider	model	tokens_in	tokens_out	cache_write	cache_read	reasoning	usd
card-tools22-pre-1478-r1	1	2026-09-20T19:19:44Z	2026-09-20T19:30:18Z	0	opencode	deepseek-v4-flash	113284	52886	0	5629440	0	0.1883
