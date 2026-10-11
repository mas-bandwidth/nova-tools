package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/pkg/cardtree"
	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
	"github.com/mas-bandwidth/nova-tools/pkg/subproc"
	"github.com/mas-bandwidth/nova-tools/pkg/swarm"
)

// cmdStep is the script-step executor (docs/SPEC-SPRINT.md, a card is a tree of steps;
// pkg/cardtree): it walks a card whose every work step is a script step, in a checkout,
// with no model: each program, its POST lines and its commit, each command in the step's own
// wall (the network denied, no credential, the checkout and a private temp the only writes),
// and it stops at the first step that is not ok. native runs it in place of the harness.
// Without a wall binary it refuses unless --no-wall is given, and then says the programs run
// unconfined. `--remainder <id> --from <n> --land <sha>` prints instead the card a failed step
// n leaves.
func cmdStep(args []string, stdout, stderr io.Writer) int {
	f := newFlags("step")
	card := f.fs.String("card", "", "required: the tree card's `file`")
	dir := f.fs.String("dir", "", "the `checkout` the steps run in (cwd of every program and POST command); required unless --remainder")
	work := f.fs.String("work", "", "the `directory` the programs are built in and the steps' private temp made under (default: a new one under TMPDIR)")
	result := f.fs.String("result", "", "the RESULT.md `file` to write in the result shape, the step lines in its body")
	sandbox := f.fs.String("sandbox", "", "the wall `binary` each program, POST command and git runs in (default: nova-sandbox on PATH)")
	noWall := f.fs.Bool("no-wall", false, "run the card's programs with no wall, with this process's own powers: network, environment and every file it can write")
	remainder := f.fs.String("remainder", "", "print the remainder card of this card `id` from --from, staged at --land, and run nothing")
	from := f.fs.String("from", "", "with --remainder: the failed `step` the remainder starts at")
	land := f.fs.String("land", "", "with --remainder: the full `sha` steps 1..n-1 landed at (the finish's pushed=)")
	dryRun := f.fs.Bool("dry-run", false, "print each step the walk would run (its language, paths and POST lines) and the wall its commands would run in, and run and write nothing")
	if !f.parse(args, stderr) {
		return 2
	}
	f.want(*card, "card", "the tree card's file")
	if *remainder != "" {
		f.want(*from, "from", "the step number the remainder starts at")
		f.want(*land, "land", "the full sha the steps before it landed at")
	} else {
		f.want(*dir, "dir", "the checkout the steps run in")
	}
	if f.refused(stderr) {
		return 2
	}
	raw, err := os.ReadFile(*card)
	if err != nil {
		return refuse(stderr, "step", "--card wants a readable card file: "+err.Error())
	}
	t := cardtree.Parse(string(raw))
	if *remainder != "" {
		if _, ok := t.Step(*from); !ok {
			return refuse(stderr, "step", fmt.Sprintf("--from %s names no step of the card", oneline.Field(*from)))
		}
		rem, err := cardtree.Remainder(string(raw), *remainder, *from, *land)
		if err != nil {
			return refuse(stderr, "step", "--land: "+err.Error())
		}
		fmt.Fprint(stdout, rem)
		return 0
	}
	if fs := cardtree.Lint(string(raw)); len(fs) > 0 {
		return refuse(stderr, "step", fmt.Sprintf("the card's tree has %d finding(s), the first %s: %d: %s; run: nova-swarm lint --card %s", len(fs), oneline.Field(fs[0].Check), fs[0].Line, oneline.Escape(fs[0].Excerpt), oneline.Field(*card)))
	}
	if !t.AllScript() {
		return refuse(stderr, "step", "the card has no script step, or has model steps too; the executor runs a card whose every work step is a script step")
	}
	wall, why := stepWall(*sandbox, *noWall)
	if why != "" {
		return refuse(stderr, "step", why)
	}
	if *dryRun {
		bin := wall.Bin
		if bin == "" {
			bin = "none"
		}
		for _, s := range t.Work() {
			fmt.Fprintf(stdout, "STEP PLAN step=%s lang=%s paths=%s posts=%d wall=%s\n", oneline.Field(s.Num), oneline.Field(s.Lang), oneline.Field(strings.Join(s.Paths, ",")), len(s.Post), oneline.Field(bin))
		}
		return 0
	}
	if *work == "" {
		if *work, err = os.MkdirTemp("", "nova-step-"); err != nil {
			return refuse(stderr, "step", "no directory for the programs: "+err.Error())
		}
	}
	if *work, err = filepath.Abs(*work); err != nil {
		return refuse(stderr, "step", "--work: "+err.Error())
	}
	bin, tmp := filepath.Join(*work, "bin"), filepath.Join(*work, "tmp")
	for _, d := range []string{bin, tmp} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return refuse(stderr, "step", "--work: "+err.Error())
		}
	}
	wall.Tmp = tmp
	if wall.Bin == "" {
		fmt.Fprintln(stdout, "STEP NOTE no wall (--no-wall): the card's programs run unconfined, with this process's network, environment and files")
	} else {
		wall.Read = stepReads(*dir, bin, benchPasswdHome())
	}
	done := cardtree.Walk(t, func(s cardtree.Step) cardtree.Result {
		r := cardtree.RunScript(*dir, s, cardtree.OSSys(bin, wall))
		word := "OK"
		if r.Verdict != cardtree.OK {
			word = "FAILED"
		}
		fmt.Fprintf(stdout, "STEP %s %s\n", oneline.Field(word), oneline.Escape(r.Line()))
		return r
	})
	if *result != "" {
		if err := os.WriteFile(*result, []byte(stepResult(*dir, t, done)), 0o644); err != nil {
			return refuse(stderr, "step", "--result could not be written: "+err.Error())
		}
	}
	if done[len(done)-1].Verdict != cardtree.OK {
		return 1
	}
	return 0
}

