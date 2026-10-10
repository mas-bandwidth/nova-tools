package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/pkg/cardhdr"
	"github.com/mas-bandwidth/nova-tools/pkg/gitrun"
	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
	"github.com/mas-bandwidth/nova-tools/pkg/swarm"
)

// holdPathsAdmit holds each brief that names PATHS, REPO and BASE to the admission check
// (sprint.PathsAdmission; docs/SPEC-CARD-CONTRACT.md, what admission verifies). The tree
// is the lander's clone of the repository at the BASE tip, the same fetch add's brief
// checks use (briefBaseAt, refs/nova-add/<base>), cached per tip for the call. A miss
// prints as one LINT DRIFT line. Any miss refuses the call, exit 2, nothing written.
// A brief that omits REPO or BASE is not read. A named REPO that cannot be resolved is
// MISSING, the same as a tip that cannot be fetched, and is not a pass.
func (a *app) holdPathsAdmit(verbName string, stderr io.Writer, briefs ...briefCheck) int {
	type key struct{ repo, ref, pin string }
	trees := map[key]sprint.AdmissionTree{}
	ctx := context.Background()
	l := &lander{a: a}
	var lines []string
	first := ""
	for _, b := range briefs {
		if _, ok := swarm.CardHeaderValue([]byte(b.brief), "PATHS"); !ok {
			continue
		}
		cb := swarm.ReadCardBase([]byte(b.brief))
		// An omitted REPO or BASE has no tree to read. A named REPO that cannot be
		// resolved is not omitted: admissionTree keeps that as MISSING, and the brief
		// is refused. Skipping it would admit a brief with no tree evidence.
		if cb.Ref == "" || cb.Named == "" {
			continue
		}
		k := key{cb.Named, cb.Ref, cb.Sha}
		tree, ok := trees[k]
		if !ok {
			tree = a.admissionTree(ctx, l, cb)
			trees[k] = tree
		}
		id := b.id
		if id == "" {
			id = "-"
		}
		for _, miss := range sprint.PathsAdmission(b.brief, tree) {
			if first == "" {
				first = miss
			}
			lines = append(lines, fmt.Sprintf("LINT DRIFT card=%s check=paths-hold-named line=%d: %s remedy=%s",
				oneline.Field(id), pathsHeaderLine(b.brief),
				oneline.Escape(oneline.Cap(miss, oneline.TailBytes)),
				oneline.Escape("name on PATHS the nearest file this line names, the one that holds the identifier, and add the brief again")))
		}
	}
	if len(lines) == 0 {
		return 0
	}
	for _, x := range lines {
		fmt.Fprintln(stderr, x)
	}
	return refuse(stderr, verbName, fmt.Sprintf("%d PATHS admission finding(s) at the base, the first %s; nothing was written", len(lines), oneline.Cap(first, 300)))
}

// admissionTree is the BASE tip in the lander's clone: the file list, and a plain grep
// cached per identifier. A tip that cannot be had is Missing, and the check does not
// invent a path miss from an empty list.
func (a *app) admissionTree(ctx context.Context, l *lander, cb swarm.CardBase) sprint.AdmissionTree {
	bb := a.briefBaseAt(ctx, l, cb)
	at := cb.Ref
	if len(bb.Sha) >= 12 {
		at = bb.Sha[:12] + " (" + cb.Ref + ")"
	}
	if bb.Missing != "" || bb.Repo == "" || len(bb.Sha) < 40 {
		why := bb.Missing
		if why == "" {
			why = "no repository at the BASE tip was handed over, so the brief was not read against " + cb.Ref
		}
		return sprint.AdmissionTree{Missing: why, At: at}
	}
	list, err := l.git(ctx, bb.Repo, "ls-tree", "-r", "--name-only", "-z", "--end-of-options", bb.Sha)
	if err != nil {
		return sprint.AdmissionTree{Missing: fmt.Sprintf("could not list the tree at %s (%s): %v", bb.Sha[:12], cb.Ref, err), At: at}
	}
	var files []string
	for _, f := range strings.Split(list, "\x00") {
		if f != "" {
			files = append(files, f)
		}
	}
	holds := map[string][]string{}
	return sprint.AdmissionTree{
		Files: files,
		At:    at,
		Hold: func(ident string) ([]string, error) {
			if got, ok := holds[ident]; ok {
				return got, nil
			}
			got, err := gitGrepFiles(ctx, a.gitEnv, bb.Repo, bb.Sha, ident)
			if err != nil {
				return nil, err
			}
			holds[ident] = got
			return got, nil
		},
	}
}

// gitGrepFiles is the files at sha whose text contains ident, a fixed-string grep.
// git grep exits 1 with empty stderr when nothing matches, and that is an answer.
func gitGrepFiles(ctx context.Context, env []string, repo, sha, ident string) ([]string, error) {
	if len(sha) < 40 || strings.ContainsAny(ident, "\n") {
		return nil, fmt.Errorf("the tree %q is not a full commit sha", sha)
	}
	res, err := gitrun.Run(ctx, gitrun.Options{C: repo, Env: env, OwnRepo: true}, "grep", "-F", "-l", "-e", ident, sha, "--")
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && ee.ExitCode() == 1 && strings.TrimSpace(string(res.Stderr)) == "" {
			return nil, nil
		}
		if msg := strings.TrimSpace(string(res.Stderr)); msg != "" {
			return nil, fmt.Errorf("could not search the tree at %s for %s: %s", sha[:12], ident, msg)
		}
		return nil, fmt.Errorf("could not search the tree at %s for %s: %v", sha[:12], ident, err)
	}
	prefix := sha + ":"
	var out []string
	for _, line := range strings.Split(string(res.Stdout), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		out = append(out, strings.TrimPrefix(line, prefix))
	}
	return out, nil
}

// pathsHeaderLine is the brief's PATHS line, 1 when it has none.
func pathsHeaderLine(brief string) int {
	for i, line := range strings.Split(brief, "\n") {
		if k, _, ok := cardhdr.KeyValue(strings.TrimSpace(line)); ok && k == "PATHS" {
			return i + 1
		}
	}
	return 1
}
