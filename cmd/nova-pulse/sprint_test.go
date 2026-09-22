package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/pulse"
)

func TestSprintCLI_LifecycleAndStopVerification(t *testing.T) {
	dir := t.TempDir()
	sprintDir := filepath.Join(dir, "sprint")
	launchedDir := filepath.Join(dir, "launched")
	if err := os.MkdirAll(launchedDir, 0o755); err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 9, 21, 15, 0, 0, 0, time.UTC)

	// 1. Missing subcommand -> exit 2
	{
		var out, errb bytes.Buffer
		code := run([]string{"sprint"}, &out, &errb, now)
		if code != 2 {
			t.Fatalf("expected exit 2 on empty sprint subcommand, got %d", code)
		}
	}

	// 2. sprint prep
	{
		var out, errb bytes.Buffer
		code := run([]string{"sprint", "prep", "--dir", sprintDir}, &out, &errb, now)
		if code != 0 {
			t.Fatalf("sprint prep failed: %d, %s", code, errb.String())
		}
		if !strings.Contains(out.String(), "SPRINT PREP OK") {
			t.Fatalf("unexpected prep output: %s", out.String())
		}
		st, _ := pulse.ReadSprintState(sprintDir)
		if st != pulse.StatePrep {
			t.Fatalf("want state prep, got %s", st)
		}
	}

	// 3. sprint run
	{
		var out, errb bytes.Buffer
		code := run([]string{"sprint", "run", "--dir", sprintDir}, &out, &errb, now.Add(time.Minute))
		if code != 0 {
			t.Fatalf("sprint run failed: %d, %s", code, errb.String())
		}
		if !strings.Contains(out.String(), "SPRINT RUN OK") {
			t.Fatalf("unexpected run output: %s", out.String())
		}
		st, _ := pulse.ReadSprintState(sprintDir)
		if st != pulse.StateRun {
			t.Fatalf("want state run, got %s", st)
		}
		if _, err := os.Stat(filepath.Join(sprintDir, pulse.SprintStartFile)); err != nil {
			t.Fatalf("missing SPRINT-START file: %v", err)
		}
	}

	// 4. sprint drain (Rule 12: prevents launches via STOP file, waits for workers)
	{
		var out, errb bytes.Buffer
		code := run([]string{"sprint", "drain", "--dir", sprintDir, "--launched", launchedDir, "--timeout", "100ms"}, &out, &errb, now.Add(2*time.Minute))
		if code != 0 {
			t.Fatalf("sprint drain failed: %d, %s", code, errb.String())
		}
		if !strings.Contains(out.String(), "launches prevented") {
			t.Fatalf("drain output missing launches prevented: %s", out.String())
		}
		if !strings.Contains(out.String(), "SPRINT DRAIN OK: 0 active leases") {
			t.Fatalf("unexpected drain output: %s", out.String())
		}
		if _, err := os.Stat(filepath.Join(sprintDir, pulse.SprintStopFile)); err != nil {
			t.Fatalf("STOP file must exist after drain: %v", err)
		}
		st, _ := pulse.ReadSprintState(sprintDir)
		if st != pulse.StateDrain {
			t.Fatalf("want state drain, got %s", st)
		}
	}

	// 5. sprint stop with active leases lingering -> MUST refuse with exit 1
	{
		// Create an active card in launchedDir
		cardFile := filepath.Join(launchedDir, "card-099.md")
		if err := os.WriteFile(cardFile, []byte("active card\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		var out, errb bytes.Buffer
		code := run([]string{"sprint", "stop", "--dir", sprintDir, "--launched", launchedDir}, &out, &errb, now.Add(3*time.Minute))
		if code != 1 {
			t.Fatalf("expected exit 1 on stop with lingering leases, got %d (err: %s)", code, errb.String())
		}
		want := "SPRINT STOP WORKING: 1 active leases remain"
		if !strings.Contains(errb.String(), want) {
			t.Fatalf("want stderr %q, got %q", want, errb.String())
		}
		if _, err := os.Stat(filepath.Join(sprintDir, pulse.SprintEndFile)); !os.IsNotExist(err) {
			t.Fatalf("SPRINT-END must not exist when stop was refused")
		}

		// Clean up lingering card
		_ = os.Remove(cardFile)
	}

	// 6. sprint stop with zero active leases -> MUST stamp SPRINT-END and exit 0
	{
		var out, errb bytes.Buffer
		code := run([]string{"sprint", "stop", "--dir", sprintDir, "--launched", launchedDir}, &out, &errb, now.Add(4*time.Minute))
		if code != 0 {
			t.Fatalf("sprint stop failed: %d, %s", code, errb.String())
		}
		if !strings.Contains(out.String(), "SPRINT-END") {
			t.Fatalf("unexpected stop output: %s", out.String())
		}
		if _, err := os.Stat(filepath.Join(sprintDir, pulse.SprintEndFile)); err != nil {
			t.Fatalf("missing SPRINT-END file: %v", err)
		}
		st, _ := pulse.ReadSprintState(sprintDir)
		if st != pulse.StateStop {
			t.Fatalf("want state stop, got %s", st)
		}
	}

	// 7. sprint status
	{
		var out, errb bytes.Buffer
		code := run([]string{"sprint", "status", "--dir", sprintDir}, &out, &errb, now.Add(5*time.Minute))
		if code != 0 {
			t.Fatalf("sprint status failed: %d, %s", code, errb.String())
		}
		if !strings.Contains(out.String(), "state=stop") || !strings.Contains(out.String(), "active=0") {
			t.Fatalf("unexpected status output: %s", out.String())
		}
	}
}

func TestSprintCLI_PauseResume(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 9, 21, 15, 0, 0, 0, time.UTC)
	var out, errb bytes.Buffer

	// Prep -> Run
	if code := run([]string{"sprint", "prep", "--dir", dir}, &out, &errb, now); code != 0 {
		t.Fatalf("prep failed: %d, %s", code, errb.String())
	}
	out.Reset()
	errb.Reset()
	if code := run([]string{"sprint", "run", "--dir", dir}, &out, &errb, now); code != 0 {
		t.Fatalf("run failed: %d, %s", code, errb.String())
	}

	// Pause -> verify state=paused and STOP file exists
	out.Reset()
	errb.Reset()
	if code := run([]string{"sprint", "pause", "--dir", dir}, &out, &errb, now); code != 0 {
		t.Fatalf("pause failed: %d, %s", code, errb.String())
	}
	if !strings.Contains(out.String(), "state=paused") {
		t.Fatalf("unexpected pause output: %s", out.String())
	}
	st, _ := pulse.ReadSprintState(dir)
	if st != pulse.StatePaused {
		t.Fatalf("want state paused, got %s", st)
	}
	if _, err := os.Stat(filepath.Join(dir, pulse.SprintStopFile)); err != nil {
		t.Fatalf("STOP file must exist in paused state: %v", err)
	}

	// Resume -> verify state=running and STOP file is removed
	out.Reset()
	errb.Reset()
	if code := run([]string{"sprint", "resume", "--dir", dir}, &out, &errb, now); code != 0 {
		t.Fatalf("resume failed: %d, %s", code, errb.String())
	}
	if !strings.Contains(out.String(), "state=running") {
		t.Fatalf("unexpected resume output: %s", out.String())
	}
	st, _ = pulse.ReadSprintState(dir)
	if st != pulse.StateRun {
		t.Fatalf("want state run, got %s", st)
	}
	if _, err := os.Stat(filepath.Join(dir, pulse.SprintStopFile)); !os.IsNotExist(err) {
		t.Fatalf("STOP file must be removed after resume")
	}
}

