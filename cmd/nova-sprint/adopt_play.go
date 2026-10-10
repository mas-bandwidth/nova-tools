package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/release"
)

// The adopt verb is the seat's adoption (docs/SPEC-SPRINT.md, "Adopting a
// build"): it runs the fleet's tools play (fleet/tools.yml) for the seat, as
// a window. The new build's checks come first and change nothing (its own
// nova-sprint live, its shadow tick on the store, its nova-friend install
// --dry-run with every friend daemon's flags); then the old server and
// member are stopped and seen gone, the configuration store migrated as its
// owning role (its own nova-config migrate --window, after a read-only dry run
// as the owner before the window), the library loaded, the tools installed on fresh inodes, every stopped or stale
// agent started and proved, the dashboard links pointed at the installed
// binary and each stale friend daemon reinstalled, each step checked by the
// manifest (nova-sprint live) before the next. A refusal once the window
// opened puts the tools and library of before back and starts the old agents
// on them. It prints the play's ADOPT line of each step, and refuses a half
// move: a play that stops, or ends without the line of every step, is a
// refusal naming the step, and running it again finishes it (the play is
// idempotent). The verb has no flag that runs a step alone.
func init() {
	notServed = append(notServed, "adopt")
	verbClasses["adopt"] = classMachine
	// it runs ansible against the fleet: it runs where it is typed or
	// scheduled, never on the server
	notServed = append(notServed, "adopt")
	verbExit["adopt"] = "exit codes: 0 every step of the seat adopted the build (or, with --dry-run, said what it would change), 1 the play stopped or left a step without its line (ADOPT REFUSED step=<step>, with what the rollback did when the window had opened: the steps before it are done, or rolled back, and the play runs again to finish), 2 usage"
	verbEffect["adopt"] = "local and remote writes through ansible-playbook: the tools play builds the version if missing and runs the new build's checks on the seat (shadow tick, nova-friend install --dry-run) before anything changes; then, in a window, it stops the seat's old server and member (bootout, seen gone in ps), migrates the configuration store as its owning role (nova-config migrate --window, refusing while any other nova session holds the database), loads the function library, installs the tools and bootstraps every stopped or stale nova launchd agent, points the dashboard links at the installed nova-sprint and reinstalls each stale friend daemon with nova-friend install; a refusal in the window puts the tools and library of before back and restarts the old agents (the migration is never undone); --dry-run runs the play with --check and writes nothing"
}

// adoptPlaySteps are the steps of the seat play, in order: one ADOPT line each.
var adoptPlaySteps = []string{"store", "server", "dashboard", "friends"}

var (
	// a JSON string holding an ADOPT line, escaped quotes and all
	adoptLine    = regexp.MustCompile(`"ADOPT (?:[^"\\]|\\.)*"`)
	adoptStepRe  = regexp.MustCompile(`\bstep=(\S+)`)
	adoptHostRe  = regexp.MustCompile(`\bhost=(\S+)`)
	adoptTaskRe  = regexp.MustCompile(`(?m)^(?:TASK|RUNNING HANDLER) \[([^\]]*)\]`)
	adoptFatalRe = regexp.MustCompile(`(?m)^(?:fatal|failed): .*$`)
)

// adoptPlayOf is a test's play runner for one app (*app to release.Ansible).
var adoptPlayOf sync.Map

// adoptReport is what the play's output says: the ADOPT lines in order, the
// steps each host printed, the first refusal (the failing check's), the steps
// its host printed before it, and what the seat play's rescue said the
// rollback did (its later refusal line, from "rollback: " or "the window never
// opened").
type adoptReport struct {
	lines    []string
	steps    map[string]map[string]bool
	refused  string
	before   []string
	rollback string
}

// adoptRollbackRe finds what the rescue's refusal says the rollback did.
var adoptRollbackRe = regexp.MustCompile(`(?:^|; )((?:rollback: |the window never opened).*)$`)

