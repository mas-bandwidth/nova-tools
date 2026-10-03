package release

// THE ONE-COMMAND CYCLE (nova-tools#5096 item 12, 2026-10-02).
//
// The ask, 2026-10-02: fast iterations, fix and repeat. A fix merged to the
// foundation reached five benches in about 25 minutes: a whole
// cross-platform build, a waiver composed by hand for a gate that read
// receipts about tools nova-tools no longer ships, then the tools play's
// check and apply, each a command somebody typed. `release cycle` is those
// steps as one command from the coordinator: the tools play with --check, then
// the play (which builds the missing platforms --incremental, the gate in
// report mode under --reason), one line per bench with the version it now
// runs. The play stays the one place a build and an install are composed;
// this verb only drives it and reads its receipts.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bounded"
	"github.com/mas-bandwidth/nova-tools/internal/subproc"
)

// CycleNote is the verb said where a person meets it.
const CycleNote = "cycle is the fix-land-install cycle from the coordinator in one command: the tools play (<--source>/fleet/tools.yml) with --check, then the play itself, limited to --benches and localhost. " +
	"The play builds every missing platform with `release build --incremental --gate report --reason <why>`, copies only the binaries a bench does not already hold, and installs; " +
	"one CYCLE BENCH line per bench names the version it now runs, and both plays' output is kept under <--out>/<version>/cycle-check.log and cycle-apply.log. " +
	"--dry-run runs the check alone. It needs the inventory's environment (the store, the seat) exactly as the play does. A release cut keeps the refusing gate: cycle is for a machinery install during a sprint."

// Ansible is the edge to ansible-playbook: one run, its output whole.
type Ansible interface {
	Play(ctx context.Context, argv []string) (string, error)
}

// ExecAnsible is the production play runner: the named ansible-playbook,
// stdin closed (no prompt can wait for a person), stdout and stderr together.
type ExecAnsible struct{ Path string }

// playCap bounds one play's output: a five-bench run is tens of KiB.
const playCap = 8 * 1024 * 1024

func (a ExecAnsible) Play(ctx context.Context, argv []string) (string, error) {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	capture := bounded.NewCapture(playCap, cancel)
	cmd := subproc.Context(runCtx, a.Path, argv...)
	cmd.Stdout = capture
	cmd.Stderr = capture
	cmd.Env = append(os.Environ(), "ANSIBLE_NOCOLOR=1", "ANSIBLE_INVENTORY_UNPARSED_FAILED=true",
		"ANSIBLE_CALLBACKS_ENABLED=ansible.posix.profile_tasks")
	err := cmd.Run()
	return string(capture.Bytes()), err
}

// limitName is what --benches may hold: a machine name, never ansible's
// pattern syntax, so the limit is exactly the machines named.
var limitName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._@-]*$`)

var (
	toolsLine = regexp.MustCompile(`"(TOOLS host=[^"]*)"`)
	buildLine = regexp.MustCompile(`"(RELEASE BUILD (?:INCREMENTAL|WHOLE|DOGFOOD [A-Z]+|OK) [^"]*)"`)
	recapLine = regexp.MustCompile(`(?m)^(\S+)\s+: ok=\d+\s+changed=\d+\s+unreachable=(\d+)\s+failed=(\d+)`)
	failLine  = regexp.MustCompile(`(?m)^(fatal|failed): .*$`)
)

// benchReceipt is one bench as the play's receipt says it.
type benchReceipt struct {
	fields map[string]string
	state  string
}

// readReceipts reads each bench's TOOLS line and the recap's verdict.
func readReceipts(output string) map[string]benchReceipt {
	got := map[string]benchReceipt{}
	for _, m := range toolsLine.FindAllStringSubmatch(output, -1) {
		r := benchReceipt{fields: map[string]string{}}
		words := strings.Fields(m[1])
		if len(words) < 2 {
			continue
		}
		for _, w := range words[1:] {
			k, v, ok := strings.Cut(w, "=")
			if ok {
				if _, seen := r.fields[k]; !seen {
					r.fields[k] = v
				}
				continue
			}
			if r.state == "" {
				r.state = w
			}
		}
		got[r.fields["host"]] = r
	}
	for _, m := range recapLine.FindAllStringSubmatch(output, -1) {
		if m[2] != "0" || m[3] != "0" {
			r := got[m[1]]
			r.state = "FAILED"
			got[m[1]] = r
		}
	}
	return got
}

