package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSweepOrphansEmptyDir(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	var stdout, stderr bytes.Buffer
	code := cmdSweepOrphans([]string{"--pid-dir", tmp}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr: %s", code, stderr.String())
	}
	if got := stdout.String(); !strings.Contains(got, "nova-ci sweep-orphans: swept=0") {
		t.Fatalf("stdout = %q, want swept=0", got)
	}
}

func TestSweepOrphansCleansStaleFiles(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	pidFile := filepath.Join(tmp, "8888.pid")
	if err := os.WriteFile(pidFile, []byte("pid=9999999\nport=8888\nppid=9999998\n"), 0644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := cmdSweepOrphans([]string{"--pid-dir", tmp}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr: %s", code, stderr.String())
	}
	if got := stdout.String(); !strings.Contains(got, "nova-ci sweep-orphans: swept=1") {
		t.Fatalf("stdout = %q, want swept=1", got)
	}
	if _, err := os.Stat(pidFile); !os.IsNotExist(err) {
		t.Fatalf("stale pid file %s was not removed", pidFile)
	}
}

func TestSweepOrphansRefusals(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	code := cmdSweepOrphans([]string{"--badflag"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("exit = %d on bad flag, want 2", code)
	}

	stdout.Reset()
	stderr.Reset()
	code = cmdSweepOrphans([]string{"unexpected"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("exit = %d on unexpected arg, want 2", code)
	}
}

func TestSweepOrphansRunDispatches(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	var stdout, stderr bytes.Buffer
	code := run([]string{"sweep-orphans", "--pid-dir", tmp}, nil, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run sweep-orphans exit = %d, want 0", code)
	}
	if got := stdout.String(); !strings.Contains(got, "nova-ci sweep-orphans: swept=0") {
		t.Fatalf("run stdout = %q, want swept=0", got)
	}
}
