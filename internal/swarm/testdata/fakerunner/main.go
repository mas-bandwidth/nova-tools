// Command fakerunner is the ONE fake runner the batch tests drive, and it is a real
// executable on every platform.
//
// WHY IT EXISTS. The batch tests used to write POSIX shell scripts -- runner.sh, writing.sh,
// silent.sh, record.sh and a dozen more -- and hand their paths to `nova-swarm batch
// --runner`. That is not what a runner is: SPEC-SWARM's usage line says
// `--runner <cmd>`, batch.go's own refusal says "--runner is required; it wants the command
// one process per card runs", and batch.go starts it with
// `exec.Command(in.Runner, label, slot, model, card, root)` -- a plain exec of an
// executable, never a shell. The FIXTURES were the unix-only part, not the product: on
// windows-latest every one of them died with
// `fork/exec C:\...\runner.sh: %1 is not a valid Win32 application`.
//
// WHAT IT IS. One tiny Go program, built ONCE per test package (TestMain), then copied under
// a different name per fixture. Its behaviour is a list of STEPS read from a JSON file
// beside its own executable (`<exe>.json`), so one build serves every fixture and no test
// pays for a compile of its own. The steps are the things the shell scripts did: make a
// directory, write or append a file, copy one, record its own argv, say a line on stdout,
// sleep, burn CPU in a grandchild, publish a RESULT.md, exit with a code -- each optionally
// guarded by the card's label or the second line of its text, which is how the scripts
// branched.
//
// Argv is the contract. A `--runner` fixture is handed label, slot, model, card path, root;
// the same binary stands in for nova-swarm itself, so it is also run as `nova-swarm native`
// with the native flags (issue #636), parsed the way native.go parses them.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// step is one thing the fake runner does. Everything is optional but Op; Path and Body are
// expanded (see expand) before use.
type step struct {
	Op   string `json:"op"`             // see run below
	Path string `json:"path,omitempty"` // the file or directory the step acts on
	Body string `json:"body,omitempty"` // the text it writes, or the source path a copy reads
	N    int    `json:"n,omitempty"`    // a repeat count, or an exit code
	Ms   int    `json:"ms,omitempty"`   // a duration in milliseconds
	When string `json:"when,omitempty"` // a guard: label==a, label!=d, line2==MISSING, ...
}

type spec struct {
	Steps []step `json:"steps"`
}

// SPIN IS THE ONE MODE THAT IS NOT A CARD. A `spin` step re-executes this binary with this
// argument so the CPU burner is a GRANDCHILD of the card -- the batch's idle monitor reads
// the whole process tree's CPU time, and a burner that is not in the tree proves nothing.
const spinArg = "--spin-ms"

func main() {
	if len(os.Args) == 3 && os.Args[1] == spinArg {
		ms, _ := strconv.Atoi(os.Args[2])
		burn(time.Duration(ms) * time.Millisecond)
		return
	}
	var r *runner
	// A runnerless batch (issue #636) runs the card through `nova-swarm native`, so a fixture
	// standing in for the nova-swarm binary itself is handed the native flags, not the five
	// runner arguments. The steps are the same; parsing the flags keeps the self's `{label}`,
	// `{root}` and guards as exact as a `--runner` fixture's. A native invocation is never
	// exactly five positional arguments, which is what separates it from the runner contract.
	// SIX RUNNER ARGUMENTS SINCE RULE 13d (issue #1545): label, slot, model, card, root and
	// the budget word. The count is what separates a `--runner` invocation from a `native`
	// one, so it moved with the contract.
	if len(os.Args) > 1 && os.Args[1] == "native" && len(os.Args) != 7 {
		n, err := nativeRunner(os.Args[2:])
		if err != nil {
			fmt.Fprintf(os.Stderr, "fakerunner: %v\n", err)
			os.Exit(2)
		}
		r = n
	} else {
		if len(os.Args) != 7 {
			fmt.Fprintf(os.Stderr, "fakerunner: want 6 arguments (label slot model card root tokens), got %d\n", len(os.Args)-1)
			os.Exit(2)
		}
		r = &runner{label: os.Args[1], slot: os.Args[2], model: os.Args[3], card: os.Args[4], root: os.Args[5], tokens: os.Args[6]}
		r.job = filepath.Join(r.root, r.slot, "jobs", r.label)
	}
	r.line1, r.line2 = cardLines(r.card)
	os.Exit(r.run(load()))
}

