package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// fsck: a read-only verb that checks store-vs-git invariants (docs/SPEC-SPRINT.md section 7,
// fsck-verb-landed-on-base-b.w2): fsck prints one FSCK VIOLATION line for each check that fails,
// a summary FSCK OK or FSCK FAIL, and exits 0/1/2.

type fsckResult struct {
	Check    string `json:"check"`
	ID       string `json:"id,omitempty"`
	Stream   string `json:"stream,omitempty"`
	Head     string `json:"head,omitempty"`
	Repo     string `json:"repo,omitempty"`
	Base     string `json:"base,omitempty"`
	Tip      string `json:"tip,omitempty"`
	Ancestor bool   `json:"ancestor,omitempty"`
	Why      string `json:"why,omitempty"`
	Fix      string `json:"fix,omitempty"`
}

func (a *app) cmdFsck(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("fsck")
	var checks []string
	fs.Func("check", "filter to named checks only (repeatable)", func(s string) error {
		checks = append(checks, sprint.Split(s)...)
		return nil
	})
	pos, err := parse(fs, args)
	if err != nil || len(pos) > 0 {
		return refuse(stderr, "fsck", argErr("takes no words; run: fsck [--json] [--check <name>]...", err, pos...))
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, "fsck", err.Error())
	}
	ctx := context.Background()
	snap, err := st.Load(ctx, []string{sprint.Work}, nil)
	if err != nil {
		return a.readFailed("fsck", err, stderr)
	}
	runGit := a.gitRunner(*c)
	results := sprint.LandOnBase(snap, checks, runGit)
	if c.json {
		var items []fsckResult
		for _, f := range results {
			items = append(items, fsckResult{
				Check: f.Check, ID: f.ID, Stream: f.Stream,
				Head: f.Head, Repo: f.Repo, Base: f.Base, Tip: f.Tip,
				Ancestor: f.Ancestor, Why: f.Why, Fix: f.Fix,
			})
		}
		b, _ := json.Marshal(map[string]any{
			"verb":   "fsck",
			"status": statusFromResults(results),
			"exit":   exitFromResults(results),
			"checks": items,
		})
		fmt.Fprintln(stdout, string(b))
		return exitFromResults(results)
	}
	violations := 0
	unreadable := false
	for _, f := range results {
		if f.Why != "" {
			unreadable = true
		} else if !f.Ancestor {
			violations++
		}
		fmt.Fprintln(stdout, f.Line())
	}
	if unreadable {
		return 2
	}
	if violations == 0 {
		fmt.Fprintln(stdout, "FSCK OK checks=1")
		return 0
	}
	fmt.Fprintf(stdout, "FSCK FAIL violations=%d\n", violations)
	return 1
}

func statusFromResults(results []sprint.LandOnBaseFinding) string {
	for _, f := range results {
		if !f.Ancestor {
			return "violation"
		}
	}
	return "ok"
}

func exitFromResults(results []sprint.LandOnBaseFinding) int {
	for _, f := range results {
		if !f.Ancestor {
			return 1
		}
	}
	return 0
}

// gitRunner returns a function that runs git commands with the verb's environment.
func (a *app) gitRunner(c common) func(cmd string, args ...string) (int, string, string) {
	return func(cmd string, args ...string) (int, string, string) {
		out, err := exec.Command(cmd, args...).CombinedOutput()
		if err != nil {
			if exit, ok := err.(*exec.ExitError); ok {
				return exit.ExitCode(), string(out), ""
			}
			return 2, "", err.Error()
		}
		return 0, string(out), ""
	}
}
