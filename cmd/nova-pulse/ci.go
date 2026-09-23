package main

import (
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/sprintci"
)

func cmdCI(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintf(stderr, "nova-pulse ci: a sub-verb is required (cut); run: nova-pulse help\n")
		return 2
	}
	switch args[0] {
	case "cut":
		return cmdCICut(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "nova-pulse ci: unknown sub-verb %q (the sub-verb is cut; run: nova-pulse help)\n", args[0])
		return 2
	}
}

func cmdCICut(args []string, stdout, stderr io.Writer) int {
	f := newFlags("ci cut")
	pr := f.fs.Int("pr", 0, "")
	repo := f.fs.String("repo", "", "")
	sha := f.fs.String("sha", "", "")
	paths := f.fs.String("paths", "", "")
	mirror := f.fs.String("mirror", "", "")
	front := f.fs.String("front", "", "")
	if !f.parse(args, stderr) {
		return 2
	}
	if *pr < 1 {
		f.add(fmt.Sprintf("--pr wants a pull request number, got %d", *pr))
	}
	f.want(*repo, "repo", "owner/name, one word")
	f.want(*sha, "sha", "the 40-digit head")
	f.want(*paths, "paths", "comma-separated packages, the PATHS line")
	f.want(*mirror, "mirror", "the absolute path of the bench mirror")
	f.want(*front, "front", "the front tier directory")
	if f.refused(stderr) {
		return 2
	}
	wrote, err := sprintci.Cut(sprintci.CutInput{
		Repo:   *repo,
		PR:     *pr,
		SHA:    *sha,
		Paths:  splitPaths(*paths),
		Mirror: *mirror,
		Front:  *front,
	})
	if err != nil && !errors.Is(err, sprintci.ErrCutExists) {
		fmt.Fprintf(stderr, "nova-pulse ci cut: %s\n", err)
		return 2
	}
	word := "CI CUT"
	if errors.Is(err, sprintci.ErrCutExists) {
		word = "CI CUT EXISTS"
	}
	fmt.Fprintf(stdout, "%s card=%s front=%s\n", word, filepath.Base(wrote), *front)
	return 0
}

func splitPaths(v string) []string {
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