// stepWall is the wall binary a step runs in: --sandbox, else nova-sandbox on PATH; none only
// when --no-wall says so, and a refusal when there is no wall and no --no-wall.
func stepWall(sandbox string, noWall bool) (cardtree.Wall, string) {
	switch {
	case noWall && sandbox != "":
		return cardtree.Wall{}, "--no-wall and --sandbox name two different walls; give one"
	case noWall:
		return cardtree.Wall{}, ""
	case sandbox != "":
		// absolute, since every command runs from the checkout
		abs, err := filepath.Abs(sandbox)
		if err != nil {
			return cardtree.Wall{}, "--sandbox: " + err.Error()
		}
		return cardtree.Wall{Bin: abs}, ""
	}
	found, err := exec.LookPath(swarm.SandboxBinary)
	if err != nil {
		return cardtree.Wall{}, "no wall: " + swarm.SandboxBinary + " is on no PATH entry; a script step's program runs only in its own wall: name it with --sandbox <path>, or give --no-wall to run it unconfined"
	}
	abs, err := filepath.Abs(found)
	if err != nil {
		return cardtree.Wall{}, "the wall on PATH: " + err.Error()
	}
	return cardtree.Wall{Bin: abs}, ""
}

// benchPasswdHome is the bench user's home from the password database, never $HOME.
// A script step's process has HOME set to the slot's data home (native.go, the step
// needs no model and no credential). os.UserHomeDir follows that variable, so a
// toolchain lookup under it finds no sdk and the step's wall never grants the bench
// go the shim execs. The password entry is the home the toolchain was installed under.
func benchPasswdHome() string {
	u, err := user.Current()
	if err != nil {
		return ""
	}
	return u.HomeDir
}

// stepReads is what a step's wall lets it read beside the system: the built programs, the
// bench's toolchain (Go, sbcl) and /opt/homebrew where git and sbcl live, and, never
// executable, the module cache GOMODCACHE names and the objects the checkout borrows (its
// alternates). home is the bench user's password-database home, never this process's HOME:
// a script step sets HOME to the slot, and a lookup there skips the bench sdk.
func stepReads(dir, bin, home string) []string {
	out := []string{"--read", bin}
	if fi, err := os.Stat("/opt/homebrew"); err == nil && fi.IsDir() {
		out = append(out, "--read", "/opt/homebrew")
	}
	if git, err := exec.LookPath("git"); err == nil {
		if real, err := filepath.EvalSymlinks(git); err == nil {
			out = append(out, "--read", filepath.Dir(real))
		}
	}
	for _, r := range swarm.ToolchainRoots(runtime.GOOS, home) {
		flag := "--read-noexec"
		if r.Exec {
			flag = "--read"
		}
		out = append(out, flag, r.Path)
	}
	// the module cache the caller names (native names the bench's shared one): read, never
	// written or run, so a POST's go vet or go test finds its modules and fetches none
	if mod := os.Getenv("GOMODCACHE"); filepath.IsAbs(mod) && !named(out, mod) {
		if fi, err := os.Stat(mod); err == nil && fi.IsDir() {
			out = append(out, "--read-noexec", mod)
		}
	}
	if b, err := os.ReadFile(filepath.Join(dir, ".git", "objects", "info", "alternates")); err == nil {
		for _, l := range strings.Split(string(b), "\n") {
			if l = strings.TrimSpace(l); filepath.IsAbs(l) {
				out = append(out, "--read-noexec", l)
			}
		}
	}
	return out
}

