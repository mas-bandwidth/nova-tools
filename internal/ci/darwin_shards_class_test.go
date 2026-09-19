package ci

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// darwin_shards_class_test.go is the Windows lesson, one platform later.
//
// On 2026-09-18 at 16:41Z the merge group of batch 7 (PR #1360, run
// 35369433950) had `test-hosted-merge (darwin, 0)` and `(darwin, 1)` CANCELLED
// at the five-minute per-leg cap on superman. A cancelled shard drops the whole
// group and restarts every PR behind it, so this was not one red leg; it was the
// queue.
//
// THE LOG DOES NOT SAY WHAT THE HEADLINE SAYS, and that is worth writing down.
// No package came near the 100 s per-package ceiling. Shard 0 finished 27
// packages summing 211.8 s of `go test` between 16:36:43 and 16:41:24 and was
// killed partway through the rest, its largest single invocation cmd/nova-wake
// at 50.7 s. What ran out was the SHARD'S SUM, and the sum is decided by how many
// ways each package is dealt — and dealing came off the LINUX table. Four of the
// five largest darwin packages sit UNDER the 40 s budget there, so each was dealt
// three ways instead of six: cmd/nova-wake 24.6 s on Linux against 120.3 s
// measured here, cmd/nova-merge 7.7 against 68.8, internal/swarm 17.4 against
// 64.4 and cmd/nova-bus 10.0 against 54.2. Only cmd/nova-swarm (51.0 against
// 82.7) was already called large by the table that leg was reading.
//
// That is integration-4's mistake wearing a different OS, and integration-4
// already produced the fix: measure the platform, keep the measurements in a
// table beside the tree, deal the shard plan from the table, and take the
// per-package ceiling from one place. These two tests are the Windows pair
// (`windows-sizes`, `windows-table`) mirrored onto darwin; those two are PARKED
// as of 2026-09-18 — the native windows CI runners were dropped, Glenn: "WSL
// only from now on" — so this file is now where that lesson lives and runs, and
// the parser it used to share with them stays in sizetable_test.go for the next
// platform. See docs/SPEC-CI.md, "Parked class tests".
//
// THE CAP IS MEASURED ON A QUIET HOST AND CARRIES A STATED MARGIN. The cancel
// happened while superman was in its post-power-on state — Spotlight still
// settling, XprotectService scanning fresh binaries, sixteen runners busy — and a
// number read off a machine in that state is not the machine's size, it is the
// state's. So the table is measured on an idle host with no runner busy and no
// merge group in flight, and the ceiling is twice that measurement. Two numbers,
// both written down: what the host did when nothing else ran, and the factor
// that covers everything else.
//
// AND THE FACTOR IS MEASURED TOO, on the same host both times. cmd/nova-merge is
// 68.8 s whole on a quiet superman, while that package's three shares in the
// cancelled run sum to about 147 s on the loaded superman: 2.1x. Two is that,
// stated, rather than a round number chosen because it felt safe.

// darwinSizesPath is the measured darwin size of each package, the table the
// merge gate's darwin leg deals its shards from. Its sibling
// testdata/ci/package-sizes.tsv (idle Linux) is a different measurement on a
// different machine and neither predicts the other — which is the whole reason
// there is more than one. There were three: the windows-latest table went with
// the native windows legs on 2026-09-18.
const darwinSizesPath = "testdata/ci/package-sizes-darwin.tsv"

// darwinShardBudget is the seconds of darwin work above which a package's tests
// are dealt across slots rather than run whole in one. It is the same 40 s
// threshold the workflow compares every table against, named here so the test
// and the shell agree on one number.
const darwinShardBudget = 40.0

// darwinMeasurementRun is the hurt this table answers: the merge-group run whose
// darwin shards were cancelled at the five-minute cap.
const darwinMeasurementRun = "35369433950"

