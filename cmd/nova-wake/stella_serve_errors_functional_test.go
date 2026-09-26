//go:build functional

package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/dispatch"
	"github.com/mas-bandwidth/nova-tools/internal/wake"
)

// Each fixture uses a private subprocess so the package's fake-PATH helpers
// cannot affect other parallel tests. No real bus, receiver, or store is used.
func TestStellaServeFailureGuidance(t *testing.T) {
	if mode := os.Getenv("STELLA_SERVE_AUDIT_CHILD"); mode != "" {
		stellaServeFailureCase(t, mode)
		return
	}
	t.Parallel()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"receipt", "durability", "diagnostic", "result-save", "log-open"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			cmd := exec.Command(exe, "-test.run=^TestStellaServeFailureGuidance$", "-test.count=1")
			cmd.Env = append(os.Environ(), "STELLA_SERVE_AUDIT_CHILD="+mode)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("isolated %s: %v\n%s", mode, err, out)
			}
		})
	}
}

func stellaServeFailureCase(t *testing.T, mode string) {
	ctx := context.Background()
	busDir, _ := fakes(t)
	statePath := filepath.Join(t.TempDir(), "state")
	st, err := wake.Load(statePath)
	if err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	s := &server{stdout: &out, stderr: &errOut, clock: wake.NewFake(at), st: st, statePath: statePath,
		bus: t.TempDir(), as: "Stella", remote: "origin", branch: "main", timeout: time.Second,
		ledger: dispatch.New(st, "serve:"), inOrder: map[string]bool{}}
	switch mode {
	case "receipt":
		write(t, filepath.Join(busDir, "out"), "RECEIPT FAIL note-a: fixture push rejected\n")
		write(t, filepath.Join(busDir, "exit"), "23")
		s.sendReceipts(ctx, []string{"note-a"})
		got := out.String() + errOut.String()
		if !strings.Contains(got, "note-a") || !strings.Contains(got, "fixture push rejected") || !strings.Contains(got, "23") {
			t.Fatalf("receipt failure disappeared: stdout=%q stderr=%q", out.String(), errOut.String())
		}
	case "diagnostic":
		write(t, filepath.Join(busDir, "out"), "fixture handler configuration missing\n")
		write(t, filepath.Join(busDir, "exit"), "7")
		s.onNote = install(t, t.TempDir(), "nova-bus")
		s.ledger.Queue("note-a", wake.Stamp(at))
		s.runBatch(ctx, []string{"note-a"}, 1, false)
		got := out.String() + errOut.String()
		if !strings.Contains(got, "fixture handler configuration missing") && !strings.Contains(got, "log=") {
			t.Fatalf("handler rc is visible but cause and log are lost: %s", got)
		}
		logs, err := filepath.Glob(statePath + ".logs/dispatch-*.log")
		if err != nil || len(logs) != 1 {
			t.Fatalf("logs=%v %v", logs, err)
		}
		raw, err := os.ReadFile(logs[0])
		if err != nil || !bytes.Contains(raw, []byte("fixture handler configuration missing")) || !bytes.Contains(raw, []byte("note-a")) {
			t.Fatalf("diagnostics not retained: %q %v", raw, err)
		}
	case "durability":
		command, recordDir := fakeNote(t)
		s.onNote = command
		s.ledger.Queue("note-a", wake.Stamp(at))
		if err := st.Save(statePath); err != nil {
			t.Fatal(err)
		}
		// A directory at the deterministic scratch path rejects writes while the
		// previous valid queued record stays readable across a real reload.
		if err := os.Mkdir(wake.TempName(statePath), 0700); err != nil {
			t.Fatal(err)
		}
		s.runBatch(ctx, []string{"note-a"}, 1, false)
		disk, err := wake.Load(statePath)
		if err != nil {
			t.Fatal(err)
		}
		if dispatch.New(disk, "serve:").StateOf("note-a") != dispatch.Queued {
			t.Fatal("fixture did not retain queued state")
		}
		// A fresh server, not the same in-memory stop flag.
		s.broken = false
		s.st, s.ledger = disk, dispatch.New(disk, "serve:")
		s.runBatch(ctx, []string{"note-a"}, 1, false)
		invocations := calls(t, recordDir)
		if len(invocations) != 0 {
			t.Fatalf("dispatch ran despite failed durable intent, including after reload: calls=%v output=%s errors=%s", invocations, out.String(), errOut.String())
		}
	case "result-save":
		command, recordDir := fakeNote(t)
		s.onNote, s.receipt = command, true
		s.ledger.Queue("note-a", wake.Stamp(at))
		write(t, filepath.Join(recordDir, "block"), "hold")
		done := make(chan struct{})
		go func() { s.runBatch(ctx, []string{"note-a"}, 1, false); close(done) }()
		t.Cleanup(func() { _ = os.Remove(filepath.Join(recordDir, "block")); <-done })
		deadline := time.Now().Add(5 * time.Second)
		for {
			if _, err := os.Stat(filepath.Join(recordDir, "calls")); err == nil {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("handler never started")
			}
			time.Sleep(time.Millisecond)
		}
		if err := os.Mkdir(wake.TempName(statePath), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(filepath.Join(recordDir, "block")); err != nil {
			t.Fatal(err)
		}
		<-done
		disk, err := wake.Load(statePath)
		if err != nil {
			t.Fatal(err)
		}
		if dispatch.New(disk, "serve:").StateOf("note-a") != dispatch.Dispatching {
			t.Fatal("durable intent lost")
		}
		if !s.broken || !strings.Contains(out.String(), "receipt withheld") || strings.Contains(out.String(), "WAKE FIRED") || len(calls(t, busDir)) != 0 {
			t.Fatalf("uncertain result advertised accepted or receipted: broken=%v output=%q errors=%q", s.broken, out.String(), errOut.String())
		}
	case "log-open":
		command, recordDir := fakeNote(t)
		s.onNote = command
		write(t, statePath+".logs", "not a directory")
		s.ledger.Queue("note-a", wake.Stamp(at))
		s.runBatch(ctx, []string{"note-a"}, 1, false)
		if len(calls(t, recordDir)) != 0 || !strings.Contains(errOut.String(), "HANDLER NOTSTARTED") || !strings.Contains(errOut.String(), "note-a") {
			t.Fatalf("log refusal lost: %q %q", out.String(), errOut.String())
		}
	default:
		t.Fatal(fmt.Sprintf("unknown fixture %s", mode))
	}
}
