// nova-pulse lineup: the sprint's preflight as a verb (#2562), replacing bin/sprint-lineup.
//
// `nova-pulse lineup --profile coding` asks every bench the coding profile's questions
// BEFORE the first card -- stage W/W in 60 s, a push credential, a writable results root, no
// finished job dirs left, the wanted build of every nova tool -- and lints the launchers
// for one ssh session per card. One LINEUP row per bench per check, RED or GREEN, one
// closing LINEUP line, and exit 1 on any RED. Exit 2 is a refusal: the verb was not given
// enough to ask anything.
//
// The rules live in internal/pulse/lineup; this file is flags and printing, and
// lineup_bench.go is how a bench's facts are fetched (one ssh session per bench, or a
// captured FACT file with --facts).
package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/pulse/lineup"
)

// lineupProfiles are the profiles this verb answers. Each is a fixed set of checks, never a
// list the caller composes: a lineup that can be told to skip a check is a lineup that
// will be, the night it matters.
var lineupProfiles = map[string]bool{"coding": true}

func cmdLineup(args []string, stdout, stderr io.Writer) int {
	f := newFlags("lineup")
	profile := f.fs.String("profile", "", "")
	want := f.fs.String("want", "", "")
	benchesPath := f.fs.String("benches", "", "")
	var benchNames, facts, launchers repeatable
	f.fs.Var(&benchNames, "bench", "")
	f.fs.Var(&facts, "facts", "")
	f.fs.Var(&launchers, "launcher", "")
	sshProg := f.fs.String("ssh", "", "")
	timeout := f.fs.Duration("timeout", 120*time.Second, "")
	results := f.fs.String("results", "", "")
	roots := f.fs.String("roots", "", "")
	stageReceipt := f.fs.String("stage-receipt", "", "")
	stageCmd := f.fs.String("stage-cmd", "", "")
	stageMax := f.fs.Duration("stage-max", 60*time.Second, "")
	stageAge := f.fs.Duration("stage-age", 24*time.Hour, "")
	ghConfig := f.fs.String("gh-config", "", "")
	tools := f.fs.String("tools", strings.Join(lineup.DefaultTools, ","), "")
	jobGrace := f.fs.Int("job-grace", 10, "")
	if !f.parse(args, stderr) {
		return 2
	}
	f.want(*profile, "profile", "the profile to line up: coding")
	if *profile != "" && !lineupProfiles[*profile] {
		f.add(fmt.Sprintf("--profile %q is not a profile; the profiles are: coding", *profile))
	}
	f.want(*want, "want", "the nova build stamp every bench must run (a substring of `nova-swarm version`, e.g. the dev tip's 12 hex)")
	if strings.TrimSpace(*benchesPath) == "" && len(facts) == 0 {
		f.add("--benches <file> or --facts <bench>=<file> is required; a lineup of no bench is not a GREEN one")
	}
	if len(benchNames) > 0 && strings.TrimSpace(*benchesPath) == "" {
		f.add("--bench names a bench of the --benches file; give --benches")
	}
	if *stageMax <= 0 {
		f.add(fmt.Sprintf("--stage-max wants a positive duration, got %s", *stageMax))
	}
	if *timeout <= 0 {
		f.add(fmt.Sprintf("--timeout wants a positive duration, got %s", *timeout))
	}
	if *jobGrace < 0 {
		f.add(fmt.Sprintf("--job-grace wants whole minutes, 0 or more, got %d", *jobGrace))
	}
	if f.refused(stderr) {
		return 2
	}

	policy := lineup.Policy{Want: strings.TrimSpace(*want), Tools: splitList(*tools), StageMax: *stageMax, StageAge: *stageAge}
	probe := lineup.ProbeOptions{
		Results: *results, Roots: splitList(*roots), StageReceipt: *stageReceipt, StageCmd: *stageCmd,
		GHConfig: *ghConfig, Tools: policy.Tools, JobGraceMin: *jobGrace,
	}

	sources, code := lineupSources(*benchesPath, benchNames, facts, stderr)
	if code != 0 {
		return code
	}
	if len(sources) == 0 {
		fmt.Fprintf(stderr, "nova-pulse lineup: %s names no bench; a lineup of no bench is not a GREEN one\n", oneline.Field(*benchesPath))
		return 2
	}
	var rows []lineup.Row
	for _, got := range lineupFetch(sources, probe, *sshProg, *timeout) {
		if got.err != "" {
			rows = append(rows, lineup.Unreachable(got.bench, got.err))
			continue
		}
		rows = append(rows, lineup.Coding(got.bench, got.facts, policy)...)
	}
	launcherRows, paths := lineupDefaultLauncher(launchers)
	rows = append(rows, launcherRows...)
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			rows = append(rows, lineup.Row{Bench: "fleet", Check: "launcher", Status: lineup.Red,
				Detail: "got=UNREADABLE file=" + oneline.Field(path)})
			continue
		}
		rows = append(rows, lineup.LintLauncher(path, string(raw)))
	}
	for _, r := range rows {
		fmt.Fprintln(stdout, r.String())
	}
	line, exit := lineup.Verdict(*profile, rows)
	fmt.Fprintln(stdout, line)
	return exit
}

// lineupDefaultLauncher is the launcher lint's input when no --launcher is given: the
// per-card launcher `nova-pulse fill` hands every card to by default (flashLauncher's
// flash-native-bench.sh, found on PATH). The launcher row is one of the coding profile's
// fixed checks, so it is never skipped: with no --launcher and none on PATH the row is RED
// MISSING, never absent (#2963: a bench with green facts exited 0 with no launcher read).
func lineupDefaultLauncher(given []string) ([]lineup.Row, []string) {
	if len(given) > 0 {
		return nil, given
	}
	const name = "flash-native-bench.sh"
	path, err := exec.LookPath(name)
	if err != nil {
		return []lineup.Row{{Bench: "fleet", Check: "launcher", Status: lineup.Red,
			Detail: "got=MISSING file=" + name + " why=" + oneline.Field("no --launcher given and no "+name+" on PATH: the per-card launcher fill uses is unread")}}, nil
	}
	return nil, []string{path}
}

// splitList reads a comma-separated flag, dropping empties.
func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