// stepResult is an all-script card's RESULT.md in the result shape (docs/SPEC-CARD-CONTRACT.md
// section 3): the checkout's head and branch, ok when every step is, the step lines in the
// body, which the member reads as any child's (treeFinish).
func stepResult(dir string, t cardtree.Tree, done []cardtree.Result) string {
	verdict, report := "ok", "script steps: "+strconv.Itoa(len(done))+" of "+strconv.Itoa(len(t.Work()))+" ok, run by the member with no model"
	if last := done[len(done)-1]; last.Verdict != cardtree.OK {
		verdict, report = "not-done", "step "+last.Num+" "+last.Verdict+": "+last.Words
	}
	var b strings.Builder
	fmt.Fprintf(&b, "head: %s\nbranch: %s\nverdict: %s\ngate: -\noutput: -\nreport: %s\ntitle: %s\n\n## Body\n\n",
		oneline.Escape(gitOut(dir, "rev-parse", "HEAD")), oneline.Escape(gitOut(dir, "branch", "--show-current")), oneline.Escape(verdict),
		oneline.Escape(oneline.Cap(report, 300)), oneline.Escape(oneline.Cap(report, 120)))
	for _, r := range done {
		fmt.Fprintf(&b, "%s\n", oneline.Escape(r.Line()))
	}
	return b.String()
}

// gitOut is one git line of the checkout, "-" when git says nothing.
func gitOut(dir string, args ...string) string {
	cmd, cancel := subproc.Command(context.Background(), subproc.Git, "git", args...)
	defer cancel()
	cmd.Dir = dir
	out, err := cmd.Output()
	if s := strings.TrimSpace(string(out)); err == nil && s != "" {
		return s
	}
	return "-"
}

// treeCardName is the slot's copy of a tree card the executor reads.
const treeCardName = "tree-card.md"

// installTreeSteps readies a card whose every work step is a script step (docs/SPEC-SPRINT.md,
// a card is a tree of steps): the card copied into the slot, and the executor's argv native
// runs in place of the harness, outside the child's wall (a wall does not nest), each of its
// commands in the step's own wall; nothing for any other card.
func installTreeSteps(card []byte, slotDir, jobDir string) ([]string, error) {
	if !cardtree.Parse(string(card)).AllScript() {
		return nil, nil
	}
	self, err := os.Executable()
	if err != nil {
		return nil, err
	}
	path := filepath.Join(slotDir, treeCardName)
	if err := os.WriteFile(path, card, 0o644); err != nil {
		return nil, err
	}
	return []string{self, "step", "--card", path, "--dir", filepath.Join(jobDir, swarm.JobRepo), "--result", filepath.Join(jobDir, "RESULT.md")}, nil
}

// named says the wall's read flags already name dir.
func named(flags []string, dir string) bool {
	for _, f := range flags {
		if f == dir {
			return true
		}
	}
	return false
}

// nativeStepLaunch is a script card's executor in the child's place (docs/SPEC-SPRINT.md, a
// card is a tree of steps): its path and argv, handed the run's wall as an absolute path (the
// executor runs from the job directory) or --no-wall when the run has none, never wrapped in
// the child's wall; and whether native lowers its priority, decided from the run's wall as
// for any child (nativeNicesChild): the step's own darwin wall forbids setpriority too, so the
// executor and every command it starts run at the nice a walled child runs at.
func nativeStepLaunch(goos string, stepArgv []string, wall string) (path string, argv []string, niced bool, err error) {
	flags := []string{"--no-wall"}
	if wall != "" {
		abs, err := filepath.Abs(wall)
		if err != nil {
			return "", nil, false, err
		}
		flags = []string{"--sandbox", abs}
	}
	return stepArgv[0], append(append([]string{}, stepArgv[1:]...), flags...), nativeNicesChild(goos, wall != ""), nil
}