func readAdopt(output string) adoptReport {
	r := adoptReport{steps: map[string]map[string]bool{}}
	var order [][2]string // host, step: each step line before the first refusal
	for _, q := range adoptLine.FindAllString(output, -1) {
		var text string
		if json.Unmarshal([]byte(q), &text) != nil {
			text = strings.Trim(q, `"`)
		}
		l := strings.Join(strings.Fields(text), " ")
		if len(r.lines) > 0 && r.lines[len(r.lines)-1] == l {
			continue
		}
		r.lines = append(r.lines, l)
		if strings.HasPrefix(l, "ADOPT REFUSED ") {
			if m := adoptRollbackRe.FindStringSubmatch(l); m != nil {
				r.rollback = m[1]
			}
			if r.refused == "" {
				r.refused = strings.TrimSuffix(strings.TrimPrefix(l, "ADOPT REFUSED "), "; "+r.rollback)
				if host := adoptHostRe.FindStringSubmatch(l); host != nil {
					for _, o := range order {
						if o[0] == strings.TrimSuffix(host[1], ":") && !slices.Contains(r.before, o[1]) {
							r.before = append(r.before, o[1])
						}
					}
				}
			}
			continue
		}
		step, host := adoptStepRe.FindStringSubmatch(l), adoptHostRe.FindStringSubmatch(l)
		if step == nil || host == nil {
			continue
		}
		if r.refused == "" {
			order = append(order, [2]string{host[1], step[1]})
		}
		if r.steps[host[1]] == nil {
			r.steps[host[1]] = map[string]bool{}
		}
		r.steps[host[1]][step[1]] = true
	}
	return r
}

// failedTask is the play task that failed: the last TASK or RUNNING HANDLER
// header before the first fatal line, its step the word before its colon.
func failedTask(output string) string {
	fatal := adoptFatalRe.FindStringIndex(output)
	if fatal == nil {
		return ""
	}
	tasks := adoptTaskRe.FindAllStringSubmatch(output[:fatal[0]], -1)
	if len(tasks) == 0 {
		return ""
	}
	return tasks[len(tasks)-1][1]
}

