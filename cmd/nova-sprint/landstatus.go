package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

type landPhase string

const (
	phaseFetch  landPhase = "fetch"
	phaseMerge  landPhase = "merge"
	phaseCheck  landPhase = "check"
	phaseQueue  landPhase = "queue"
	phasePush   landPhase = "push"
	phaseReport landPhase = "report"
)

type landStatusRecord struct {
	Stream    string    `json:"stream"`
	Phase     string    `json:"phase"`
	Cards     int       `json:"cards"`
	StartedAt time.Time `json:"started_at"`
}

func (l *lander) statusPath(repo string) string {
	if l.repoDir != "" {
		return l.repoDir + ".status"
	}
	root := l.root
	if root == "" {
		if r, err := defaultLandRoot(); err == nil {
			root = r
		} else {
			root = filepath.Join(os.TempDir(), "nova-sprint", "land")
		}
	}
	return filepath.Join(root, repoDirName(repo)+".status")
}

func (l *lander) printPhase(stream, phase string, cards int, stdout io.Writer) {
	if stdout == nil {
		stdout = io.Discard
	}
	t := time.Now().Format(time.RFC3339)
	if l != nil && l.a != nil && l.a.now != nil {
		t = l.a.now().Format(time.RFC3339)
	}
	line := fmt.Sprintf("LANDING stream=%s phase=%s cards=%d at=%s", stream, phase, cards, t)
	fmt.Fprintln(stdout, line)

	repo := ""
	if len(l.out) > 0 {
		repo = l.out[0].Repo
	}
	if repo == "" {
		repo = "default"
	}
	path := l.statusPath(repo)
	// ignored: status directory creation failure is non-fatal for landing progress
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	rec := landStatusRecord{
		Stream:    stream,
		Phase:     phase,
		Cards:     cards,
		StartedAt: time.Now(),
	}
	if data, err := json.Marshal(rec); err == nil {
		// ignored: status file write failure is non-fatal
		_ = os.WriteFile(path, data, 0o644)
	}
}

func (l *lander) clearStatus() {
	repo := "default"
	if len(l.out) > 0 && l.out[0].Repo != "" {
		repo = l.out[0].Repo
	}
	path := l.statusPath(repo)
	// ignored: status removal failure is non-fatal
	_ = os.Remove(path)
}

func (a *app) cmdLandStatus(args []string, stdout, stderr io.Writer) int {
	root, err := defaultLandRoot()
	if err != nil {
		fmt.Fprintln(stdout, "no land pass running")
		return 0
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		fmt.Fprintln(stdout, "no land pass running")
		return 0
	}
	found := false
	for _, entry := range entries {
		if !entry.IsDir() && filepath.Ext(entry.Name()) == ".status" {
			path := filepath.Join(root, entry.Name())
			data, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			var rec landStatusRecord
			if json.Unmarshal(data, &rec) == nil {
				if time.Since(rec.StartedAt) > 10*time.Minute {
					continue // stale
				}
				dur := time.Since(rec.StartedAt).Round(time.Millisecond)
				fmt.Fprintf(stdout, "LANDING PASS stream=%s phase=%s duration=%s\n", rec.Stream, rec.Phase, dur)
				found = true
			}
		}
	}
	if !found {
		fmt.Fprintln(stdout, "no land pass running")
	}
	return 0
}
