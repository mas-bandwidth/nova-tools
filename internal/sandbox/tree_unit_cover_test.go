//go:build darwin || linux

package sandbox

import (
	"errors"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSandboxTreeCoverRunawayLineZero(t *testing.T) {
	t.Parallel()
	result := RunawayLine(0)
	assert.Equal(t, "the process tree was killed", result)
}

func TestSandboxTreeCoverRunawayLineNegative(t *testing.T) {
	t.Parallel()
	result := RunawayLine(-1)
	assert.Equal(t, "the process tree was sent SIGKILL and could not be counted after", result)
}

func TestSandboxTreeCoverRunawayLinePositive(t *testing.T) {
	t.Parallel()
	result := RunawayLine(3)
	assert.Equal(t, "the process tree was sent SIGKILL and 3 of its processes are still running", result)
}

func TestSandboxTreeCoverLiveListErrs(t *testing.T) {
	t.Parallel()
	tr := &Tree{top: 100, keepTop: false, pgid: 50}
	tr.list = func() ([]procRow, error) {
		return nil, errors.New("list error")
	}
	members, ok := tr.live()
	assert.False(t, ok)
	assert.Nil(t, members)
}

func TestSandboxTreeCoverLiveWithRows(t *testing.T) {
	t.Parallel()
	self := os.Getpid()
	tr := &Tree{top: 100, keepTop: false, pgid: 50}
	tr.list = func() ([]procRow, error) {
		rows := []procRow{
			{pid: 100, ppid: 50, pgid: 50},   // command (top)
			{pid: 101, ppid: 100, pgid: 50},  // child
			{pid: self, ppid: 100, pgid: 50}, // test's own pid hung under command
			{pid: 1, ppid: 0, pgid: 50},      // pid 1
		}
		return rows, nil
	}
	members, ok := tr.live()
	assert.True(t, ok)
	assert.Len(t, members, 1)
	assert.Equal(t, 101, members[0].pid)
}

func TestSandboxTreeCoverKillAndAwaitNoMembers(t *testing.T) {
	t.Parallel()
	tr := &Tree{top: 100, keepTop: false, pgid: 50}
	tr.list = func() ([]procRow, error) {
		rows := []procRow{
			{pid: 100, ppid: 50, pgid: 50}, // top (not a member when keepTop is false)
			{pid: 1, ppid: 0, pgid: 50},    // pid 1
		}
		return rows, nil
	}
	result := tr.KillAndAwait()
	assert.Equal(t, 0, result)
}

func TestSandboxTreeCoverKillAndAwaitSelfOnly(t *testing.T) {
	t.Parallel()
	self := os.Getpid()
	tr := &Tree{top: 100, keepTop: false, pgid: 50}
	tr.list = func() ([]procRow, error) {
		rows := []procRow{
			{pid: self, ppid: 100, pgid: 50}, // test's own pid under top
			{pid: 1, ppid: 0, pgid: 50},      // pid 1
		}
		return rows, nil
	}
	result := tr.KillAndAwait()
	assert.Equal(t, 0, result)
}
