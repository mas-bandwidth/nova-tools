package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/cardcontract"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

func TestStageOKReportsTheMeasuredCommandPhases(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	writeStageOK(&out, "bench", swarm.StageResult{BaseRepo: "/repo", BaseSha: "0123456789abcdef",
		Wall: time.Second, Clone: 100 * time.Millisecond, Fetch: 200 * time.Millisecond, Checkout: 300 * time.Millisecond})
	assert.Equal(t, "STAGE OK bench=bench repo=/repo base=01234567 secs=1 clone=0.1 fetch=0.2 checkout=0.3\n", out.String())
}

func TestFrameTimingReportsOnlySuccessfulInstallation(t *testing.T) {
	t.Parallel()
	for _, success := range []bool{true, false} {
		name := "success"
		if !success {
			name = "refused"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			slot := t.TempDir()
			job := filepath.Join(slot, "jobs", "c")
			require.NoError(t, os.MkdirAll(job, 0o755))
			if !success {
				// A directory where the staged commit file belongs refuses the frame.
				require.NoError(t, os.Mkdir(filepath.Join(slot, cardcontract.StagedName), 0o755))
			}
			cfg := nativeRunConfig{slotDir: slot, model: "fake/model", frame: &cardcontract.Frame{Kind: "work", Card: "c"}}
			var out bytes.Buffer
			_, err := installFrameTimed(cfg, job, "0123456789abcdef0123456789abcdef01234567", &out)
			if !success {
				assert.Error(t, err)
				assert.Empty(t, out.String(), "a refused frame cannot claim readiness")
				return
			}
			require.NoError(t, err)
			assert.Regexp(t, `^FRAME OK secs=[0-9]+\.[0-9]\n$`, out.String())
			assert.FileExists(t, filepath.Join(job, cardcontract.JobName))
		})
	}
}
