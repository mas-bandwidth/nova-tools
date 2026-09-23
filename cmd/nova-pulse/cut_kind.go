package main

// `cut --kind` — the typed cutter, and the only place a card number comes from. Without
// --kind, `cut` is the pool-driven cutter of internal/pulse/cut.go and nothing here runs.
//
// There is no --number flag, and passing one is a refusal that names why: the number comes
// only from the queue's state file, under the queue's lock (issue #828, class B).

import (
	"fmt"
	"io"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/pulse"
)

// hasKindFlag says whether this invocation is the typed cutter's.
func hasKindFlag(args []string) bool {
	for _, a := range args {
		if a == "--kind" || a == "-kind" || strings.HasPrefix(a, "--kind=") || strings.HasPrefix(a, "-kind=") {
			return true
		}
	}
	return false
}

func cmdCutKind(args []string, stdout, stderr io.Writer) int {
	f := newFlags("cut")
	for _, a := range args {
		if a == "--number" || a == "-number" || strings.HasPrefix(a, "--number=") || strings.HasPrefix(a, "-number=") {
			return refuse(stderr, " cut", "--number is not a flag; a card number comes only from the queue state file's next_card, under the queue's lock, so two cutters never share one")
		}
	}
	kind := f.fs.String("kind", "", "")
	repo := f.fs.String("repo", "", "")
	pr := f.fs.Int("pr", 0, "")
	head := f.fs.String("head", "", "")
	issue := f.fs.Int("issue", 0, "")
	title := f.fs.String("title", "", "")
	bodyFile := f.fs.String("body-file", "", "")
	prior := f.fs.String("prior", "", "")
	names := f.fs.String("names", "", "")
	specLines := f.fs.String("spec-lines", "", "")
	out := f.fs.String("out", "", "")
	queue := f.fs.String("queue", "", "")
	from := f.fs.String("from", "", "")
	if !f.parse(args, stderr) {
		return 2
	}
	// --from <table> is the typed cut's small loop (#2021): one row per line of a
	// kind-specific table, deduped against cards already in <queue>/{pending,launched,
	// done,failed}, cutted one at a time. A row's words are the same word the kind's own
	// single-row cut takes; nothing here invents a new shape.
	if strings.TrimSpace(*from) != "" {
		if strings.TrimSpace(*kind) == "" {
			fmt.Fprintf(stderr, "CUT FROM REFUSED: --from wants --kind (the kind fixes the row shape: fix|read|guard)\n")
			return 2
		}
		return cutKindFrom(*kind, *from, *out, *queue, stdout, stderr)
	}
	return pulse.CutKind(pulse.CutKindInput{
		Kind: *kind, Repo: *repo, PR: *pr, Head: *head, Issue: *issue, Title: *title,
		BodyFile: *bodyFile, Prior: *prior, Names: *names, SpecLines: *specLines,
		Out: *out, Queue: *queue, Stdout: stdout, Stderr: stderr,
	})
}