// nativeRunner reads the arguments a runnerless batch hands the self when it runs the card
// through its own native verb (issue #636):
//
//	native --harness <h> --model <m> --label <l> --card <c> --slot <slotdir> --root <r> --deadline <d> --slots-store <s> --owner <o> [--auth <a>]
//
// `--slot` is the SLOT DIRECTORY, unlike a `--runner` invocation's slot number, so the job
// sits directly under it. Parsing the flags, rather than running with empty fields, keeps a
// native fixture's expansion and guards the same as a runner fixture's.
func nativeRunner(args []string) (*runner, error) {
	fs := flag.NewFlagSet("native", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	_ = fs.String("harness", "", "the harness binary")
	model := fs.String("model", "", "the card's model")
	label := fs.String("label", "", "the card's label")
	card := fs.String("card", "", "the card file")
	slotDir := fs.String("slot", "", "the slot directory")
	root := fs.String("root", "", "the batch root")
	_ = fs.String("deadline", "", "the card's deadline")
	_ = fs.String("auth", "", "the auth profile")
	// The bench slot lease (nova-tools#1546) travels with every native launch, so the
	// fixture standing in for nova-swarm must accept it or a runnerless batch cannot run
	// here at all. The fixture takes no lease: it is not nova-swarm, and a test that let it
	// pretend to would be testing the fixture.
	_ = fs.String("slots-store", "", "the bench slot store")
	_ = fs.String("owner", "", "whose share the lease counts against")
	// The budget word (rule 13d) travels with every native launch, so the fixture standing
	// in for nova-swarm must accept it or a runnerless batch cannot run here at all.
	tokens := fs.String("tokens", "", "the card's token budget, or `unmetered`")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	r := &runner{label: *label, slot: filepath.Base(*slotDir), model: *model, card: *card, root: *root, tokens: *tokens}
	r.job = filepath.Join(*slotDir, "jobs", *label)
	return r, nil
}

type runner struct {
	label, slot, model, card, root string
	// tokens is the budget word the batch hands a runner as its sixth argument, and `native`
	// as --tokens (SPEC-SWARM rule 13d). The fixture only carries it: deciding what a budget
	// word means is `native`'s, and a fixture that decided would be testing itself.
	tokens       string
	job          string
	line1, line2 string
}

// load reads the step list written beside this executable. A fixture with no file behind it
// is a test bug worth failing loudly for: the card would otherwise pass by doing nothing.
func load() spec {
	exe, err := os.Executable()
	if err != nil {
		exe = os.Args[0]
	}
	raw, err := os.ReadFile(exe + ".json")
	if err != nil {
		fmt.Fprintf(os.Stderr, "fakerunner: reading the behaviour beside %s: %v\n", exe, err)
		os.Exit(2)
	}
	var s spec
	if err := json.Unmarshal(raw, &s); err != nil {
		fmt.Fprintf(os.Stderr, "fakerunner: the behaviour beside %s does not parse: %v\n", exe, err)
		os.Exit(2)
	}
	return s
}

// cardLines returns the first two lines of the card, the two the RESULT contract is made of:
// line 1 is the contract line and line 2 the disposition. A card that cannot be read leaves
// both empty, which is what `sed -n 1p` did.
func cardLines(path string) (string, string) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", ""
	}
	lines := strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n")
	var l1, l2 string
	if len(lines) > 0 {
		l1 = lines[0]
	}
	if len(lines) > 1 {
		l2 = lines[1]
	}
	return l1, l2
}

func (r *runner) run(s spec) int {
	for _, st := range s.Steps {
		if !r.guardHolds(st.When) {
			continue
		}
		switch st.Op {
		case "mkdir":
			must(os.MkdirAll(r.expand(st.Path), 0o755))
		case "touch":
			p := r.expand(st.Path)
			must(os.MkdirAll(filepath.Dir(p), 0o755))
			f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY, 0o644)
			must(err)
			must(f.Close())
		case "write":
			r.writeFile(r.expand(st.Path), r.expand(st.Body), false)
		case "append":
			r.writeFile(r.expand(st.Path), r.expand(st.Body), true)
		case "appendn":
			// N lines, {i} counting from 0, Ms apart: the shape of a card whose log keeps
			// growing (or stops growing after a few lines).
			p := r.expand(st.Path)
			for i := 0; i < st.N; i++ {
				r.writeFile(p, strings.ReplaceAll(r.expand(st.Body), "{i}", strconv.Itoa(i)), true)
				sleep(st.Ms)
			}
		case "copy":
			// Body is the SOURCE, Path the destination, matching `cp "$src" "$dst"`.
			raw, err := os.ReadFile(r.expand(st.Body))
			must(err)
			p := r.expand(st.Path)
			must(os.MkdirAll(filepath.Dir(p), 0o755))
			must(os.WriteFile(p, raw, 0o644))
		case "record":
			// The runner's WHOLE argv, one element per line, is what a fixture checks when the
			// question is which command the batch ran and with what flags -- the self's `native`
			// verb, say, or a runner's five arguments. Path is where it lands, expanded like any
			// other step's; the argv itself is written verbatim.
			p := r.expand(st.Path)
			must(os.MkdirAll(filepath.Dir(p), 0o755))
			must(os.WriteFile(p, []byte(strings.Join(os.Args, "\n")+"\n"), 0o644))
		case "stdout":
			// The batch pins the runner's stdout to the job's harness.log. N defaults to one.
			n := st.N
			if n <= 0 {
				n = 1
			}
			for i := 0; i < n; i++ {
				fmt.Println(strings.ReplaceAll(r.expand(st.Body), "{i}", strconv.Itoa(i)))
				sleep(st.Ms)
			}
		case "sleep":
			sleep(st.Ms)
		case "spin":
			r.spin(st.N, st.Ms)
		case "exit":
			return st.N
		default:
			fmt.Fprintf(os.Stderr, "fakerunner: no such step %q\n", st.Op)
			return 2
		}
	}
	return 0
}

