package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// verbAdoptFake is the verb's steps, faked: the base is ahead of the live
// build, the cold read comes back ok, and every machine reads back the build.
type verbAdoptFake struct {
	migrated bool
	switched bool
	pushed   []string
}

func (f *verbAdoptFake) BaseTip(context.Context) (string, error) {
	return "1111111111111111111111111111111111111111", nil
}
func (f *verbAdoptFake) LiveBuild(context.Context) (string, error) { return "222222222222", nil }
func (f *verbAdoptFake) Build(_ context.Context, tip string) (sprint.AdoptBuild, error) {
	return sprint.AdoptBuild{Tip: tip, Version: "v1.0.0-adopt.111111111111", Dir: "/out"}, nil
}
func (f *verbAdoptFake) Canary(context.Context, sprint.AdoptBuild) (string, error) {
	return "verified=9", nil
}
func (f *verbAdoptFake) Shadow(context.Context, sprint.AdoptBuild) (string, error) {
	return "parts=3", nil
}
func (f *verbAdoptFake) DealColdRead(context.Context, sprint.AdoptBuild) (string, error) {
	return "adopt-read-111111111111", nil
}
func (f *verbAdoptFake) ColdRead(context.Context, string) (sprint.AdoptRead, error) {
	return sprint.AdoptRead{Done: true, OK: true}, nil
}
func (f *verbAdoptFake) Ask(context.Context, sprint.AdoptJudgment) error { return nil }
func (f *verbAdoptFake) Migrate(context.Context, sprint.AdoptBuild) (string, error) {
	f.migrated = true
	return "role=nova_config from=35 to=36 applied=1", nil
}
func (f *verbAdoptFake) KeepRollback(context.Context) ([]string, error) {
	return []string{"/srv/nova-sprint.adopt-prev"}, nil
}
func (f *verbAdoptFake) Switch(context.Context, sprint.AdoptBuild) error {
	f.switched = true
	return nil
}
func (f *verbAdoptFake) Machines(context.Context) ([]string, error) {
	return []string{"m1", "m2"}, nil
}
func (f *verbAdoptFake) Push(_ context.Context, m string, _ sprint.AdoptBuild) error {
	f.pushed = append(f.pushed, m)
	return nil
}
func (f *verbAdoptFake) Version(context.Context, string) (string, error) {
	return "v1.0.0-adopt.111111111111", nil
}
func (f *verbAdoptFake) LastTick(context.Context) (time.Time, error) { return time.Time{}, nil }
func (f *verbAdoptFake) Rollback(context.Context, []string) error    { return nil }

func TestAdoptVerbAsksOneJudgmentAndActsOnTheAnswer(t *testing.T) {
	t.Parallel()
	f := &verbAdoptFake{}
	state := filepath.Join(t.TempDir(), "adopt.json")
	a := newApp(func(string) string { return "" })
	adoptStepsOf.Store(a, sprint.AdoptSteps(f))
	t.Cleanup(func() { adoptStepsOf.Delete(a) })
	// cmdAdopt, not a.run: the verb table's adopt is the play (adopt_play.go).
	run := func(args ...string) (int, string, string) {
		var out, errs bytes.Buffer
		code := a.cmdAdopt(append([]string{"--state", state}, args...), &out, &errs)
		return code, out.String(), errs.String()
	}

	code, out, errs := run()
	require.Equal(t, 0, code, errs)
	assert.Contains(t, out, "ADOPT START tip=111111111111 live=222222222222")
	assert.Contains(t, out, "JUDGMENT adopt 111111111111")
	assert.False(t, f.switched)

	code, _, errs = run("--answer", "yes", "--judgment", "999999999999", "--reason", "green")
	assert.Equal(t, 1, code, "an answer to another judgment is refused")
	assert.Contains(t, errs, "the open judgment is 111111111111")

	code, _, errs = run("--answer", "yes", "--judgment", "111111111111")
	assert.Equal(t, 2, code, "an answer wants a reason")
	assert.Contains(t, errs, "--reason")

	code, out, errs = run("--answer", "yes", "--judgment", "111111111111", "--reason", "canary, shadow and read green")
	require.Equal(t, 0, code, errs)
	assert.Contains(t, out, "ADOPT ANSWERED judgment=111111111111 answer=yes")
	assert.False(t, f.switched, "an answer runs nothing; the next pass acts")

	code, out, errs = run()
	require.Equal(t, 0, code, errs)
	assert.True(t, f.migrated, "the config store is migrated before the switch")
	assert.Contains(t, out, "ADOPT MIGRATED tip=111111111111 role=nova_config from=35 to=36 applied=1")
	assert.True(t, f.switched)
	assert.Equal(t, []string{"m1", "m2"}, f.pushed)
	assert.Contains(t, out, "ADOPT FLEET version=v1.0.0-adopt.111111111111 machines=2 read_back=2")

	code, out, _ = run("--show")
	require.Equal(t, 0, code)
	assert.Contains(t, out, `"stage":"watching"`)
}

