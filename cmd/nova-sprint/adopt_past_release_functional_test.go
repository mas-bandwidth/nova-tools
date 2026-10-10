//go:build functional

package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// pastAdoptCuts are the releases the adopt-from-each-past-release check runs
// from: v1.1.0, v1.2.0 and v1.2.1, the cuts the 2026-10-09 study names. Each
// one's own fleet/tools.yml is the adopt play the seat adopts a build through
// (docs/SPEC-SPRINT.md, "Adopting a build", and "The client of this build is
// read by the last release's server").
var pastAdoptCuts = []string{"v1.1.0", "v1.2.0", "v1.2.1"}

// pastReleasePlay reads one file of a past cut's own tree out of its release
// tag. It skips the cut when this checkout holds no such tag, so the check runs
// where the tags do (the functional tier, which stages the whole repository) and
// never fails a checkout that was made without tags.
func pastReleasePlay(t *testing.T, cut, file string) string {
	t.Helper()
	root := pastReleaseRoot(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", root, "show", cut+":"+file)
	cmd.WaitDelay = 5 * time.Second
	out, err := cmd.Output()
	if err != nil {
		t.Skipf("%s is not a tag of this checkout: %v", cut, err)
	}
	return string(out)
}

// pastReleaseRoot walks up from the package directory to the module root, the
// checkout `git show` reads a tag from.
func pastReleaseRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	require.NoError(t, err)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		require.NotEqual(t, parent, dir, "no go.mod above %s", dir)
		dir = parent
	}
}

// pastReleaseBeat is the play runner the adopt verb is given for one past cut:
// it is the seam ansible-playbook fills in a real adoption, and here it holds
// the seat's beat the way the play's friends step does (a beat the scratch
// store records), then answers with the cut's own ADOPT lines. It keeps the
// argv the adopt verb built, so the test can hold it to the cut's version and
// play.
type pastReleaseBeat struct {
	t    *testing.T
	app  *app
	argv []string
	beat string
}

func (p *pastReleaseBeat) Play(_ context.Context, argv []string) (string, error) {
	p.t.Helper()
	p.argv = argv
	var out, errs bytes.Buffer
	code := p.app.run([]string{"fleet", "beat", "seat"}, &out, &errs)
	require.Zero(p.t, code, "the seat did not beat: %s", errs.String())
	p.beat = out.String()
	return playOK, nil
}

// TestAdoptFromEachPastReleaseBeatsGreen is the end-to-end half of the
// compatibility contract, the companion of
// TestEveryVerbOfThisClientIsReadByTheLastRelease: for each past cut it reads
// the cut's own fleet/tools.yml -- the adopt play, with every step it carries
// (store, server, dashboard, friends) and the friends' beat check -- and runs
// the adopt verb with it on a scratch store and seat, then holds the adopted
// build's beat green (docs/SPEC-SPRINT.md, "The client of this build is read by
// the last release's server"). It needs the play, a store and a seat, so it is
// a functional test and never runs on a pull request.
func TestAdoptFromEachPastReleaseBeatsGreen(t *testing.T) {
	t.Parallel()
	for _, cut := range pastAdoptCuts {
		t.Run(cut, func(t *testing.T) {
			t.Parallel()
			play := pastReleasePlay(t, cut, "fleet/tools.yml")
			// the cut's play is the adopt play: every step, and the friends'
			// beat after their reinstall, are in it.
			for _, step := range []string{"store", "server", "dashboard", "friends"} {
				require.Contains(t, play, "ADOPT step="+step, "%s's play does not carry the %s step", cut, step)
			}
			require.Contains(t, play, "beat", "%s's play does not check the friends' beats", cut)

			// a scratch store and seat: the cut's play as the checkout the
			// adopt verb reads, and the same store the beat is recorded in.
			file := filepath.Join(t.TempDir(), "sprint.twin")
			home := t.TempDir()
			env := map[string]string{"NOVA_SPRINT_REDIS": "mem:" + file, "NOVA_SPRINT_ACTOR": "boss", "HOME": home}
			a := newApp(func(k string) string { return env[k] })
			t.Cleanup(a.close)
			run := func(args ...string) (int, string, string) {
				t.Helper()
				var out, errs bytes.Buffer
				code := a.run(args, &out, &errs)
				return code, out.String(), errs.String()
			}
			code, _, errs := run("init", "--readers", "reader-a", "--members", "seat")
			require.Zero(t, code, "%s: the scratch store did not start: %s", cut, errs)

			source := t.TempDir()
			require.NoError(t, os.MkdirAll(filepath.Join(source, "fleet"), 0o755))
			require.NoError(t, os.WriteFile(filepath.Join(source, "fleet", "tools.yml"), []byte(play), 0o644))

			beat := &pastReleaseBeat{t: t, app: a}
			adoptPlayOf.Store(a, beat)
			defer adoptPlayOf.Delete(a)

			code, out, errs := run("adopt", cut, "--source", source, "--inventory", "/inv/nova-inventory", "--reason", "the compatibility check")
			require.Zero(t, code, "%s: the adopt play did not finish: %s", cut, errs)
			assert.Contains(t, out, "ADOPT ADOPTED version="+cut+" ", "%s: the play did not adopt", cut)
			assert.Contains(t, beat.argv, "nova_version="+cut, "%s: the play was not run for the cut", cut)
			assert.Contains(t, beat.argv, filepath.Join(source, "fleet", "tools.yml"), "%s: the play was not the cut's own", cut)
			assert.Contains(t, beat.beat, "FLEET-BEAT OK seat", "%s: the adopted build's beat is not green", cut)
		})
	}
}