func (a *app) cmdAdoptPlay(args []string, stdout, stderr io.Writer) int {
	const name = "adopt"
	fs, _ := a.verbSetup(name)
	source := fs.String("source", "", "the nova-tools checkout the build is made from; its fleet/tools.yml is the play")
	inventory := fs.String("inventory", a.getenv("NOVA_INVENTORY"), "the inventory the play reads, the nova-inventory script (else NOVA_INVENTORY)")
	limit := fs.String("limit", "", "the one machine to adopt on, as the inventory names it (default: the coordinator group, the seat)")
	receipts := fs.String("receipts", "", "the dogfood receipts directory (default ~/"+release.DefaultReceiptsDir+")")
	reason := fs.String("reason", "", "why this build is adopted now: the build's dogfood gate reports it and does not refuse")
	ansible := fs.String("ansible", "ansible-playbook", "the ansible-playbook binary")
	dry := fs.Bool("dry-run", false, "run the play with --check: each step says WOULD and nothing is written")
	pos, err := parse(fs, args)
	if err != nil || len(pos) != 1 {
		return refuse(stderr, name, argErr("wants one <version|path> ", err, pos...))
	}
	// <path> is a built release directory, <out>/<version>; else the version.
	version, out := pos[0], ""
	if fi, err := os.Stat(version); err == nil && fi.IsDir() {
		abs, err := filepath.Abs(version)
		if err != nil {
			return refuse(stderr, name, oneline.Err(err))
		}
		version, out = filepath.Base(abs), filepath.Dir(abs)
	}
	if err := release.ValidVersion(version); err != nil {
		return refuse(stderr, name, "<version|path>: "+oneline.Err(err))
	}
	play := filepath.Join(*source, "fleet", "tools.yml")
	switch {
	case *source == "":
		return refuse(stderr, name, "--source names the nova-tools checkout whose fleet/tools.yml is the play")
	case *inventory == "":
		return refuse(stderr, name, "--inventory (or NOVA_INVENTORY) names the inventory the play reads")
	case strings.TrimSpace(*reason) == "":
		return refuse(stderr, name, "--reason says why this build is adopted now")
	case *limit != "" && (strings.ContainsAny(*limit, ",:!&*[") || strings.TrimSpace(*limit) != *limit):
		return refuse(stderr, name, "--limit names one machine as the inventory does, found "+*limit)
	}
	if _, err := os.Stat(play); err != nil {
		// an input that does not read, not a usage: exit 1, nothing run
		fmt.Fprintf(stderr, "%s adopt REFUSED step=play: --source %s holds no fleet/tools.yml (%s); nothing was run; run: nova-sprint adopt -h\n", prog, *source, oneline.Err(err))
		return 1
	}
	if *receipts == "" {
		*receipts = filepath.Join(a.getenv("HOME"), release.DefaultReceiptsDir)
	}
	buildArgs, err := json.Marshal(map[string][]string{"nova_release_build_args": {"--incremental", "--gate", "report", release.DogfoodReasonFlag, *reason}})
	if err != nil {
		return refuse(stderr, name, oneline.Err(err))
	}
	seat := "coordinator"
	if *limit != "" {
		seat = *limit
	}
	argv := []string{"-i", *inventory, play, "-e", "nova_version=" + version, "-e", "nova_source=" + *source,
		"-e", "nova_dogfood_receipts=" + *receipts, "-e", string(buildArgs), "--limit", seat + ",localhost,store_deployer"}
	if out != "" {
		argv = append(argv, "-e", "nova_release_out="+out)
	}
	if *dry {
		argv = append(argv, "--check")
	}
	var runner release.Ansible = release.ExecAnsible{Path: *ansible}
	if fake, ok := adoptPlayOf.Load(a); ok {
		runner = fake.(release.Ansible)
	}
	output, playErr := runner.Play(context.Background(), argv)
	r := readAdopt(output)
	for _, l := range r.lines {
		if strings.HasPrefix(l, "ADOPT REFUSED ") {
			continue
		}
		if *dry {
			if strings.Contains(l, " WOULD-CHANGE") {
				fmt.Fprintln(stdout, oneline.Escape(strings.Replace(l, "ADOPT ", "ADOPT WOULD ", 1)))
			}
		} else {
			fmt.Fprintln(stdout, oneline.Escape(l))
		}
	}
	again := "fix the cause and run the same adopt again (the play is idempotent: it changes only what is still stale)"
	finish := "the steps before it are done and the ones after it did not run; " + again
	switch {
	case r.refused != "":
		said := r.refused
		if r.rollback != "" {
			said += "; " + r.rollback
		}
		if strings.HasPrefix(r.rollback, "rollback: ") {
			// the rollback undid what the window did: no step before it stands
			undone := strings.Join(r.before, ",")
			if undone == "" {
				undone = "the window"
			}
			finish = "rolled back: " + undone + "; the ones after it did not run; " + again
		}
		fmt.Fprintf(stderr, "%s adopt REFUSED %s; %s; run: nova-sprint live\n", prog, oneline.Escape(said), finish)
		return 1
	case playErr != nil:
		task := failedTask(output)
		step, _, _ := strings.Cut(task, ":")
		if *dry {
			if step == "" {
				step = "play"
			}
			fatal := adoptFatalRe.FindString(output)
			if fatal == "" {
				fatal = oneline.Err(playErr)
			}
			fmt.Fprintf(stderr, "%s adopt: ADOPT REFUSED step=%s dry-run=yes: %s; run: fix the failed play task and repeat the same nova-sprint adopt --dry-run command\n", prog, step, oneline.Escape(truncateLine(fatal, 300)))
			return 1
		}
		if !slices.Contains(adoptPlaySteps, step) {
			step = "play"
		}
		for _, l := range adoptFatalRe.FindAllString(output, 5) {
			fmt.Fprintln(stderr, oneline.Escape(truncateLine(l, 300)))
		}
		fmt.Fprintf(stderr, "%s adopt REFUSED step=%s task=%q: %s; %s; run: nova-sprint live\n", prog, step, task, oneline.Err(playErr), finish)
		return 1
	case len(r.steps) == 0:
		fmt.Fprintf(stderr, "%s adopt REFUSED step=seat: the play printed no ADOPT line, so no machine of %s is a seat (the coordinator group); nothing of the seat was adopted; run: nova-sprint adopt --limit <the coordinator machine>\n", prog, seat)
		return 1
	}
	hosts := slices.Sorted(func(yield func(string) bool) {
		for h := range r.steps {
			if !yield(h) {
				return
			}
		}
	})
	for _, h := range hosts {
		if r.steps[h]["seat"] && *dry {
			continue // --check before the first adoption: the installed build has no manifest
		}
		for _, s := range adoptPlaySteps {
			if !r.steps[h][s] {
				fmt.Fprintf(stderr, "%s adopt REFUSED step=%s host=%s: a half move, the play ended without this step's line; %s; run: nova-sprint live\n", prog, s, h, finish)
				return 1
			}
		}
	}
	word := "ADOPTED"
	if *dry {
		if !slices.ContainsFunc(r.lines, func(l string) bool {
			return strings.Contains(l, " WOULD-CHANGE") || strings.Contains(l, " UNCHANGED")
		}) {
			fmt.Fprintf(stdout, "ADOPT WOULD-ADOPT version=%s hosts=%s steps=%s\n", version, strings.Join(hosts, ","), strings.Join(adoptPlaySteps, ","))
		}
		steps := 0
		for _, l := range r.lines {
			if strings.HasPrefix(l, "ADOPT step=") && strings.Contains(l, " WOULD-CHANGE") {
				steps++
			}
		}
		fmt.Fprintf(stdout, "ADOPT DRY-RUN OK steps=%d\n", steps)
		return 0
	}
	fmt.Fprintf(stdout, "ADOPT %s version=%s hosts=%s steps=%s\n", word, version, strings.Join(hosts, ","), strings.Join(adoptPlaySteps, ","))
	return 0
}

func truncateLine(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
