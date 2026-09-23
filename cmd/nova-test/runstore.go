package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Run is one validation run as the run store holds it: one <run>.json file
// per run directly under the store directory. Stamps are RFC 3339; an absent
// stamp is an interval that has not happened.
type Run struct {
	ID              string
	State           string
	Queued          time.Time
	Started         time.Time
	Finished        time.Time
	CancelRequested time.Time
	Cancelled       time.Time
	Attempts        []Attempt
}

// Attempt is one attempt of a run, kept in order: a retried run keeps every
// attempt identity and the failure of each one before the last.
type Attempt struct {
	ID      string `json:"id"`
	State   string `json:"state"`
	Failure string `json:"failure,omitempty"`
}

type runFile struct {
	ID              string    `json:"id"`
	State           string    `json:"state"`
	QueuedAt        string    `json:"queued_at"`
	StartedAt       string    `json:"started_at"`
	FinishedAt      string    `json:"finished_at"`
	CancelRequested string    `json:"cancel_requested_at"`
	CancelledAt     string    `json:"cancelled_at"`
	Attempts        []Attempt `json:"attempts"`
}

// loadRuns reads every run in the store. A run that does not parse is an
// error naming its file, never a skip: a survey that dropped it would report
// a healthier queue than the one there is.
func loadRuns(dir string) ([]Run, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("run store: %v", err)
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	runs := make([]Run, 0, len(names))
	for _, name := range names {
		r, err := loadRun(filepath.Join(dir, name))
		if err != nil {
			return nil, fmt.Errorf("run %s: %v", name, err)
		}
		runs = append(runs, r)
	}
	return runs, nil
}

func loadRun(path string) (Run, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Run{}, err
	}
	var f runFile
	if err := json.Unmarshal(raw, &f); err != nil {
		return Run{}, err
	}
	if f.ID == "" {
		return Run{}, fmt.Errorf("no id")
	}
	r := Run{ID: f.ID, State: f.State, Attempts: f.Attempts}
	if r.State == "" {
		r.State = "-"
	}
	for _, s := range []struct {
		name, value string
		into        *time.Time
		required    bool
	}{
		{"queued_at", f.QueuedAt, &r.Queued, true},
		{"started_at", f.StartedAt, &r.Started, false},
		{"finished_at", f.FinishedAt, &r.Finished, false},
		{"cancel_requested_at", f.CancelRequested, &r.CancelRequested, false},
		{"cancelled_at", f.CancelledAt, &r.Cancelled, false},
	} {
		if s.value == "" {
			if s.required {
				return Run{}, fmt.Errorf("no %s; a run without it cannot be placed in a window", s.name)
			}
			continue
		}
		t, err := instant(s.value)
		if err != nil {
			return Run{}, fmt.Errorf("%s: %v", s.name, err)
		}
		*s.into = t
	}
	return r, nil
}
