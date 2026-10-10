package swarm

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/subproc"
)

// CmdRunner runs one command in dir with env added to the process's own, and returns its
// failure. A test's fake records the call and runs nothing.
type CmdRunner func(ctx context.Context, dir string, env []string, name string, args ...string) error

// MirrorPath is the bench mirror of the repository named name: the first place
// FindBenchMirror looks, so a stage finds what a keeper made.
func MirrorPath(mirrorDir, name string) string {
	return filepath.Join(mirrorDir, strings.TrimSuffix(name, ".git")+".git")
}

// cardMirrorDir is where a pulse card's clone step looks for the bench mirror.
const cardMirrorDir = "$HOME/nova-bench/mirror/"

// PointCardAtMirrors writes the bench's own mirror directory into a card's clone step: the
// harness runs with HOME set to the slot's data home, where no mirror is, so the card the
// member hands it names the directory MirrorPath makes under benchHome instead of $HOME.
// A card with no clone step, or a benchHome of "", is returned as it was.
func PointCardAtMirrors(card, benchHome string) string {
	if benchHome == "" {
		return card
	}
	return strings.ReplaceAll(card, cardMirrorDir, benchHome+"/")
}

// MirrorKeeper keeps one bare mirror of Origin at Mirror and a build cache warmed at the
// mirror's Ref tip. A job's clone borrows the mirror's objects (StageCard, MirrorCloneArgs),
// so a card's clone reads no network and its first build reads a warm GOCACHE: the two
// waits the sprint measured in a card's wall (docs/SPEC-SWARM.md, the warm clones and
// caches card). The mirror carries gc.auto=0 and is only ever fetched into, so no object a
// live checkout borrows is dropped (mirrorKeepsObjects).
type MirrorKeeper struct {
	Origin  string    // the remote: a URL or a path
	Mirror  string    // the bare mirror's directory (MirrorPath)
	Ref     string    // the branch whose tip the cache is warmed at; "" is the mirror's HEAD branch
	WarmDir string    // where the warm checkout lives, inside the member's work root
	GoCache string    // GOCACHE the warm builds fill: the one the member's cards share
	Run     CmdRunner // runs the warm commands; nil is exec
}

// MirrorRefresh is what one Refresh did.
type MirrorRefresh struct {
	Created bool   // the mirror did not exist and was cloned
	Moved   bool   // the Ref tip is not the one the cache was last warmed at
	Tip     string // the Ref tip after the refresh
}

// Refresh creates the mirror when absent and fetches into it otherwise, then warms the
// cache when the Ref tip moved. A warm failure is returned beside the refresh: the mirror
// is good, and a cold cache costs time, never correctness. It runs at the member's start
// and after each landing the member sees.
func (k MirrorKeeper) Refresh(ctx context.Context) (MirrorRefresh, error) {
	var res MirrorRefresh
	if _, err := os.Stat(filepath.Join(k.Mirror, "HEAD")); err != nil {
		if err := os.MkdirAll(filepath.Dir(k.Mirror), 0o755); err != nil {
			return res, err
		}
		if out, err := stageGit(ctx, "clone", "-q", "--mirror", "--", k.Origin, k.Mirror).CombinedOutput(); err != nil {
			return res, fmt.Errorf("mirror clone of %s: %s (%w)", k.Origin, strings.TrimSpace(string(out)), err)
		}
		res.Created = true
	} else if out, err := stageGit(ctx, "-C", k.Mirror, "fetch", "-q", "origin").CombinedOutput(); err != nil {
		return res, fmt.Errorf("mirror fetch of %s: %s (%w)", k.Origin, strings.TrimSpace(string(out)), err)
	}
	if !mirrorKeepsObjects(ctx, stageGit, k.Mirror) {
		return res, fmt.Errorf("mirror %s: gc.auto could not be set to 0", k.Mirror)
	}
	if k.WarmDir == "" || k.GoCache == "" {
		return res, nil
	}
	ref := k.Ref
	if ref == "" {
		head, err := stageGit(ctx, "-C", k.Mirror, "symbolic-ref", "--short", "HEAD").Output()
		if err != nil {
			return res, fmt.Errorf("mirror %s: HEAD names no branch: %w", k.Mirror, err)
		}
		ref = strings.TrimSpace(string(head))
	}
	tip, err := stageGit(ctx, "-C", k.Mirror, "rev-parse", "--verify", "--end-of-options", "refs/heads/"+ref+"^{commit}").Output()
	if err != nil {
		return res, fmt.Errorf("mirror %s has no branch %s: %w", k.Mirror, ref, err)
	}
	res.Tip = strings.TrimSpace(string(tip))
	warmed := filepath.Join(k.WarmDir, ".warmed")
	if last, err := os.ReadFile(warmed); err == nil && strings.TrimSpace(string(last)) == res.Tip {
		return res, nil
	}
	res.Moved = true
	if err := k.warm(ctx, res.Tip); err != nil {
		return res, err
	}
	return res, os.WriteFile(warmed, []byte(res.Tip+"\n"), 0o644)
}

// warm builds and vets the tree at tip into GoCache, at low priority: a checkout borrowed
// from the mirror under WarmDir, moved to tip, then go build and go vet over every package.
func (k MirrorKeeper) warm(ctx context.Context, tip string) error {
	run := k.Run
	if run == nil {
		run = runCmd
	}
	tree := filepath.Join(k.WarmDir, "tree")
	if _, err := os.Stat(filepath.Join(tree, ".git")); err != nil {
		if err := os.MkdirAll(k.WarmDir, 0o755); err != nil {
			return err
		}
		if out, err := stageGit(ctx, MirrorCloneArgs(k.Mirror, tree, true)...).CombinedOutput(); err != nil {
			return fmt.Errorf("warm checkout: %s (%w)", strings.TrimSpace(string(out)), err)
		}
	}
	for _, args := range [][]string{{"fetch", "-q", "origin"}, {"checkout", "-q", "--detach", tip}} {
		if out, err := stageGit(ctx, append([]string{"-C", tree}, args...)...).CombinedOutput(); err != nil {
			return fmt.Errorf("warm git %s: %s (%w)", args[0], strings.TrimSpace(string(out)), err)
		}
	}
	env := []string{"GOCACHE=" + k.GoCache, "GOFLAGS=-mod=readonly", "NOVA_TEST_NO_HOST=1"}
	for _, verb := range []string{"build", "vet"} {
		if err := run(ctx, tree, env, "nice", "-n", "19", "go", verb, "./..."); err != nil {
			return fmt.Errorf("warm go %s: %w", verb, err)
		}
	}
	return nil
}

func runCmd(ctx context.Context, dir string, env []string, name string, args ...string) error {
	cmd := subproc.Context(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), append(env, "GIT_TERMINAL_PROMPT=0")...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}
