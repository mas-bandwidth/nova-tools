package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/gitrun"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// mirrorPass is one pass of nova-swarm mirror. git and exists are the seams
// a test replaces, so a test never runs git and never opens a network.
type mirrorPass struct {
	dir, remote string
	repos       []string
	every       time.Duration
	dry         bool
	git         func(args ...string) error
	exists      func(path string) bool
	out         io.Writer
	failed      int
}

func (m *mirrorPass) say(line string) {
	fmt.Fprintln(m.out, oneline.Escape(line))
}

// run fetches each mirror, or clones it when the directory is missing.
// --dry-run prints the action and runs no git. The pass does not sleep.
func (m *mirrorPass) run() int {
	for _, repo := range m.repos {
		dest := filepath.Join(m.dir, repo+".git")
		if m.exists(dest) {
			m.fetch(repo, dest)
			continue
		}
		m.clone(repo, dest)
	}
	if m.failed > 0 {
		fmt.Fprintf(m.out, "MIRROR INCOMPLETE repos=%d failed=%d\n", len(m.repos), m.failed)
		return 1
	}
	fmt.Fprintf(m.out, "MIRROR OK repos=%d every=%s\n", len(m.repos), oneline.Field(m.every.String()))
	return 0
}

func (m *mirrorPass) fetch(repo, dest string) {
	if m.dry {
		m.say(fmt.Sprintf("WOULD-FETCH repo=%s dest=%s", oneline.Field(repo), oneline.Field(dest)))
		return
	}
	if err := m.git("-C", dest, "fetch", "--prune"); err != nil {
		m.failed++
		m.say(fmt.Sprintf("NOTE fetch %s failed (%s)", oneline.Field(repo), oneline.Err(err)))
		return
	}
	m.say(fmt.Sprintf("FETCHED repo=%s dest=%s", oneline.Field(repo), oneline.Field(dest)))
}

func (m *mirrorPass) clone(repo, dest string) {
	if !strings.Contains(m.remote, "{repo}") {
		m.failed++
		m.say(fmt.Sprintf("NOTE clone %s needs --remote with {repo}", oneline.Field(repo)))
		return
	}
	url := strings.ReplaceAll(m.remote, "{repo}", repo)
	if m.dry {
		m.say(fmt.Sprintf("WOULD-CLONE repo=%s dest=%s", oneline.Field(repo), oneline.Field(dest)))
		return
	}
	if err := m.git("clone", "--mirror", url, dest); err != nil {
		m.failed++
		m.say(fmt.Sprintf("NOTE clone %s failed (%s)", oneline.Field(repo), oneline.Err(err)))
		return
	}
	m.say(fmt.Sprintf("CLONED repo=%s dest=%s", oneline.Field(repo), oneline.Field(dest)))
}

// gitMirror is the production git. It captures the child's output and does
// not copy it onto this process's streams.
func gitMirror(args ...string) error {
	_, err := gitrun.Run(context.Background(), gitrun.Options{}, args...)
	if err != nil {
		return fmt.Errorf("%s", oneline.Err(err))
	}
	return nil
}

func mirrorExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.IsDir()
}

// mirrorRepos splits --repos. A name with a slash or .. is refused.
func mirrorRepos(raw string) ([]string, error) {
	var out []string
	for _, r := range strings.Split(raw, ",") {
		r = strings.TrimSpace(r)
		if r == "" {
			continue
		}
		if strings.Contains(r, "/") || strings.Contains(r, `\`) || strings.Contains(r, "..") {
			return nil, fmt.Errorf("repository %s", oneline.Field(r))
		}
		out = append(out, r)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("none")
	}
	return out, nil
}

func mirrorRemoteRefused(remote string) bool {
	low := strings.ToLower(remote)
	return strings.Contains(low, "https://") || strings.Contains(low, "http://")
}

// cmdMirror is `nova-swarm mirror`: one pass, no sleep, no unit install.
func cmdMirror(args []string, stdout, stderr io.Writer) int {
	f := newFlags("mirror")
	dir := f.fs.String("dir", "", "the `dir` the mirrors live in, each <dir>/<repo>.git")
	repos := f.fs.String("repos", "", "the repository names, comma-separated, `a,b`")
	remote := f.fs.String("remote", "", "the clone `url` for a missing mirror; it must contain {repo}; http and https are refused")
	every := f.fs.Duration("every", 60*time.Second, "how often the supervisor repeats this pass, a `duration` above 0 (default 60s); this pass does not sleep")
	dry := f.fs.Bool("dry-run", false, "print WOULD-FETCH or WOULD-CLONE and run no git")
	if !f.parse(args, stderr) {
		return 2
	}
	if *dir == "" {
		f.add("--dir is required")
	}
	names, err := mirrorRepos(*repos)
	if err != nil {
		f.add("--repos wants one or more repository names with no slash, got " + oneline.Field(*repos))
	}
	if *every <= 0 {
		f.add("--every is a duration above 0, got " + oneline.Field(every.String()))
	}
	if mirrorRemoteRefused(*remote) {
		f.add("--remote refuses http and https")
	}
	if f.refused(stderr) {
		return 2
	}
	m := &mirrorPass{
		dir: *dir, remote: *remote, repos: names, every: *every, dry: *dry,
		git: gitMirror, exists: mirrorExists, out: stdout,
	}
	return m.run()
}
