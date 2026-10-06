package main

// landproc.go is the process side of the landing step: every child it starts
// (the gate's go, the check's sh, and git) runs in a process group of its own,
// registered while it runs, and the gate's group is also recorded under the
// land root, so a server exit ends the whole group and a later run ends a gate
// an earlier run left (docs/SPEC-SPRINT.md section 7, the tree gate). The group
// is the cause: `go test` starts the compiled test binary as a child of go, and
// killing go alone leaves the test binary running, the orphan of 2026-10-06
// (ci.test at 310% CPU under launchd, parent pid 1); the group carries it.

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// landGatesDir is where a land root records its running gate groups: one file
// a group, named by its process group id. "" keeps no record.
func landGatesDir(root string) string {
	if root == "" {
		return ""
	}
	return filepath.Join(root, "gates")
}

// landGates is the landing step's process state: the live groups by the gates
// directory they run under, and the directories the step has ended, which start
// no child again.
var landGates = struct {
	mu     sync.Mutex
	live   map[string]map[int]struct{}
	closed map[string]bool
}{live: map[string]map[int]struct{}{}, closed: map[string]bool{}}

// startLandChild makes cmd the leader of a process group of its own, starts it
// and records the group while it runs. gates is the directory the group is
// registered under ("" is unregistered); record also writes the group's record
// under gates, which a later run's start reads. It refuses once the landing
// step at gates has ended: a group is never started after its server's last
// exit, so no gate is left to run.
func startLandChild(cmd *exec.Cmd, gates string, record bool) (int, error) {
	ownLandGroup(cmd)
	landGates.mu.Lock()
	if landGates.closed[gates] {
		landGates.mu.Unlock()
		return 0, errors.New("the landing step has ended and starts no child")
	}
	if err := cmd.Start(); err != nil {
		landGates.mu.Unlock()
		return 0, err
	}
	pgid := cmd.Process.Pid
	groups := landGates.live[gates]
	if groups == nil {
		groups = map[int]struct{}{}
		landGates.live[gates] = groups
	}
	groups[pgid] = struct{}{}
	landGates.mu.Unlock()
	if record {
		recordLandGate(gates, pgid)
	}
	return pgid, nil
}

// stopLandChild forgets a group once its child has been waited for, and removes
// its record.
func stopLandChild(pgid int, gates string) {
	landGates.mu.Lock()
	delete(landGates.live[gates], pgid)
	landGates.mu.Unlock()
	if gates != "" {
		// ignored: a failed removal leaves a record the next start's scan reads as any other
		_ = os.Remove(filepath.Join(gates, strconv.Itoa(pgid)))
	}
}

// recordLandGate writes a group's record under gates, one line its id.
func recordLandGate(gates string, pgid int) {
	if gates == "" {
		return
	}
	// ignored: the group is ended by this process's own end, so a record not written leaves nothing to read
	_ = os.MkdirAll(gates, 0o755)
	// ignored: a record not written leaves the next run with nothing to read
	_ = os.WriteFile(filepath.Join(gates, strconv.Itoa(pgid)), []byte(strconv.Itoa(pgid)+"\n"), 0o644)
}

// landChildOutput starts cmd in a process group of its own, waits for it, and
// returns its combined output. record also records the group under gates.
func landChildOutput(cmd *exec.Cmd, gates string, record bool) ([]byte, error) {
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	pgid, err := startLandChild(cmd, gates, record)
	if err != nil {
		return nil, err
	}
	werr := cmd.Wait()
	stopLandChild(pgid, gates)
	return out.Bytes(), werr
}

// landChildStreams is landChildOutput for a caller that reads the two streams
// apart (git); the group is registered but not recorded.
func landChildStreams(cmd *exec.Cmd, gates string) (stdout, stderr []byte, err error) {
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	pgid, err := startLandChild(cmd, gates, false)
	if err != nil {
		return nil, nil, err
	}
	werr := cmd.Wait()
	stopLandChild(pgid, gates)
	return out.Bytes(), errb.Bytes(), werr
}

// endLandGroups ends every live group under gates and closes it: no child of
// the landing step starts there again.
func endLandGroups(gates string) {
	landGates.mu.Lock()
	groups := landGates.live[gates]
	delete(landGates.live, gates)
	landGates.closed[gates] = true
	landGates.mu.Unlock()
	for pgid := range groups {
		killLandGroup(pgid)
	}
}

// staleLandGates ends every gate group an earlier landing run recorded under
// gates, removes the records, and returns the groups it ended.
func staleLandGates(gates string) []int {
	if gates == "" {
		return nil
	}
	entries, err := os.ReadDir(gates)
	// ignored: no gates directory is no recorded gate
	if err != nil {
		return nil
	}
	var ended []int
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		pgid, err := strconv.Atoi(strings.TrimSpace(e.Name()))
		if err == nil && pgid > 0 && landGroupAlive(pgid) {
			killLandGroup(pgid)
			ended = append(ended, pgid)
		}
		// ignored: the record is spent, and one naming no live group is a leftover
		_ = os.Remove(filepath.Join(gates, e.Name()))
	}
	return ended
}

// endLandChildren ends every process group the landing step of this process
// started, under this app's land root: the server's exit, whatever ended it,
// takes the gate's group with it.
func (a *app) endLandChildren() {
	root, err := a.landRoot()
	// ignored: a land root that cannot be read keeps no gate record
	if err != nil {
		return
	}
	endLandGroups(landGatesDir(root))
}

// endStaleLandGates ends every gate an earlier landing run left under this
// app's land root and says so in one line.
func (a *app) endStaleLandGates(stdout io.Writer) {
	root, err := a.landRoot()
	// ignored: a land root that cannot be read keeps no gate record
	if err != nil {
		return
	}
	if ended := staleLandGates(landGatesDir(root)); len(ended) > 0 {
		fmt.Fprintf(stdout, "LANDING ended %d gate process group(s) an earlier landing run left under %s: %s\n", len(ended), root, landGateList(ended))
	}
}

// landGateList is a group's ids as a line says them.
func landGateList(pgids []int) string {
	parts := make([]string, 0, len(pgids))
	for _, pgid := range pgids {
		parts = append(parts, "pgid="+strconv.Itoa(pgid))
	}
	return strings.Join(parts, ",")
}
