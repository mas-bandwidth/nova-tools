package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/swarm"
)

// installTreeStepsCoverCards are the cards installTreeSteps' AllScript gate meets
// (docs/SPEC-SPRINT.md, a card is a tree of steps: the member runs an all-script
// card with no model, so native installs the executor). allScript is a tree card
// whose only work step is a script step; modelCard is a work step without SCRIPT:,
// a model step, against which the gate declines.
var installTreeStepsCoverCards = map[string]string{
	"all-script": "STEP 1. Rename.\n  PATHS: a.txt\n  COMMIT: step 1\n  VERDICT: ok\n  SCRIPT: regex\n  ```\n  s/Foo/Bar/\n  ```\n  POST: exit0 true\n",
	"model":      "STEP 1. Read.\n  COMMIT: step 1\n  VERDICT: ok\n",
}

// TestStepCover pins installTreeSteps (cmd/nova-swarm/step.go:232): the tree
// card's copy into the slot and the executor argv native runs in place of the
// harness for an all-script card, the decline for a non-script card, and the
// refusal when the slot cannot hold the tree card. It reaches the function with
// no process, no store and no wall: os.Executable supplies the executor path and
// the only writes are the tree card under slotDir.
func TestStepCover(t *testing.T) {
	t.Parallel()
	self, err := os.Executable()
	require.NoError(t, err, "os.Executable is the executor installTreeSteps returns")
	for name, c := range installTreeStepsCoverCards {
		c := c
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			slotDir := t.TempDir()
			jobDir := t.TempDir()
			argv, err := installTreeSteps([]byte(c), slotDir, jobDir)
			if name == "all-script" {
				require.NoError(t, err, "an all-script card installs the executor")
				want := []string{
					self,
					"step",
					"--card", filepath.Join(slotDir, treeCardName),
					"--dir", filepath.Join(jobDir, swarm.JobRepo),
					"--result", filepath.Join(jobDir, "RESULT.md"),
				}
				if assert.Len(t, argv, len(want), "the executor argv names the slot's tree card, the job's repo dir and its RESULT.md") {
					assert.Equal(t, want, argv)
				}
				b, err := os.ReadFile(filepath.Join(slotDir, treeCardName))
				require.NoError(t, err, "the tree card is written into the slot")
				assert.Equal(t, c, string(b), "the tree card is a verbatim copy of the card")
			} else {
				assert.Nil(t, argv, "a non-script card installs no executor: %v", argv)
				assert.NoError(t, err, "a non-script card declines, it does not error")
				_, statErr := os.Stat(filepath.Join(slotDir, treeCardName))
				assert.True(t, os.IsNotExist(statErr), "nothing is written for a non-script card")
			}
		})
	}
}

// TestStepCoverInstallTreeStepsRefusesAnUnwritableSlot makes the tree card's
// write fail by naming a slot under a missing directory: the all-script gate
// has already opened, so the function returns the write error and no argv.
func TestStepCoverInstallTreeStepsRefusesAnUnwritableSlot(t *testing.T) {
	t.Parallel()
	card := []byte(installTreeStepsCoverCards["all-script"])
	slotDir := filepath.Join(t.TempDir(), "missing", "slot")
	argv, err := installTreeSteps(card, slotDir, t.TempDir())
	assert.Nil(t, argv, "a write failure returns no argv: %v", argv)
	require.Error(t, err, "an unwritable slot refuses")
	assert.Contains(t, err.Error(), "no such file or directory", "the refusal names the failed write")
}
