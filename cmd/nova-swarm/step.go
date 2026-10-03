package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/cardtree"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/subproc"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// cmdStep is the script-step executor (docs/SPEC-SPRINT.md, a card is a tree of steps;
// internal/cardtree): it runs a tree card's script steps in a checkout with no model, checks
// each step's POST lines and commits it, and prints the step's verdict line. A mixed card's
// child runs it through the `nova-step` shim at each script step; an all-script card is run
// by native with `all` in place of the harness, and `--result` writes the card's RESULT.md.
// `--remainder <id> --from <n>` prints instead the card a failed step n leaves.
func cmdStep(args []string, stdout, stderr io.Writer) int {
	f := newFlags("step")
	card := f.fs.String("card", "", "required: the tree card's `file`")
	dir := f.fs.String("dir", "", "the `checkout` the steps run in (cwd of every program and POST command); required unless --remainder")
	work := f.fs.String("work", "", "the `directory` the programs are written into (default: a new one under TMPDIR)")
	result := f.fs.String("result", "", "with all: the RESULT.md `file` to write in the result shape, the step lines in its body")
	remainder := f.fs.String("remainder", "", "print the remainder card of this card `id` from --from, and run nothing")
	from := f.fs.String("from", "", "with --remainder: the failed `step` the remainder starts at")
	which := f.fs.String("steps", "", "the script `steps` to run, comma separated (3.1,3.2), or all for a card whose every work step is a script step; required unless --remainder")
	if !f.parse(args, stderr) {
		return 2
	}
	f.want(*card, "card", "the tree card's file")
	if *remainder != "" {
		f.want(*from, "from", "the step number the remainder starts at")
	} else {
		f.want(*dir, "dir", "the checkout the steps run in")
		f.want(*which, "steps", "the script steps to run, comma separated, or all")
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
		fmt.Fprint(stdout, cardtree.Remainder(string(raw), *remainder, *from))
		return 0
	}
	if fs := cardtree.Lint(string(raw)); len(fs) > 0 {
		return refuse(stderr, "step", fmt.Sprintf("the card's tree has %d finding(s), the first %s: %d: %s; run: nova-swarm lint --card %s", len(fs), oneline.Field(fs[0].Check), fs[0].Line, oneline.Escape(fs[0].Excerpt), oneline.Field(*card)))
	}
	if *work == "" {
		if *work, err = os.MkdirTemp("", "nova-step-"); err != nil {
			return refuse(stderr, "step", "no directory for the programs: "+err.Error())
		}
	}
	sys := cardtree.OSSys(*work)
	var todo []cardtree.Step
	steps := strings.Split(*which, ",")
	if *which == "all" {
		if !t.AllScript() {
			return refuse(stderr, "step", "all runs a card whose every work step is a script step; this card has model steps: run each script step by its number")
		}
		todo = t.Work()
	} else {
		for _, n := range steps {
			s, ok := t.Step(n)
			if !ok || !s.Script() {
				return refuse(stderr, "step", fmt.Sprintf("step %s is no script step of the card (a work step with SCRIPT:)", oneline.Field(n)))
			}
			todo = append(todo, s)
		}
	}
	var done []cardtree.Result
	for _, s := range todo {
		r := cardtree.RunScript(*dir, s, sys)
		done = append(done, r)
		word := "OK"
		if r.Verdict != cardtree.OK {
			word = "FAILED"
		}
		fmt.Fprintf(stdout, "STEP %s %s\n", oneline.Field(word), oneline.Escape(r.Line()))
		if r.Verdict != cardtree.OK {
			break
		}
	}
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

// stepShim is the `nova-step` command a mixed tree card's child runs at a script step: the
// executor, on the slot's copy of the card (read-only inside the wall), in the staged checkout.
func stepShim(self, card, repo string) string {
	q := func(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
	return "#!/bin/sh\n# nova-swarm step shim: a tree card's script step is the machine's (docs/SPEC-SPRINT.md, a card is a tree of steps)\n" +
		"IFS=,\nexec " + q(self) + " step --card " + q(card) + " --dir " + q(repo) + " --steps \"$*\"\n"
}

// treeCardName is the slot's copy of a tree card the executor reads.
const treeCardName = "tree-card.md"

// installTreeSteps readies a tree card with script steps (docs/SPEC-SPRINT.md, a card is a
// tree of steps): the card copied into the slot, which the wall reads and the child cannot
// write, and the `nova-step` shim beside the profile's shims. It returns the executor's
// binary (the wall reads its directory) and, for an all-script card, the argv native runs in
// place of the harness; nothing for a card with no script step.
func installTreeSteps(card []byte, slotDir, jobDir, shimDir string) (self string, all []string, err error) {
	t := cardtree.Parse(string(card))
	if !t.HasScript() {
		return "", nil, nil
	}
	if self, err = os.Executable(); err != nil {
		return "", nil, err
	}
	path := filepath.Join(slotDir, treeCardName)
	repo := filepath.Join(jobDir, swarm.JobRepo)
	if err := os.WriteFile(path, card, 0o644); err != nil {
		return "", nil, err
	}
	if err := os.MkdirAll(shimDir, 0o755); err != nil {
		return "", nil, err
	}
	if err := os.WriteFile(filepath.Join(shimDir, "nova-step"), []byte(stepShim(self, path, repo)), 0o755); err != nil {
		return "", nil, err
	}
	if t.AllScript() {
		all = []string{self, "step", "--card", path, "--dir", repo, "--result", filepath.Join(jobDir, "RESULT.md"), "--steps", "all"}
	}
	return self, all, nil
}