func cycle(ctx context.Context, o options, deps Deps, out, errs io.Writer) int {
	var benches []string
	for _, b := range strings.Split(o.benches, ",") {
		if b = strings.TrimSpace(b); b == "" {
			continue
		}
		if !limitName.MatchString(b) {
			return refusal(errs, "CYCLE", refuse("name each machine as the inventory does, comma-separated", "--benches holds %q, which is not a machine name", b))
		}
		benches = append(benches, b)
	}
	if len(benches) == 0 {
		return refusal(errs, "CYCLE", refuse("name at least one machine", "--benches is empty"))
	}
	play := filepath.Join(o.source, "fleet", "tools.yml")
	if !exists(play) {
		return refusal(errs, "CYCLE", refuse("name a nova-tools checkout with --source", "%s has no fleet/tools.yml", o.source))
	}
	receipts := o.receipts
	if receipts == "" {
		if home, err := os.UserHomeDir(); err == nil && exists(filepath.Join(home, DefaultReceiptsDir)) {
			receipts = filepath.Join(home, DefaultReceiptsDir)
		}
	}
	if receipts == "" {
		return refusal(errs, "CYCLE", refuse("name the dogfood receipts with --receipts <dir>", "no receipts directory was named and ~/%s does not exist", DefaultReceiptsDir))
	}
	buildArgs, err := json.Marshal(map[string][]string{"nova_release_build_args": {"--incremental", "--gate", "report", DogfoodReasonFlag, o.reason}})
	if err != nil {
		return refusal(errs, "CYCLE", err)
	}
	argv := []string{"-i", o.inventory, play,
		"-e", "nova_version=" + o.version, "-e", "nova_source=" + o.source, "-e", "nova_release_out=" + o.out,
		"-e", "nova_dogfood_receipts=" + receipts, "-e", string(buildArgs),
		"--limit", strings.Join(append(append([]string(nil), benches...), "localhost"), ",")}
	logs := filepath.Join(o.out, o.version)
	if err := os.MkdirAll(logs, 0o755); err != nil {
		return refusal(errs, "CYCLE", fmt.Errorf("cannot create %s: %w (name a writable --out)", logs, err))
	}
	ansible := deps.Ansible
	if ansible == nil {
		ansible = ExecAnsible{Path: o.ansible}
	}
	began := deps.Now()
	run := func(step string, extra ...string) (string, map[string]benchReceipt, bool) {
		progress(errs, "running the tools play%s on %s", map[bool]string{true: " --check", false: ""}[step == "check"], strings.Join(benches, ","))
		output, err := ansible.Play(ctx, append(append([]string(nil), argv...), extra...))
		log := filepath.Join(logs, "cycle-"+step+".log")
		if werr := writeNoFollow("write play log", log, []byte(output), 0o644); werr != nil {
			progress(errs, "cannot keep the play's output at %s: %v", log, werr)
		}
		got := readReceipts(output)
		ok := err == nil
		for _, b := range benches {
			if r, seen := got[b]; !seen || r.state == "FAILED" {
				ok = false
			}
		}
		if !ok {
			for _, l := range failLine.FindAllString(output, 10) {
				if len(l) > 300 {
					l = l[:300] + "..."
				}
				fmt.Fprintln(errs, l)
			}
			reason := "a bench has no receipt"
			if err != nil {
				reason = oneLine("", err)
			}
			fmt.Fprintf(errs, "CYCLE FAIL step=%s version=%s reason=%s log=%s\n", step, field(o.version), field(reason), field(log))
		}
		return output, got, ok
	}
	_, checked, ok := run("check", "--check")
	checkTook := deps.Now().Sub(began)
	if !ok {
		return 1
	}
	if o.dryRun {
		for _, b := range benches {
			r := checked[b]
			fmt.Fprintf(out, "CYCLE WOULD host=%s platform=%s version=%s was=%s state=%s\n",
				field(b), field(r.fields["platform"]), field(o.version), field(r.fields["was"]), field(r.state))
		}
		fmt.Fprintf(out, "CYCLE DRY-RUN version=%s benches=%d check=%s\n", field(o.version), len(benches), checkTook.Round(time.Second))
		return 0
	}
	applied, got, ok := run("apply")
	for _, m := range buildLine.FindAllStringSubmatch(applied, -1) {
		fmt.Fprintln(out, strings.ReplaceAll(m[1], `\\`, `\`)) // the play prints it JSON-quoted
	}
	changed := 0
	for _, b := range benches {
		r := got[b]
		if r.state == "INSTALLED" {
			changed++
		}
		fmt.Fprintf(out, "CYCLE BENCH host=%s platform=%s version=%s was=%s state=%s installed=%s skipped=%s\n",
			field(b), field(r.fields["platform"]), field(r.fields["version"]), field(r.fields["was"]), field(r.state),
			field(r.fields["tools"]), field(r.fields["skipped"]))
	}
	if !ok {
		return 1
	}
	total := deps.Now().Sub(began)
	fmt.Fprintf(out, "CYCLE OK version=%s benches=%d changed=%d check=%s apply=%s total=%s logs=%s\n",
		field(o.version), len(benches), changed, checkTook.Round(time.Second), (total - checkTook).Round(time.Second),
		total.Round(time.Second), field(logs))
	return 0
}
