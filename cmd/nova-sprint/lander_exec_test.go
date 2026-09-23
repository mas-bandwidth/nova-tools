//go:build !windows

package main

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/land"
)

// script writes an executable sh program into dir.
func script(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestLanderProgramsSpeakTheProtocol: the production seams hand each program
// its arguments as the verb's doc says and read its answer back: the gate's
// exit and red line, the bisect's base= members= line, the lander's exit and
// the filer's issue number with the body on stdin.
func TestLanderProgramsSpeakTheProtocol(t *testing.T) {
	dir := t.TempDir()
	argv := filepath.Join(dir, "argv")
	ctx := context.Background()
	b := land.Batch{Repo: "nova-tools", Name: "b1", Members: []land.Member{{Number: 7, Head: "abc"}, {Number: 8, Head: "def"}}}
	p := landerPrograms{
		Gate:   script(t, dir, "gate", `echo "$@" > `+argv+`; echo noise; echo "BATCH FAIL step=test package=internal/x test=TestY"; exit 1`+"\n"),
		Bisect: script(t, dir, "bisect", `echo "base=green members=green,red"`+"\n"),
		Land:   script(t, dir, "land", `exit 3`+"\n"),
		File:   script(t, dir, "file", `cat > `+argv+`.body; echo "#4242"`+"\n"),
	}
	gate, bisect, lander, filer := landerSeams(p)

	v, err := gate.Run(ctx, b, 1)
	if err != nil || !reflect.DeepEqual(v, land.Verdict{Step: "test", Package: "internal/x", Test: "TestY"}) {
		t.Fatalf("gate verdict %+v err %v", v, err)
	}
	if raw, _ := os.ReadFile(argv); strings.TrimSpace(string(raw)) != "nova-tools b1 1 7@abc 8@def" {
		t.Fatalf("gate argv %q", raw)
	}
	base, members, err := bisect.Alone(ctx, b, v)
	if err != nil || !base || !reflect.DeepEqual(members, []bool{true, false}) {
		t.Fatalf("bisect base %t members %v err %v", base, members, err)
	}
	if err := lander.Land(ctx, b, land.Verdict{OK: true}); err == nil || !strings.Contains(err.Error(), "exited 3") {
		t.Fatalf("land err %v, want the exit named", err)
	}
	n, err := filer.File(ctx, "nova-tools", "flaky: x", "the body\n")
	if err != nil || n != 4242 {
		t.Fatalf("file %d err %v", n, err)
	}
	if raw, _ := os.ReadFile(argv + ".body"); string(raw) != "the body\n" {
		t.Fatalf("file stdin %q", raw)
	}
}
