package main

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestFleetHygieneInstallWritesScriptAndTimer is #1139's install half: the
// bench timer that deletes read job dirs and trims caches, as a verb every
// bench installs. A fake sudo and a fake systemctl stand in for root and
// systemd, so no test needs either. The verb writes the hygiene script and
// the ten-minute timer once, and a second --install rewrites neither.
func TestFleetHygieneInstallWritesScriptAndTimer(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the install writes a shell script and a systemd timer; unix-only here")
	}
	bin := t.TempDir()
	writeBenchExe(t, filepath.Join(bin, "sudo"), "#!/bin/sh\n"+
		"while [ $# -gt 0 ]; do case \"$1\" in -*) shift;; *) break;; esac; done\nexec \"$@\"\n")
	writeBenchExe(t, filepath.Join(bin, "systemctl"), "#!/bin/sh\nexit 0\n")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	root := t.TempDir()
	var out, errb bytes.Buffer
	if code := run([]string{"fleet", "hygiene", "--install", "--root", root}, &out, &errb, time.Now().UTC()); code != 0 {
		t.Fatalf("fleet hygiene --install exit = %d, want 0\nstdout:%s\nstderr:%s", code, out.String(), errb.String())
	}
	script := filepath.Join(root, "nova-bench", "bench-hygiene.sh")
	timer := filepath.Join(root, "bench-hygiene.timer")
	scriptRaw, err := os.ReadFile(script)
	if err != nil {
		t.Fatalf("the install wrote no script at %s: %v\nstdout:%s\nstderr:%s", script, err, out.String(), errb.String())
	}
	if !strings.Contains(string(scriptRaw), "nova-pulse hygiene run") {
		t.Errorf("the installed script does not run the hygiene verb:\n%s", scriptRaw)
	}
	timerRaw, err := os.ReadFile(timer)
	if err != nil {
		t.Fatalf("the install wrote no timer at %s: %v", timer, err)
	}
	if !strings.Contains(string(timerRaw), "OnUnitActiveSec=10min") {
		t.Errorf("the installed timer is not the ten-minute timer:\n%s", timerRaw)
	}

	// A second --install rewrites nothing that already matches.
	out.Reset()
	errb.Reset()
	if code := run([]string{"fleet", "hygiene", "--install", "--root", root}, &out, &errb, time.Now().UTC()); code != 0 {
		t.Fatalf("second install exit = %d, want 0", code)
	}
	scriptRaw2, _ := os.ReadFile(script)
	timerRaw2, _ := os.ReadFile(timer)
	if string(scriptRaw2) != string(scriptRaw) || string(timerRaw2) != string(timerRaw) {
		t.Error("the second install rewrote a file that already matched; it must be idempotent")
	}
}
