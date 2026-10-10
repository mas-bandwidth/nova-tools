package main

// landstatus.go is the live progress of a land pass (docs/SPEC-SPRINT.md,
// section 7, land-live-progress-bb.w1~15.g5). A pass that ran 25 to 40 minutes
// with no line until its end left the coordinator unable to tell a slow pass
// from a stuck one, so the lander prints one LANDING line as each stream starts
// a phase and records the phase it is in beside the repository's cache clone
// under the land root. `nova-sprint land --status` reads the record back and
// says the current pass, its phase and how long the phase has run, or that no
// land pass is running; a status file left by a crash names a dead pid and is
// read as no pass.

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// landPhase is one of the phases a land pass prints and records, the coarse
// steps a pass spends its time in, in order.
type landPhase string

const (
	phaseFetch  landPhase = "fetch"
	phaseMerge  landPhase = "merge"
	phaseCheck  landPhase = "check"
	phaseQueue  landPhase = "queue"
	phasePush   landPhase = "push"
	phaseReport landPhase = "report"
)

// landPhaseOf maps the lander's stage names onto the printed phases: the six
// stages a pass prints. Every other stage a lander names (a gate, a bench lane)
// is finer than the phase it stands in and prints nothing of its own.
func landPhaseOf(stage string) (landPhase, bool) {
	switch p := landPhase(stage); p {
	case phaseFetch, phaseMerge, phaseCheck, phaseQueue, phasePush, phaseReport:
		return p, true
	}
	return "", false
}

// landStatusRecord is the current phase of one land pass, kept in a status file
// under the land root while the pass runs. Since is when the phase began and
// PID the pass's process, so `land --status` says how long the phase has run
// and whether a file left by a crash still names a live pass.
type landStatusRecord struct {
	Stream string    `json:"stream"`
	Phase  string    `json:"phase"`
	Cards  int       `json:"cards"`
	Since  time.Time `json:"since"`
	PID    int       `json:"pid"`
}

// landStatusName is a repository's status file name under the land root, beside
// its cache clone (repoDirName). A batch naming no REPOSITORY uses "default".
func landStatusName(repo string) string {
	if repo == "" {
		return "default.status"
	}
	return repoDirName(repo) + ".status"
}

// statusRoot is where a pass keeps its status files: the lander's own root when
// it has one, else the app's land root (the server's and a hand land's alike),
// so `land --status` finds a pass's file from the land root alone.
func (l *lander) statusRoot() string {
	if l != nil {
		if l.root != "" {
			return l.root
		}
		if l.a != nil {
			if root, err := l.a.landRoot(); err == nil && root != "" {
				return root
			}
		}
	}
	if root, err := defaultLandRoot(); err == nil {
		return root
	}
	return ""
}

// printPhase prints one LANDING line for a stream's phase and records it in the
// status file, both best-effort: a land pass is never stopped by a status file
// that cannot be written. The line goes to the pass's writer; the file stands
// beside the repository's cache clone under the land root.
func (l *lander) printPhase(p landPhase, stream string, cards int) {
	if l == nil {
		return
	}
	out := l.progress
	if out == nil {
		out = io.Discard
	}
	t := l.clock()
	fmt.Fprintf(out, "LANDING stream=%s phase=%s cards=%d at=%s\n", stream, p, cards, t.Format(time.RFC3339))
	root := l.statusRoot()
	if root == "" {
		return
	}
	rec := landStatusRecord{Stream: stream, Phase: string(p), Cards: cards, Since: t, PID: os.Getpid()}
	data, _ := json.Marshal(rec) // ignored: a record of strings, a count and a time always encodes
	// ignored: the phase's LANDING line is already printed; a status file that cannot be written only loses `land --status`
	if err := os.MkdirAll(root, 0o755); err != nil {
		return
	}
	// ignored: as above, the live pass goes on whether or not the status file is written
	_ = os.WriteFile(filepath.Join(root, landStatusName(l.repo)), data, 0o644)
}

// clearStatus removes a pass's status files as it ends: one per repository its
// batches name, plus the default one a batch naming none wrote. A file already
// gone is no error.
func (l *lander) clearStatus() {
	if l == nil {
		return
	}
	root := l.statusRoot()
	if root == "" {
		return
	}
	repos := map[string]bool{"": true}
	for _, b := range l.out {
		repos[b.Repo] = true
	}
	for repo := range repos {
		// ignored: a status file already gone (or never written) is what the clear wants
		_ = os.Remove(filepath.Join(root, landStatusName(repo)))
	}
}

// cmdLandStatus prints the land pass running now: one LANDING PASS line per
// status file naming a live process, its stream, phase, cards and how long the
// phase has run; "no land pass running" when none does. With the server running
// --land the file is the server's, in the same land root.
func (a *app) cmdLandStatus(stdout io.Writer) int {
	root := ""
	if a != nil && a.landRoot != nil {
		if r, err := a.landRoot(); err == nil {
			root = r
		}
	}
	if root == "" {
		fmt.Fprintln(stdout, "no land pass running")
		return 0
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		fmt.Fprintln(stdout, "no land pass running")
		return 0
	}
	now := time.Now()
	if a != nil && a.now != nil {
		now = a.now()
	}
	found := false
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".status" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, entry.Name()))
		if err != nil {
			continue
		}
		var rec landStatusRecord
		if json.Unmarshal(data, &rec) != nil || rec.Phase == "" {
			continue
		}
		if rec.PID > 0 && !demoProcessAlive(rec.PID) {
			continue // a pass that ended without clearing its file: not running
		}
		dur := now.Sub(rec.Since)
		if dur < 0 {
			dur = 0
		}
		fmt.Fprintf(stdout, "LANDING PASS stream=%s phase=%s cards=%d since=%s duration=%s\n",
			rec.Stream, rec.Phase, rec.Cards, rec.Since.UTC().Format(time.RFC3339), dur.Round(time.Second))
		found = true
	}
	if !found {
		fmt.Fprintln(stdout, "no land pass running")
	}
	return 0
}
