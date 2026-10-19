//go:build !windows

package main

import (
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/cardcontract"
)

// TestCardWorkNiceIsOneConstantAboveTheGates pins the one level card work runs at: a level
// above the lander's gate, which runs at nice 0, held once and used by the gate line the
// member hands a card and by the group native lowers behind the darwin wall. The coordinator
// note's 10 is an approximation; the paths already ask for 19.
func TestCardWorkNiceIsOneConstantAboveTheGates(t *testing.T) {
	t.Parallel()
	assert.GreaterOrEqual(t, cardWorkNice, 10, "card work sits at least ten steps behind the lander's gate")
	assert.Greater(t, cardWorkNice, 0, "card work is strictly below the lander's gate, which runs at nice 0")

	level := strconv.Itoa(cardWorkNice)

	// THE GATE LINE THE MEMBER HANDS A READ. The contract writes the read's gate as commands;
	// the first one is the vet, and it begins with the one level.
	read := cardcontract.Frame{Kind: "read", Card: "c1.r1.x", Attempt: 1, Repo: "https://github.com/mas-bandwidth/nova-tools.git", ReviewBase: "sprint/v17", Branch: "sprint/c1"}
	readStaged := cardcontract.Staged{
		Job: "/j", Repo: "/j/repo", Head: strings.Repeat("a", 40), Start: strings.Repeat("b", 40),
		Gate: &cardcontract.Gate{Packages: []string{"./internal/sprint"}},
	}
	gate := ""
	for _, line := range strings.Split(cardcontract.For("plain").JobText(read, readStaged), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "nice -n ") {
			gate = strings.TrimSpace(line)
			break
		}
	}
	require.NotEmpty(t, gate, "the member hands the card a gate line")
	assert.True(t, strings.HasPrefix(gate, "nice -n "+level+" "), "the gate line %q begins with nice -n %s", gate, level)

	// AND THE WORK CARD'S OWN TEST INSTRUCTION names the same level.
	work := cardcontract.Frame{Kind: "work", Card: "c1", Attempt: 1, Repo: "https://github.com/mas-bandwidth/nova-tools.git", BaseRef: "sprint/v17", Branch: "sprint/c1"}
	workStaged := cardcontract.Staged{Job: "/j", Repo: "/j/repo", Head: strings.Repeat("a", 40)}
	assert.Contains(t, cardcontract.For("plain").JobText(work, workStaged), "`nice -n "+level+"`",
		"the work card's test instruction names the one card-work level")

	// LOWERCHILDPRIORITY'S TARGET, read the way TestLowerChildPriorityPutsTheGroupAtNineteen
	// reads it: one child that waits on its stdin, its group lowered, then its nice read.
	cmd := exec.Command("/bin/sh", "-c", "read x")
	ownChildGroup(cmd)
	stdin, err := cmd.StdinPipe()
	require.NoError(t, err)
	require.NoError(t, cmd.Start())
	defer func() {
		_ = stdin.Close() // ignored: the child reads EOF and exits; its exit is not under test
		_ = cmd.Wait()    // ignored: as above
	}()
	require.NoError(t, lowerChildPriority(cmd.Process.Pid))
	raw, err := syscall.Getpriority(syscall.PRIO_PROCESS, cmd.Process.Pid)
	require.NoError(t, err)
	nice := raw
	if runtime.GOOS == "linux" {
		nice = 20 - raw // the raw syscall answers 20-nice on linux
	}
	assert.Equal(t, cardWorkNice, nice, "lowerChildPriority puts the group at cardWorkNice")
}