// darwinQuietHostMargin is the factor between the measurement and the ceiling,
// and it is the second half of the rule. A measurement taken on a quiet host is
// the floor of what the leg will meet, never the worst case: the same host under
// sixteen busy runners, a Spotlight pass and XprotectService scanning every fresh
// test binary is the machine the merge group actually lands on. Two is the stated
// margin, measured on superman both ways (cmd/nova-merge 68.8 s quiet against
// about 147 s loaded in the cancelled run, 2.1x), and it is written here rather
// than folded into the table so that re-measuring does not silently re-decide how
// much room the number has.
const darwinQuietHostMargin = 2.0

// TestDarwinMergeShardPlanIsDerivedFromMeasurements keeps the numbers that decide
// this leg's cost — the measured sizes, the shard budget, and DARWIN_TIMEOUT in
// the Makefile — in one place and in step, and holds that the ceiling can never
// drop back below a size actually observed.
//
// This is `windows-sizes` for darwin, and the hurt is the same shape: a plan that
// dealt by count off another platform's table, and a cap that was a convention
// rather than a measurement.
func TestDarwinMergeShardPlanIsDerivedFromMeasurements(t *testing.T) {
	root := repoRoot(t)
	_, full := readSizeTable(t, root, darwinSizesPath)
	if len(full) == 0 {
		t.Fatalf("%s carries no full measurements; the darwin shard plan would guess at every package, which is what run %s cost", darwinSizesPath, darwinMeasurementRun)
	}

	// The packages whose measured darwin size is over the shard budget are the
	// reason this leg is dealt at all. Naming them means a table that quietly
	// loses one is a red run rather than a cancelled group.
	for _, pkg := range darwinForcingPackages {
		v, ok := full[modulePath+pkg]
		if !ok || !v.measured {
			t.Errorf("%s has no full row for %s, one of the packages whose darwin size is the reason this leg is dealt from a table at all (run %s)", darwinSizesPath, pkg, darwinMeasurementRun)
			continue
		}
		if !v.censored && v.secs < darwinShardBudget {
			t.Errorf("%s says %s is %.1fs, under the %.0fs shard budget, so the plan would run it whole in one slot; it was measured over the budget on an x64 Mac. If a real re-measurement put it under, take it off darwinForcingPackages in the same edit", darwinSizesPath, pkg, v.secs, darwinShardBudget)
		}
	}

	// The per-package ceiling lives in the Makefile, where the darwin merge leg
	// reads it through `make -s darwin-timeout`. It bounds ONE `go test`
	// invocation, and the largest invocation this plan can produce is THE WHOLE OF
	// THE LARGEST PACKAGE — not its share.
	//
	// That is worth spelling out, because the share is the tempting number and it
	// is the wrong one. plan-merge decides how many slots a group opens from the
	// LINUX table, and it opens ONE when the changed packages sum under 20 s
	// there. cmd/nova-merge is 7.7 s on Linux and 68.8 s here, so a group that
	// changes only that package opens one slot and runs the package WHOLE in a
	// single `go test`. Dealing cannot help a package the plan never dealt.
	//
	// And the margin on top is not decoration either. Tests are dealt by NAME
	// INDEX and not by time, so shares come out uneven; and these sizes were read
	// on a QUIET host while the leg runs on a loaded one.
	// darwinQuietHostMargin is those allowances in one stated factor.
	largest := 0.0
	largestName := ""
	for pkg, v := range full {
		if v.measured && v.secs > largest {
			largest, largestName = v.secs, pkg
		}
	}
	mk := parseMakefile(t, filepath.Join(root, "Makefile"))
	raw, ok := mk.vars["DARWIN_TIMEOUT"]
	if !ok {
		t.Fatal("the Makefile declares no DARWIN_TIMEOUT; the darwin per-package ceiling has nowhere to live but a workflow line nobody can run, which is how the linux 100 s became darwin's number by default")
	}
	d, err := time.ParseDuration(strings.TrimSpace(raw))
	if err != nil {
		t.Fatalf("DARWIN_TIMEOUT = %q is not a Go duration: %v", raw, err)
	}
	if d.Seconds() < darwinQuietHostMargin*largest {
		t.Errorf("DARWIN_TIMEOUT = %s, under %.0fx the whole of the largest measured package (%s at %.1fs in %s); plan-merge opens a SINGLE slot for a group whose Linux sizes sum under 20 s, and that slot runs the package whole in one `go test`, so the share is not the bound — the whole is. The sizes are from a quiet host, which is what the stated margin is for", d, darwinQuietHostMargin, strings.TrimPrefix(largestName, modulePath), largest, darwinSizesPath)
	}

	// And the number must say where it comes from. A ceiling is a claim about a
	// machine, so the machine, the run that forced it and the margin belong in the
	// repository beside it.
	mkSrc := readFile(t, filepath.Join(root, "Makefile"))
	if !strings.Contains(mkSrc, darwinMeasurementRun) {
		t.Errorf("the Makefile does not say where DARWIN_TIMEOUT's number comes from (run %s); a ceiling is a claim about the machine and belongs in the repository with its measurement", darwinMeasurementRun)
	}

	// The table itself must record the conditions it was read under. "Measured on
	// a quiet host" is not a note, it is what makes the number meaningful: the
	// cancel that produced this table happened on a host in its post-power-on
	// state, and the same packages on the same machine answer differently then.
	tbl := readFile(t, filepath.Join(root, filepath.FromSlash(darwinSizesPath)))
	for _, want := range []string{"quiet", "margin"} {
		if !strings.Contains(strings.ToLower(tbl), want) {
			t.Errorf("%s does not say %q anywhere in its header; a size is only a size if the header says what the machine was doing when it was read, and a cap is only a cap if it states its margin", darwinSizesPath, want)
		}
	}
	if !strings.Contains(tbl, darwinMeasurementRun) {
		t.Errorf("%s does not name run %s, the cancelled merge group it exists to answer", darwinSizesPath, darwinMeasurementRun)
	}
}

