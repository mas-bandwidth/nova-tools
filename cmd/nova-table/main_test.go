package main

import (
	"bytes"
	"strings"
	"testing"
)

// runTable invokes the binary's own entry point.
func runTable(args ...string) (int, string, string) {
	var stdout, stderr bytes.Buffer
	code := run(args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

// at appends --redis <addr> to a command line.
func at(addr string, args ...string) []string { return append(args, "--redis", addr) }

func TestBareCommandNamesTheDoor(t *testing.T) {
	t.Parallel()

	code, stdout, stderr := runTable()
	if code != 2 || stdout != "" || stderr != "nova-table: no verb; available: help, create, set, drop, list, row, col, cell, member, check, clear, show, render, watch, view, shell, version; run: nova-table help\n" {
		t.Fatalf("bare: exit %d stdout %q stderr %q", code, stdout, stderr)
	}
	code, _, stderr = runTable("bogus")
	if code != 2 || !strings.HasPrefix(stderr, "nova-table: unknown verb bogus;") || !strings.HasSuffix(stderr, "; run: nova-table help\n") {
		t.Fatalf("unknown verb: exit %d stderr %q", code, stderr)
	}
	code, stdout, _ = runTable("help")
	if code != 0 || !strings.HasPrefix(stdout, "nova-table: ") || !strings.Contains(stdout, "\nexample:\n") {
		t.Fatalf("help: exit %d\n%s", code, stdout)
	}
	code, stdout, _ = runTable("version")
	if code != 0 || !strings.HasPrefix(stdout, "nova-table ") {
		t.Fatalf("version: exit %d %q", code, stdout)
	}
}
