package main

// `cut --kind` — the typed cutter, and the only place a card number comes from. Without
// --kind, `cut` is the pool-driven cutter of internal/pulse/cut.go and nothing here runs.
//
// There is no --number flag, and passing one is a refusal that names why: the number comes
// only from the queue's state file, under the queue's lock (issue #828, class B).

import (
	"io"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/buildinfo"
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
	base := f.fs.String("base", "", "")
	issue := f.fs.Int("issue", 0, "")
	title := f.fs.String("title", "", "")
	bodyFile := f.fs.String("body-file", "", "")
	prior := f.fs.String("prior", "", "")
	priorCard := f.fs.String("prior-card", "", "")
	names := f.fs.String("names", "", "")
	specLines := f.fs.String("spec-lines", "", "")
	out := f.fs.String("out", "", "")
	queue := f.fs.String("queue", "", "")
	if !f.parse(args, stderr) {
		return 2
	}
	// The route a card takes is the queue's configuration, never a template's memory: the
	// cutter rewrites a MODEL: line to routes.rewrite_model_to (class M, #828). A queue
	// whose configuration cannot be read is a refusal here, not a card on an unknown route.
	rewrite := ""
	if *queue != "" {
		cfg, err := pulse.LoadConfig(*queue, io.Discard, 0)
		if err != nil {
			return refuse(stderr, " cut", err.Error())
		}
		rewrite = cfg.Routes.RewriteModelTo
	}
	return pulse.CutKind(pulse.CutKindInput{
		Kind: *kind, Repo: *repo, PR: *pr, Head: *head, Base: *base, Issue: *issue, Title: *title,
		BodyFile: *bodyFile, Prior: *prior, PriorCard: *priorCard, Names: *names, SpecLines: *specLines,
		Out: *out, Queue: *queue, Version: buildinfo.Version(version), RewriteModelTo: rewrite,
		Stdout: stdout, Stderr: stderr,
	})
}
