package ci

import (
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// workflows_rows_test.go is #2925: nova-tools Actions keeps only the
// runner-only rows, and every Linux or darwin `go test` row runs as a ci card
// (#2842: a CI pass is a script card under the dealer's slot share, its verdict
// in ci:<repo>:<sha>) instead of as a second scheduler on the swarm benches
// (#2795: vision at load 97 on 64 cores, ~39 of them Go compile/link from nine
// CI shards).
//
// THE SWAP IS A FLAG, NOT YET A DELETION. Ready = dependencies merged, and the
// two this rests on are not on dev yet: the ci card itself (#2842, PR #2886)
// and the lander reading CI from ci:<repo>:<sha> instead of check-runs (#2924).
// Until both land, deleting the rows would leave a PR with no test evidence the
// lander reads. So every Linux or darwin `go test` job carries ONE gate as the
// first conjunct of its job-level `if:`,
//
//	vars.NOVA_CI_CARDS != 'on'
//
// and the aggregators that read those jobs (ci-ok, certification-ok) accept
// `skipped` for exactly those jobs when the flag is on. With the repository
// variable unset (today) nothing changes; setting NOVA_CI_CARDS=on is the swap,
// and a follow-up deletes the gated jobs and this allowance together.
//
// The DONE-WHEN is read with the flag ON: no job runs a Linux or darwin
// `go test` row except the runner-only rows named below, each with its reason,
// and those stay ungated.

// ciCardsGate is the flag every Linux/darwin go-test job's `if:` opens with.
const ciCardsGate = "vars.NOVA_CI_CARDS != 'on'"

// runnerOnlyRows are the go-test jobs that stay on Actions with the flag on,
// keyed workflow-file/job. A ci card cannot stand in for them: what they prove
// is the runner or the platform, not the change. The issue's third kind,
// s390x, has no row in nova-tools (it is a rocketnet row); Windows stays as
// certification's smoke leg, which runs the built binary, not `go test`, and is
// asserted below.
var runnerOnlyRows = map[string]string{
	"ci.yml/test-hosted-pr": "the hosted platform selftest: internal/sandbox on the hosted Linux kernel, the one platform the self-hosted matrix cannot see",
	"ci.yml/fleet-probe":    "proves a self-hosted runner itself; workflow_dispatch only, with a bench named",
}

func TestWorkflowsKeepOnlyRunnerOnlyRows(t *testing.T) {
	root := repoRoot(t)
	goTestTargets := makeGoTestTargets(t, readFile(t, filepath.Join(root, "Makefile")))
	if !goTestTargets["test"] {
		t.Fatalf("the Makefile parser found no go-test target named test (found %v); it is reading the wrong file", goTestTargets)
	}

	dir := filepath.Join(root, ".github", "workflows")
	files, err := filepath.Glob(filepath.Join(dir, "*.yml"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no workflows under %s: %v", dir, err)
	}
	sort.Strings(files)

	gated := map[string]map[string]bool{} // file -> gated job names
	seenRunnerOnly := map[string]bool{}
	windowsRow := false
	rows := 0
	for _, path := range files {
		file := filepath.Base(path)
		blocks := jobBlocks(readFile(t, path))
		if len(blocks) == 0 {
			t.Errorf("%s: no jobs parsed; the parser is looking in the wrong place", file)
			continue
		}
		gated[file] = map[string]bool{}
		for _, name := range sortedKeys(blocks) {
			block := blocks[name]
			code := stripComments(block)
			if strings.Contains(code, "windows-latest") {
				windowsRow = true
			}
			if !runsGoTest(code, goTestTargets) || !runsOnLinuxOrDarwin(code) {
				continue
			}
			rows++
			key := file + "/" + name
			cond := jobIf(block)
			if _, ok := runnerOnlyRows[key]; ok {
				seenRunnerOnly[key] = true
				if strings.Contains(cond, "NOVA_CI_CARDS") {
					t.Errorf("%s is a runner-only row (%s) and must stay on Actions with the flag on, but its if: reads NOVA_CI_CARDS: %q", key, runnerOnlyRows[key], cond)
				}
				continue
			}
			if !gatedByCICards(cond) {
				t.Errorf("%s runs a Linux or darwin `go test` row on Actions with NOVA_CI_CARDS=on; its job-level if: must open with %q (as `%s && (<the old guard>)`), got %q", key, ciCardsGate, ciCardsGate, cond)
				continue
			}
			gated[file][name] = true
		}
	}
	if rows == 0 {
		t.Fatal("no Linux or darwin go-test job found in any workflow; the detector is broken, not the tree clean")
	}
	for key, why := range runnerOnlyRows {
		if !seenRunnerOnly[key] {
			t.Errorf("runner-only row %s (%s) is gone or no longer runs go test; the issue keeps it, so either restore it or drop it from runnerOnlyRows with the ruling", key, why)
		}
	}
	if !windowsRow {
		t.Error("no windows-latest row left in any workflow; the issue keeps the Windows row on Actions")
	}

	// An aggregator that reads a gated job's result with always() must accept
	// `skipped` for it under the flag, or setting the flag turns it red.
	for _, path := range files {
		file := filepath.Base(path)
		blocks := jobBlocks(readFile(t, path))
		for _, name := range sortedKeys(blocks) {
			block := blocks[name]
			if !strings.Contains(jobIf(block), "always()") {
				continue
			}
			accepted := skipAccepted(block)
			for _, need := range jobNeeds(block) {
				if !gated[file][need] {
					continue
				}
				if !accepted[need] {
					t.Errorf("%s/%s reads %s, which is skipped when NOVA_CI_CARDS=on, but does not accept skipped for it under the flag; add it to the `case \"$job\" in ...)` line guarded by NOVA_CI_CARDS", file, name, need)
				}
			}
		}
	}
}

// makeGoTestTargets returns the Makefile targets whose recipe runs `$(GO) test`,
// so `make test-short` in a workflow is read as the go test it is and
// `make test-lisp` is not.
func makeGoTestTargets(t *testing.T, mk string) map[string]bool {
	t.Helper()
	target := regexp.MustCompile(`^([A-Za-z0-9_.-]+):`)
	out := map[string]bool{}
	cur := ""
	for _, line := range strings.Split(mk, "\n") {
		if strings.HasPrefix(line, "\t") {
			if cur != "" && strings.Contains(line, "$(GO) test") {
				out[cur] = true
			}
			continue
		}
		if m := target.FindStringSubmatch(line); m != nil {
			cur = m[1]
			continue
		}
		if strings.TrimSpace(line) != "" {
			cur = ""
		}
	}
	return out
}

var makeCallRe = regexp.MustCompile(`\bmake\s+([A-Za-z0-9_.-]+)`)

// runsGoTest reports whether a job's code (comments stripped) runs go test,
// directly or through a make target that does.
func runsGoTest(code string, targets map[string]bool) bool {
	if regexp.MustCompile(`\bgo test\b`).MatchString(code) {
		return true
	}
	for _, m := range makeCallRe.FindAllStringSubmatch(code, -1) {
		if targets[m[1]] {
			return true
		}
	}
	return false
}

// runsOnLinuxOrDarwin reports whether a job names a Linux or macOS runner. A
// self-hosted runner is one of ours, and every bench is Linux or macOS (the
// Threadripper is WSL2, a Linux bench), so self-hosted counts too.
func runsOnLinuxOrDarwin(code string) bool {
	return regexp.MustCompile(`(?i)ubuntu|linux|macos|darwin|self-hosted`).MatchString(code)
}

// stripComments drops every `#` comment, so prose naming `go test` or a runner
// is not read as the thing itself.
func stripComments(block string) string {
	var b strings.Builder
	for _, line := range strings.Split(block, "\n") {
		if j := strings.Index(line, "#"); j >= 0 {
			line = line[:j]
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String()
}

// jobIf returns a job's own `if:` expression (four-space indent), or "".
func jobIf(block string) string {
	for _, line := range strings.Split(block, "\n") {
		if strings.HasPrefix(line, "    if:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "    if:"))
		}
	}
	return ""
}

// gatedByCICards reports whether an if: expression is the gate alone or the
// gate AND the whole old guard in parentheses, so no `||` in the old guard can
// reopen the job with the flag on.
func gatedByCICards(cond string) bool {
	if cond == ciCardsGate {
		return true
	}
	rest, ok := strings.CutPrefix(cond, ciCardsGate+" && (")
	if !ok || !strings.HasSuffix(rest, ")") {
		return false
	}
	depth := 1
	inner := rest[:len(rest)-1]
	for _, r := range inner {
		switch r {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return false // the opening paren closed before the end
			}
		}
	}
	return depth == 1
}

// jobNeeds returns the job names in a job's `needs:` (list or single name).
func jobNeeds(block string) []string {
	for _, line := range strings.Split(block, "\n") {
		if !strings.HasPrefix(line, "    needs:") {
			continue
		}
		v := strings.TrimSpace(strings.TrimPrefix(line, "    needs:"))
		v = strings.TrimSuffix(strings.TrimPrefix(v, "["), "]")
		var out []string
		for _, n := range strings.Split(v, ",") {
			if n = strings.TrimSpace(n); n != "" {
				out = append(out, n)
			}
		}
		return out
	}
	return nil
}

var skipCaseRe = regexp.MustCompile(`case "\$job" in ([A-Za-z0-9_|-]+)\)`)

// skipAccepted returns the jobs an aggregator lets through as skipped under
// the flag: the names on a `case "$job" in a|b)` line in a block that reads
// NOVA_CI_CARDS.
func skipAccepted(block string) map[string]bool {
	out := map[string]bool{}
	if !strings.Contains(block, "vars.NOVA_CI_CARDS") {
		return out
	}
	for _, m := range skipCaseRe.FindAllStringSubmatch(block, -1) {
		for _, n := range strings.Split(m[1], "|") {
			out[n] = true
		}
	}
	return out
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