func TestAdoptVerbNamesWhatAPassWants(t *testing.T) {
	t.Parallel()
	a := newApp(func(string) string { return "" })
	var out, errs bytes.Buffer
	code := a.cmdAdopt([]string{"--state", filepath.Join(t.TempDir(), "adopt.json")}, &out, &errs)
	assert.Equal(t, 2, code)
	assert.Contains(t, errs.String(), "a pass wants --base, --bench")
	assert.Empty(t, out.String())
}

func TestColdReadOfReadsTheReportsLastWord(t *testing.T) {
	t.Parallel()
	assert.Equal(t, sprint.AdoptRead{Done: true, OK: true, Finding: "the log is clean."}, coldReadOf("the log is clean.\nREAD OK"))
	r := coldReadOf("READ BROKEN: the shadow tick refuses a held card")
	assert.False(t, r.OK)
	assert.Equal(t, "the shadow tick refuses a held card", r.Finding)
	r = coldReadOf("looks fine")
	assert.False(t, r.OK)
	assert.True(t, strings.HasPrefix(r.Finding, "the report says neither"))
}

func TestAdoptionSwitchesFriendDaemonsFromTheNovaFriendArtifact(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	platform := filepath.Join(dir, "linux-amd64")
	require.NoError(t, os.MkdirAll(platform, 0o755))
	serverCandidate := filepath.Join(platform, "nova-sprint")
	daemonCandidate := filepath.Join(platform, "nova-friend")
	serverTarget := filepath.Join(dir, "server")
	daemonTarget := filepath.Join(dir, "friend-daemon")
	for path, body := range map[string]string{
		serverCandidate: "new sprint", daemonCandidate: "new friend",
		serverTarget: "old sprint", daemonTarget: "old friend",
	} {
		require.NoError(t, os.WriteFile(path, []byte(body), 0o755))
	}
	s := &adoptSteps{a: &app{now: time.Now}, platform: "linux-amd64", serverBin: serverTarget, daemons: []string{daemonTarget}}
	kept, err := s.KeepRollback(context.Background())
	require.NoError(t, err)
	require.NoError(t, s.Switch(context.Background(), sprint.AdoptBuild{Dir: dir}))
	server, err := os.ReadFile(serverTarget)
	require.NoError(t, err)
	daemon, err := os.ReadFile(daemonTarget)
	require.NoError(t, err)
	assert.Equal(t, "new sprint", string(server))
	assert.Equal(t, "new friend", string(daemon), "a daemon must receive nova-friend, not nova-sprint")
	require.NoError(t, s.Rollback(context.Background(), kept))
	server, err = os.ReadFile(serverTarget)
	require.NoError(t, err)
	daemon, err = os.ReadFile(daemonTarget)
	require.NoError(t, err)
	assert.Equal(t, "old sprint", string(server))
	assert.Equal(t, "old friend", string(daemon))
}