// TestMergeGateDarwinLegDealsFromTheDarwinTable is the darwin half of
// integration-4's lesson, held shut.
//
// The merge group's darwin leg dealt its shards from the LINUX table until
// 2026-09-18: cmd/nova-bus at 10.0 s bought three shards, and on a thirteen-member
// batch shards 0 and 1 were cancelled at the five-minute leg cap with their
// packages still running. Linux could not have said otherwise — that package is
// 10.0 s on hulk and roughly 240 s on a loaded x64 Mac — so the darwin leg reads
// the darwin table, and its ceiling is the Makefile's DARWIN_TIMEOUT rather than
// the 100 s that linux keeps as its own.
func TestMergeGateDarwinLegDealsFromTheDarwinTable(t *testing.T) {
	root := repoRoot(t)
	src := readFile(t, filepath.Join(root, ".github", "workflows", "ci.yml"))
	step := stepBody(src, "test (full, the packages this group changes, shard ${{ matrix.shard }} of ${{ needs.plan-merge.outputs.slots }})")
	if strings.TrimSpace(step) == "" {
		t.Fatal("no merge-gate test step in ci.yml; the shard plan moved and this test is looking in the wrong place")
	}
	if !strings.Contains(step, darwinSizesPath) {
		t.Errorf("the merge gate's shard plan never reads %s; its darwin leg would deal from the Linux column again, which is how run %s's group was dropped", darwinSizesPath, darwinMeasurementRun)
	}
	// The leg must be told apart by name, and the darwin branch must read the
	// FULL column ($3) — the merge group runs without -short.
	if !strings.Contains(step, `leg=${{ matrix.leg.name }}`) || !strings.Contains(step, `[ "$leg" = "darwin" ]`) {
		t.Error("the merge gate's shard plan does not branch on the darwin leg; darwin would fall through to the Linux table, which is not its measurement")
	}
	if !strings.Contains(step, `darwin=$(cat `+darwinSizesPath+`)`) {
		t.Errorf("the merge gate's shard plan does not load %s into the darwin variable the branch reads", darwinSizesPath)
	}
	// Censored and unmeasured both get every slot the group opened. This is the
	// one rule that must never be relaxed: an unknown size guessed downward is
	// exactly what dropped integration-4's group and then batch 7's.
	// The branch is located inside the per-package loop and matched WITHOUT its
	// `if`/`elif` keyword: it was an `elif` while a windows branch stood in front
	// of it and became the `if` when the native windows legs were dropped on
	// 2026-09-18. Which keyword it carries says nothing about the rule; that it
	// reads the darwin table's FULL column and deals both unknown cases across
	// every slot does.
	darwinBranch := step
	if i := strings.Index(step, "for pkg in "); i < 0 {
		t.Error("the merge gate's shard plan has no per-package loop; the step moved and this test is reading the wrong text")
	} else if j := strings.Index(step[i:], `[ "$leg" = "darwin" ]; then`); j >= 0 {
		darwinBranch = step[i+j:]
	} else {
		t.Error("the merge gate's per-package loop has no darwin branch; without one the darwin table is read into a variable nobody uses")
	}
	for _, want := range []string{"''|'-')", "*+)", "print $3"} {
		if !strings.Contains(darwinBranch, want) {
			t.Errorf("the merge gate's darwin branch does not handle %s; an unknown darwin size must be dealt across every slot and never guessed downward, and the FULL column is the one this leg runs", want)
		}
	}
	if !strings.Contains(step, "make -s darwin-timeout") {
		t.Error("the merge gate's darwin leg does not take its ceiling from `make -s darwin-timeout`; the darwin number would be written twice and drift")
	}
	if !strings.Contains(step, "MERGE_TIMEOUT") {
		t.Error("the merge gate's darwin leg does not export MERGE_TIMEOUT; the Makefile's `?=` is what lets the darwin ceiling win over the default")
	}
	// Linux keeps the Linux table: this change moved darwin off it, not everyone.
	if !strings.Contains(step, "testdata/ci/package-sizes.tsv") {
		t.Error("the merge gate's shard plan no longer reads testdata/ci/package-sizes.tsv; the linux leg's own measurement is still its own")
	}

	// And the Makefile end of that handshake: a target that prints the number,
	// and a merge target that reads MERGE_TIMEOUT rather than a literal.
	mk := parseMakefile(t, filepath.Join(root, "Makefile"))
	want := "echo " + strings.TrimSpace(mk.vars["DARWIN_TIMEOUT"])
	if got := strings.Join(mk.recipeFor("darwin-timeout"), "\n"); !strings.Contains(got, want) {
		t.Errorf("`make darwin-timeout` runs %q, not %q; the workflow reads the darwin ceiling from this target, so it must print DARWIN_TIMEOUT itself and not a number that can drift from it", got, want)
	}
	if _, ok := mk.vars["MERGE_TIMEOUT"]; !ok {
		t.Error("the Makefile declares no MERGE_TIMEOUT; the darwin leg has no variable to override")
	}
}

// darwinForcingPackages are the packages whose MEASURED full darwin size is over
// the shard budget, so the plan must deal them across the slots the group opened.
// Naming them here means a table that quietly loses one is a red run.
//
// Four of these five are UNDER the 40 s budget in the Linux table the leg used to
// read — cmd/nova-wake 24.6, cmd/nova-merge 7.7, internal/swarm 17.4 and
// cmd/nova-bus 10.0 — so each was dealt three ways instead of six while their
// darwin wholes are 120.3, 68.8, 64.4 and 54.2 s. Only cmd/nova-swarm (51.0 s on
// Linux, 82.7 s here) was already called large by the table that leg was reading.
// That gap is the whole reason this file exists.
var darwinForcingPackages = []string{"cmd/nova-wake", "cmd/nova-swarm", "cmd/nova-merge", "internal/swarm", "cmd/nova-bus"}
