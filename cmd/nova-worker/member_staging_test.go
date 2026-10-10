package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/member"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// A launch native refused at staging ran no child: the member reads the STAGE FAIL line's
// reason as the staging kind, and reads no RESULT.md (one left in the results is not this
// launch's).
func TestALaunchRefusedAtStagingIsReadAsTheStagingKindWithItsReason(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	logPath := filepath.Join(dir, "c1.native.log")
	require.NoError(t, os.WriteFile(logPath, []byte("STAGE FAIL bench=studio repo=https://example.com/o/quack.git base=main reason=staging refused: no bench mirror for https://example.com/o/quack.git: a card may not clone directly from github without a bench mirror\n"+
		"NATIVE REFUSED: staging refused: no bench mirror for https://example.com/o/quack.git\n"), 0o644))
	results := filepath.Join(dir, "results")
	require.NoError(t, os.MkdirAll(results, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(results, "RESULT.md"), []byte("## Head\n\nverdict: ok\n\n## One line\n\nan older run's\n"), 0o644))
	c := &nativeChild{card: "c1", logPath: logPath, results: results, job: filepath.Join(dir, "job"), done: make(chan struct{})}
	r := c.Result()
	assert.Equal(t, member.EndStaging, r.End)
	assert.Equal(t, "no bench mirror for https://example.com/o/quack.git: a card may not clone directly from github without a bench mirror", r.Staging)
	assert.False(t, r.Shaped)
	assert.Empty(t, r.Verdict, "no RESULT.md is read")
	assert.NotContains(t, r.Report, "an older run's")
	fin, why := member.Judge(r, member.Push{None: "no head"})
	assert.Equal(t, member.FinishFailed, fin)
	assert.Equal(t, "staging refused: no bench mirror for https://example.com/o/quack.git: a card may not clone directly from github without a bench mirror", why)
}

// The STAGE FAIL line's base is the card's: its sha's first eight, else its ref.
func TestTheStageFailLineNamesTheCardsBase(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "main", stageFailBase(swarm.StageResult{Ref: "main"}))
	assert.Equal(t, "09fbedc9", stageFailBase(swarm.StageResult{BaseSha: "09fbedc9052145b20677501a1dbcb5f5ba9c87d4", Ref: "main"}))
}
