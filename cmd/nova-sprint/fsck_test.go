package main

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

func TestFsckCmdSeatAgreementClean(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --coordinator rowan --owner glenn")

	reset := setFsckConfigCoordinator(func(ctx context.Context, pg string) (string, error) {
		return "rowan", nil
	})
	defer reset()

	ctx := context.Background()
	st, err := ta.a.store(common{redis: "mem:0", actor: "coordinator"})
	require.NoError(t, err)
	require.NoError(t, st.SetServerActor(ctx, "rowan"))

	var stdout, stderr bytes.Buffer
	code := ta.a.cmdFsck(nil, &stdout, &stderr)
	assert.Equal(t, 0, code, "clean must exit 0: %s %s", stdout.String(), stderr.String())
	assert.Contains(t, stdout.String(), "FSCK seat-agreement clean")
	assert.Contains(t, stdout.String(), "key=rowan")
	assert.Contains(t, stdout.String(), "record=rowan")
	assert.Contains(t, stdout.String(), "server=rowan")
	assert.Contains(t, stdout.String(), "config=rowan")
	assert.Empty(t, stderr.String())

	// JSON mode
	stdout.Reset()
	stderr.Reset()
	code = ta.a.cmdFsck([]string{"--json"}, &stdout, &stderr)
	assert.Equal(t, 0, code)
	var finding sprint.FsckFinding
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &finding))
	assert.True(t, finding.Clean)
	assert.Equal(t, "seat-agreement", finding.Check)
	assert.Equal(t, "rowan", finding.Key)
	assert.Equal(t, "rowan", finding.Record)
	assert.Equal(t, "rowan", finding.Server)
	assert.Equal(t, "rowan", finding.Config)
}

func TestFsckCmdSeatAgreementDriftFixture(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --coordinator stella --owner glenn")

	// Handover to rowan, approved by glenn
	ctx := context.Background()
	st, err := ta.a.store(common{redis: "mem:0", actor: "coordinator"})
	require.NoError(t, err)

	_, err = st.Run(ctx, store.SeatStep(sprint.SeatReq{
		To:         "rowan",
		Who:        "rowan",
		Reason:     "10-03 handover",
		Take:       true,
		ApprovedBy: "glenn",
		Owner:      "glenn",
	}))
	require.NoError(t, err)

	// Fixture from BRIEF.md:
	// key is stella, record is rowan, server is stella, config is stella
	require.NoError(t, st.B.SetCoordinator(ctx, "stella"))
	require.NoError(t, st.SetServerActor(ctx, "stella"))

	reset := setFsckConfigCoordinator(func(ctx context.Context, pg string) (string, error) {
		return "stella", nil
	})
	defer reset()

	var stdout, stderr bytes.Buffer
	code := ta.a.cmdFsck([]string{"--repair"}, &stdout, &stderr)
	assert.Equal(t, 1, code, "drift must exit 1: %s %s", stdout.String(), stderr.String())
	assert.Contains(t, stdout.String(), "FSCK seat-agreement drift")
	assert.Contains(t, stdout.String(), "key=stella")
	assert.Contains(t, stdout.String(), "record=rowan")
	assert.Contains(t, stdout.String(), "server=stella")
	assert.Contains(t, stdout.String(), "config=stella")
	assert.Contains(t, stdout.String(), "fix=")
	assert.Contains(t, stdout.String(), "seat --repair")
	assert.Contains(t, stdout.String(), "nova-config sprint set --coordinator rowan")
	assert.Contains(t, stderr.String(), "remedy=")
	assert.Contains(t, stderr.String(), "seat --repair")
	assert.Contains(t, stderr.String(), "nova-config sprint set --coordinator rowan")

	// JSON mode
	stdout.Reset()
	stderr.Reset()
	code = ta.a.cmdFsck([]string{"--json"}, &stdout, &stderr)
	assert.Equal(t, 1, code)
	var finding sprint.FsckFinding
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &finding))
	assert.False(t, finding.Clean)
	assert.Equal(t, "seat-agreement", finding.Check)
	assert.Equal(t, "stella", finding.Key)
	assert.Equal(t, "rowan", finding.Record)
	assert.Equal(t, "stella", finding.Server)
	assert.Equal(t, "stella", finding.Config)
	assert.Contains(t, finding.Fix, "seat --repair")
	assert.Contains(t, finding.Fix, "nova-config sprint set --coordinator rowan")
}

func TestFsckCmdRefusesArgs(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	var stdout, stderr bytes.Buffer
	code := ta.a.cmdFsck([]string{"extra", "words"}, &stdout, &stderr)
	assert.Equal(t, 2, code)
	assert.Contains(t, stderr.String(), "takes no words")
}