func TestSprintCLI_ForceStop(t *testing.T) {
	dir := t.TempDir()
	sprintDir := filepath.Join(dir, "sprint")
	launchedDir := filepath.Join(dir, "launched")
	if err := os.MkdirAll(launchedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 21, 15, 0, 0, 0, time.UTC)

	_ = run([]string{"sprint", "prep", "--dir", sprintDir}, io.Discard, io.Discard, now)
	_ = run([]string{"sprint", "run", "--dir", sprintDir}, io.Discard, io.Discard, now)

	// Add lingering card
	cardFile := filepath.Join(launchedDir, "card-099.md")
	if err := os.WriteFile(cardFile, []byte("active\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var out, errb bytes.Buffer
	// Stop with --force succeeds despite lingering leases
	code := run([]string{"sprint", "stop", "--dir", sprintDir, "--launched", launchedDir, "--force"}, &out, &errb, now)
	if code != 0 {
		t.Fatalf("force stop failed: %d, %s", code, errb.String())
	}
	if !strings.Contains(out.String(), "SPRINT-END") {
		t.Fatalf("unexpected stop output: %s", out.String())
	}
	st, _ := pulse.ReadSprintState(sprintDir)
	if st != pulse.StateStop {
		t.Fatalf("want state stop after force stop, got %s", st)
	}
}

func TestSprintCLI_Refusals(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 9, 21, 15, 0, 0, 0, time.UTC)
	var out, errb bytes.Buffer

	// Stop from uninitialized/idle sprint -> exit 2
	code := run([]string{"sprint", "stop", "--dir", dir}, &out, &errb, now)
	if code != 2 {
		t.Fatalf("expected exit 2 on stop from idle, got %d", code)
	}
	if !strings.Contains(errb.String(), "SPRINT REFUSED") {
		t.Fatalf("unexpected stderr: %s", errb.String())
	}

	// Pause from idle sprint -> exit 2
	out.Reset()
	errb.Reset()
	code = run([]string{"sprint", "pause", "--dir", dir}, &out, &errb, now)
	if code != 2 {
		t.Fatalf("expected exit 2 on pause from idle, got %d", code)
	}

	// Resume from idle sprint -> exit 2
	out.Reset()
	errb.Reset()
	code = run([]string{"sprint", "resume", "--dir", dir}, &out, &errb, now)
	if code != 2 {
		t.Fatalf("expected exit 2 on resume from idle, got %d", code)
	}

	// Drain from idle sprint -> exit 2
	out.Reset()
	errb.Reset()
	code = run([]string{"sprint", "drain", "--dir", dir}, &out, &errb, now)
	if code != 2 {
		t.Fatalf("expected exit 2 on drain from idle, got %d", code)
	}
}

func TestSprintCLI_StopStrict_RefusesWhenRootsOmittedWithActiveLeases(t *testing.T) {
	dir := t.TempDir()
	launchedDir := filepath.Join(dir, "launched")
	if err := os.MkdirAll(launchedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 21, 15, 0, 0, 0, time.UTC)

	// Prep and run the sprint with --dir dir
	if code := run([]string{"sprint", "prep", "--dir", dir}, io.Discard, io.Discard, now); code != 0 {
		t.Fatalf("prep failed: code %d", code)
	}
	if code := run([]string{"sprint", "run", "--dir", dir}, io.Discard, io.Discard, now); code != 0 {
		t.Fatalf("run failed: code %d", code)
	}

	// Active card lease exists in <dir>/launched
	cardFile := filepath.Join(launchedDir, "card-101.md")
	if err := os.WriteFile(cardFile, []byte("active card lease\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Negative control test: sprint stop --strict without --roots (and without --launched)
	// MUST refuse exit 1 because the active card lease in <dir>/launched is detected by default.
	var out, errb bytes.Buffer
	code := run([]string{"sprint", "stop", "--dir", dir, "--strict"}, &out, &errb, now.Add(time.Minute))
	if code != 1 {
		t.Fatalf("expected exit 1 on strict stop when --roots is omitted with active card in <dir>/launched, got %d (err: %s)", code, errb.String())
	}
	want := "SPRINT STOP WORKING: 1 active leases remain"
	if !strings.Contains(errb.String(), want) {
		t.Fatalf("want stderr %q, got %q", want, errb.String())
	}
	if _, err := os.Stat(filepath.Join(dir, pulse.SprintEndFile)); !os.IsNotExist(err) {
		t.Fatalf("SPRINT-END must not exist when stop was refused")
	}

	// Also verify that even if an empty --roots is explicitly given, omitting --launched
	// still defaults to <dir>/launched and refuses exit 1.
	out.Reset()
	errb.Reset()
	emptyRootsDir := t.TempDir()
	code = run([]string{"sprint", "stop", "--dir", dir, "--strict", "--roots", emptyRootsDir}, &out, &errb, now.Add(time.Minute))
	if code != 1 {
		t.Fatalf("expected exit 1 on strict stop when --roots is given without --launched, got %d (err: %s)", code, errb.String())
	}
	if !strings.Contains(errb.String(), want) {
		t.Fatalf("want stderr %q, got %q", want, errb.String())
	}

	// Clean up card, then sprint stop without flags must succeed
	if err := os.Remove(cardFile); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	errb.Reset()
	code = run([]string{"sprint", "stop", "--dir", dir, "--strict"}, &out, &errb, now.Add(2*time.Minute))
	if code != 0 {
		t.Fatalf("strict stop should succeed after card removed, got %d (err: %s)", code, errb.String())
	}
	if !strings.Contains(out.String(), "SPRINT-END") {
		t.Fatalf("unexpected stop output: %s", out.String())
	}
	if _, err := os.Stat(filepath.Join(dir, pulse.SprintEndFile)); err != nil {
		t.Fatalf("missing SPRINT-END file after successful stop: %v", err)
	}
	st, _ := pulse.ReadSprintState(dir)
	if st != pulse.StateStop {
		t.Fatalf("want state stop, got %s", st)
	}
}
