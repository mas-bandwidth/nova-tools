package main

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
	"github.com/mas-bandwidth/nova-tools/internal/cardtree"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
	"io"
)

// alreadyDoneAtBase checks Git evidence only (docs/SPEC-SPRINT.md section 11,
// add-first-check-b.w1). It does not run the brief's TEST.
func (a *app) alreadyDoneAtBase(ctx context.Context, l *lander, id, brief string) (string, error) {
	cb := swarm.ReadCardBase([]byte(brief))
	if cb.Named == "" || cb.Ref == "" {
		return "", nil
	}
	bb := a.briefBaseAt(ctx, l, cb)
	if bb.Missing != "" || len(bb.Sha) != 40 {
		// The existing brief-base checks own missing or dead bases. This check is
		// advisory evidence for a readable base, not a replacement for those checks.
		return "", nil
	}
	tree := cardtree.Parse(brief)
	for _, step := range tree.Work() {
		prefix, _, ok := strings.Cut(strings.TrimSpace(step.Commit), ":")
		if !ok || prefix == "" {
			continue
		}
		out, err := l.git(ctx, bb.Repo, "log", "--format=%H%x00%s", bb.Sha, "--")
		if err != nil {
			return "", fmt.Errorf("cannot read commits at BASE %s: %w", cb.Ref, err)
		}
		for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
			sha, subject, ok := strings.Cut(line, "\x00")
			if ok && strings.HasPrefix(subject, prefix+":") {
				return fmt.Sprintf("base commit %s has subject %q", sha, subject), nil
			}
		}
	}
	refs, err := l.git(ctx, bb.Repo, "ls-remote", "--heads", "origin")
	if err != nil {
		return "", fmt.Errorf("cannot read pushed card branches for %s: %w", id, err)
	}
	if id != "" {
		branchPrefix := "refs/heads/sprint/" + id + "."
		for _, line := range strings.Split(strings.TrimSpace(refs), "\n") {
			sha, ref, ok := strings.Cut(line, "\t")
			if !ok || !strings.HasPrefix(ref, branchPrefix) {
				continue
			}
			_, err := l.git(ctx, bb.Repo, "merge-base", "--is-ancestor", sha, bb.Sha)
			if err == nil {
				return fmt.Sprintf("pushed branch %s at %s is an ancestor of base %s", strings.TrimPrefix(ref, "refs/heads/"), sha, bb.Sha), nil
			}
		}
	}
	if evidence, ok, err := a.createdTestAtBase(ctx, l, bb.Repo, bb.Sha, brief); err != nil {
		return "", err
	} else if ok {
		return evidence, nil
	}
	return "", nil
}

func (a *app) createdTestAtBase(ctx context.Context, l *lander, repo, sha, brief string) (string, bool, error) {
	newValue, hasNew := swarm.CardHeaderValue([]byte(brief), "NEW")
	testValue, hasTest := swarm.CardHeaderValue([]byte(brief), "TEST")
	if !hasNew || !hasTest {
		return "", false, nil
	}
	test, why := cardhdr.ParseTest(testValue)
	if why != "" || test.None {
		return "", false, nil
	}
	pathsValue, _ := swarm.CardHeaderValue([]byte(brief), "PATHS")
	paths := splitGlobs(pathsValue)
	newGlobs := splitGlobs(newValue)
	if len(paths) == 0 || len(newGlobs) == 0 {
		return "", false, nil
	}
	filesOut, err := l.git(ctx, repo, "ls-tree", "-r", "--name-only", "-z", sha)
	if err != nil {
		return "", false, fmt.Errorf("cannot list files at BASE: %w", err)
	}
	files := strings.Split(filesOut, "\x00")
	var created []string
	for _, glob := range newGlobs {
		inPaths := false
		for _, path := range paths {
			if patternCovers(path, glob) {
				inPaths = true
				break
			}
		}
		if !inPaths {
			continue
		}
		matched := ""
		for _, file := range files {
			if hit, _ := filepath.Match(glob, file); hit {
				matched = file
				break
			}
		}
		if matched == "" {
			return "", false, nil
		}
		created = append(created, matched)
	}
	if len(created) == 0 {
		return "", false, nil
	}
	needle := "func " + test.Name + "("
	args := []string{"grep", "-F", "-l", "-e", needle, sha, "--"}
	if testDir := strings.TrimPrefix(test.Package, "./"); testDir != "." {
		args = append(args, testDir)
	}
	out, err := l.git(ctx, repo, args...)
	if err != nil || strings.TrimSpace(out) == "" {
		return "", false, nil
	}
	return fmt.Sprintf("created file %s and named TEST function %s are present at base %s", created[0], test.Name, sha), true, nil
}

func splitGlobs(value string) []string {
	var out []string
	for _, s := range strings.Split(value, ",") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func patternCovers(paths, created string) bool {
	if paths == created {
		return true
	}
	if strings.ContainsAny(paths, "*?[") {
		ok, _ := filepath.Match(paths, created)
		return ok
	}
	return false
}

func (a *app) holdAlreadyDone(briefs []briefCheck, allow bool, stderr io.Writer) int {
	if allow {
		return 0
	}
	l := &lander{a: a}
	for _, b := range briefs {
		ctx, cancel := context.WithTimeout(context.Background(), briefBaseFetch)
		evidence, err := a.alreadyDoneAtBase(ctx, l, b.id, b.brief)
		cancel()
		if err != nil {
			return refuse(stderr, "add", err.Error())
		}
		if evidence != "" {
			return refuse(stderr, "add", fmt.Sprintf("card %s is already done at its BASE: %s", oneline.Field(b.id), evidence))
		}
	}
	return 0
}