// spin starts a CPU burner as a grandchild of the card, waits Ms, then kills it. burnMs is
// the burner's OWN deadline, so a red run of the test that drives this leaves no process
// behind: it ends on its own whatever happens to its parent.
func (r *runner) spin(burnMs, ms int) {
	exe, err := os.Executable()
	must(err)
	cmd := exec.Command(exe, spinArg, strconv.Itoa(burnMs))
	must(cmd.Start())
	sleep(ms)
	_ = cmd.Process.Kill()
	_, _ = cmd.Process.Wait()
}

// burn holds a core for d without writing one byte: the state the idle monitor must read as
// "working", from the process tree's CPU time alone.
func burn(d time.Duration) {
	end := time.Now().Add(d)
	x := 0
	for time.Now().Before(end) {
		for i := 0; i < 2_000_000; i++ {
			x += i
		}
	}
	_ = x
}

func (r *runner) writeFile(path, body string, appendTo bool) {
	must(os.MkdirAll(filepath.Dir(path), 0o755))
	flags := os.O_CREATE | os.O_WRONLY | os.O_TRUNC
	if appendTo {
		flags = os.O_CREATE | os.O_WRONLY | os.O_APPEND
	}
	if body != "" && !strings.HasSuffix(body, "\n") {
		body += "\n"
	}
	if !appendTo {
		// A whole-file write is published atomically: written beside the target and renamed
		// into place. A test that waits for the file to EXIST and then fires the deadline must
		// never be able to catch it created and still empty; that window turned
		// result-after-deadline into line1-mismatch on the Studio (2026-09-17) once the
		// runner started fast enough for the race to show.
		tmp, err := os.CreateTemp(filepath.Dir(path), ".fakerunner-*")
		must(err)
		_, err = tmp.WriteString(body)
		must(err)
		must(tmp.Chmod(0o644))
		must(tmp.Close())
		must(os.Rename(tmp.Name(), path))
		return
	}
	f, err := os.OpenFile(path, flags, 0o644)
	must(err)
	_, err = f.WriteString(body)
	must(err)
	must(f.Close())
}

// guardHolds reads the tiny condition language the shell scripts' `if` and `case` became:
// `label==a`, `label!=d`, `line2==MISSING`, `line2!=MISSING`. An empty guard always holds.
func (r *runner) guardHolds(when string) bool {
	if when == "" {
		return true
	}
	neg := false
	var name, want string
	switch {
	case strings.Contains(when, "!="):
		neg = true
		name, want = split2(when, "!=")
	case strings.Contains(when, "=="):
		name, want = split2(when, "==")
	default:
		fmt.Fprintf(os.Stderr, "fakerunner: no such guard %q\n", when)
		os.Exit(2)
	}
	var got string
	switch name {
	case "label":
		got = r.label
	case "line1":
		got = r.line1
	case "line2":
		got = r.line2
	case "slot":
		got = r.slot
	case "model":
		got = r.model
	default:
		fmt.Fprintf(os.Stderr, "fakerunner: no such guard field %q\n", name)
		os.Exit(2)
	}
	return (got == want) != neg
}

func split2(s, sep string) (string, string) {
	i := strings.Index(s, sep)
	return strings.TrimSpace(s[:i]), strings.TrimSpace(s[i+len(sep):])
}

// expand substitutes the card's own facts into a path or a body, the way the scripts read
// `"$1"`, `"$5"` and `$NOVA_SWARM_ROOT`.
func (r *runner) expand(s string) string {
	if s == "" {
		return s
	}
	rep := strings.NewReplacer(
		"{label}", r.label,
		"{slot}", r.slot,
		"{model}", r.model,
		"{card}", r.card,
		"{root}", r.root,
		"{job}", r.job,
		"{line1}", r.line1,
		"{line2}", r.line2,
		"{arg5}", r.root, // what the runner was HANDED as its root, spelled as it was handed
		"{env:NOVA_SWARM_ROOT}", os.Getenv("NOVA_SWARM_ROOT"),
		"{env:NOVA_SWARM_JOB}", os.Getenv("NOVA_SWARM_JOB"),
	)
	return rep.Replace(s)
}

func sleep(ms int) {
	if ms > 0 {
		time.Sleep(time.Duration(ms) * time.Millisecond)
	}
}

func must(err error) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "fakerunner: %v\n", err)
		os.Exit(2)
	}
}
