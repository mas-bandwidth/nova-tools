package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
	"github.com/mas-bandwidth/nova-tools/internal/member"
	"github.com/mas-bandwidth/nova-tools/internal/safepath"
	"github.com/mas-bandwidth/nova-tools/internal/subproc"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// scriptVerify is a reader's member.Config.ScriptVerify (docs/SPEC-SPRINT.md, the script
// read): a read of a script card is asked first of a ScriptVerifier that checks the head
// out of the bench mirror of the card's repository, runs the card's program in the same
// wall the member runs its script steps in, and compares the program's diff with the
// head's. The verifier is built per packet because the mirror is the card's repository's;
// the checkouts sit under root, the reader's own work dir. sandbox names the wall binary
// ("" resolves nova-sandbox on PATH) and noWall runs the program unconfined, as the
// member's own --no-wall does for its children.
func scriptVerify(root, sandbox string, noWall bool) func(member.Packet, cardhdr.Class) (bool, string) {
	base := filepath.Join(root, "script-read")
	return func(p member.Packet, class cardhdr.Class) (ok bool, why string) {
		repo := swarm.ReadCardBase([]byte(p.Brief)).Repo
		home, _ := os.UserHomeDir()
		mirror := swarm.FindBenchMirror(home, repo)
		if mirror == "" {
			return false, "no bench mirror for " + repo
		}
		if err := os.MkdirAll(base, 0o700); err != nil {
			return false, "the script read's work dir " + base + ": " + err.Error()
		}
		v := member.ScriptVerifier{Mirror: mirror, Temp: base, Run: scriptRun(base, sandbox, noWall)}
		return v.Verify(p, class)
	}
}

// scriptRun runs a script card's program in the member's wall (step.go's cardtree.Wall):
// the checkout and a private temp its only writes, the network denied, the toolchain and
// the checkout's borrowed objects readable, all under ctx (the card's deadline). It is the
// one place the script read starts a process, so a test can hand Verify a fake instead.
func scriptRun(base, sandbox string, noWall bool) func(context.Context, string, []string) error {
	return func(ctx context.Context, dir string, argv []string) error {
		wall, why := stepWall(sandbox, noWall)
		if why != "" {
			return errors.New(why)
		}
		work, err := os.MkdirTemp(base, "wall-")
		if err != nil {
			return err
		}
		defer func() { _ = safepath.RemoveUnder(base, work) }() // ignored: the work dir is this read's own, under the reader's work dir the pool sweeps
		bin, tmp := filepath.Join(work, "bin"), filepath.Join(work, "tmp")
		for _, d := range []string{bin, tmp} {
			if err := os.MkdirAll(d, 0o755); err != nil {
				return err
			}
		}
		wall.Tmp = tmp
		if wall.Bin != "" {
			wall.Read = stepReads(dir, bin, benchPasswdHome())
		}
		run := argv
		if wall.Bin != "" {
			run = append([]string{wall.Bin}, wall.Argv(dir, argv)...)
		}
		cmd := subproc.Context(ctx, run[0], run[1:]...)
		cmd.Dir, cmd.Env = dir, wall.Env(os.Environ())
		return cmd.Run()
	}
}
