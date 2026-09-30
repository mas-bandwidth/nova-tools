package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestAConformingBenchIsStandardOK(t *testing.T) {
	t.Parallel()
	b := conformingBench(t)
	code, output := b.standard()
	if code != 0 || len(drifts(output)) != 0 {
		t.Fatalf("exit %d:\n%s", code, output)
	}
	want := "STANDARD OK go=go1.26.5 bins=" + benchWant + " harness=ok seats=1 free=400G\n"
	if output != want {
		t.Errorf("output %q, want %q", output, want)
	}
}

// The witness is not a noop: an empty bench drifts, exits 1, and is never told
// it conforms.
func TestAnEmptyBenchDriftsAndIsNeverStandardOK(t *testing.T) {
	t.Parallel()
	b := emptyBench(t)
	code, output := b.standard()
	if code != 1 {
		t.Errorf("exit %d, want 1", code)
	}
	if len(drifts(output)) == 0 {
		t.Errorf("an empty bench drew no DRIFT line:\n%s", output)
	}
	if strings.Contains(output, "STANDARD OK") {
		t.Errorf("an empty bench was given a clean bill of health:\n%s", output)
	}
	if !strings.HasSuffix(output, "STANDARD DRIFT (see lines above)\n") {
		t.Errorf("the verdict is not the last line:\n%s", output)
	}
}

func TestEveryFindingIsOneDriftLineBeforeTheVerdict(t *testing.T) {
	t.Parallel()
	b := emptyBench(t)
	_, output := b.standard()
	lines := strings.Split(strings.TrimRight(output, "\n"), "\n")
	for i, l := range lines[:len(lines)-1] {
		if !strings.HasPrefix(l, "DRIFT ") {
			t.Errorf("line %d is not a DRIFT line: %q", i+1, l)
		}
	}
}

func TestHelpNamesApplyAsKillingStraysAndNothingMore(t *testing.T) {
	t.Parallel()
	b := conformingBench(t)
	for _, flag := range []string{"--help", "-h"} {
		code, output := b.standard(flag)
		if code != 0 {
			t.Errorf("%s: exit %d", flag, code)
		}
		low := strings.ToLower(output)
		if !strings.Contains(low, "--apply") || !strings.Contains(low, "kills stray") || !strings.Contains(low, "nothing more") {
			t.Errorf("%s does not say --apply kills strays and nothing more:\n%s", flag, output)
		}
	}
	if len(b.h.ran) != 0 {
		t.Errorf("--help started processes: %v", b.h.ran)
	}
}

func TestAnUnknownArgumentIsRefusedLoudly(t *testing.T) {
	t.Parallel()
	b := conformingBench(t)
	code, output := b.standard("--no-such-flag")
	if code != 1 || !strings.Contains(output, "DRIFT unknown argument --no-such-flag\n") || !strings.Contains(output, "STANDARD DRIFT") {
		t.Errorf("exit %d:\n%s", code, output)
	}
	// Even next to a valid one, and before any check has run.
	b2 := conformingBench(t)
	code, output = b2.standard("--apply", "--reboot-the-host")
	if code != 1 || !strings.Contains(output, "DRIFT unknown argument --reboot-the-host") {
		t.Errorf("exit %d:\n%s", code, output)
	}
	if len(b2.h.ran) != 0 || len(b2.h.killed) != 0 {
		t.Errorf("a refused argument still ran things: ran=%v killed=%v", b2.h.ran, b2.h.killed)
	}
}

// Nothing but the bench's own scratch directories is created or removed, and
// nothing is killed, on a run without --apply.
func TestAPlainRunMutatesNothingButItsOwnScratchDirectories(t *testing.T) {
	t.Parallel()
	b := conformingBench(t)
	if code, output := b.standard(); code != 0 {
		t.Fatalf("exit %d:\n%s", code, output)
	}
	if len(b.h.killed) != 0 {
		t.Errorf("a run without --apply killed %v", b.h.killed)
	}
	if len(b.h.made) != 2 {
		t.Errorf("made %v, want the canary and probe directories", b.h.made)
	}
	if !reflect.DeepEqual(b.h.made, b.h.removed) {
		t.Errorf("made %v but removed %v: a scratch directory was left or another path was removed", b.h.made, b.h.removed)
	}
	for _, d := range b.h.made {
		if !strings.HasPrefix(d, filepath.Join(b.home, "nova-bench")+string(filepath.Separator)) {
			t.Errorf("scratch directory %s is outside nova-bench", d)
		}
		if _, err := os.Stat(d); err == nil {
			t.Errorf("scratch directory %s still exists", d)
		}
	}
}

// go.mod's go line is the wanted go, never a default copied into the tool.
func TestTheWantedGoTracksGoMod(t *testing.T) {
	t.Parallel()
	b := conformingBench(t)
	b.unsetEnv("NOVA_GO")
	tree := t.TempDir()
	if err := os.WriteFile(filepath.Join(tree, "go.mod"), []byte("module example\n\ngo 1.99.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var buf strings.Builder
	code := run(nil, env{stdout: &buf, h: b.h, exeDir: filepath.Join(tree, "bin"), cwd: t.TempDir(), systemDir: b.t.TempDir()})
	if code != 1 || !strings.Contains(buf.String(), "DRIFT go version [go version go1.26.5 linux/amd64] want go1.99.0\n") {
		t.Errorf("exit %d:\n%s", code, buf.String())
	}
	// The same from the working directory, found above a subdirectory.
	sub := filepath.Join(tree, "tools", "x")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	buf.Reset()
	run(nil, env{stdout: &buf, h: b.h, exeDir: t.TempDir(), cwd: sub, systemDir: b.t.TempDir()})
	if !strings.Contains(buf.String(), "want go1.99.0") {
		t.Errorf("the go.mod above the working directory was not read:\n%s", buf.String())
	}
}

func TestNovaGoOverridesGoMod(t *testing.T) {
	t.Parallel()
	b := conformingBench(t)
	tree := t.TempDir()
	if err := os.WriteFile(filepath.Join(tree, "go.mod"), []byte("module example\n\ngo 1.99.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var buf strings.Builder
	code := run(nil, env{stdout: &buf, h: b.h, exeDir: filepath.Join(tree, "bin"), cwd: tree, systemDir: b.t.TempDir()})
	if code != 0 {
		t.Errorf("NOVA_GO=go1.26.5 was overridden by go.mod:\n%s", buf.String())
	}
}

func TestNoGoModAndNoNovaGoIsDriftNotAGuess(t *testing.T) {
	t.Parallel()
	b := conformingBench(t)
	b.unsetEnv("NOVA_GO")
	code, output := b.standard()
	wantOnlyDrift(t, code, output, "go.mod go directive unread; set NOVA_GO or run from a nova-tools checkout")
}

func TestAGoOnePatchBehindTheTreeDrifts(t *testing.T) {
	t.Parallel()
	b := conformingBench(t)
	b.setEnv("NOVA_GO=go1.26.6")
	code, output := b.standard()
	wantOnlyDrift(t, code, output, "go version [go version go1.26.5 linux/amd64] want go1.26.6")
}

func TestGoNotOnPathDrifts(t *testing.T) {
	t.Parallel()
	b := conformingBench(t)
	if err := os.Remove(filepath.Join(b.bin, "go")); err != nil {
		t.Fatal(err)
	}
	code, output := b.standard()
	wantOnlyDrift(t, code, output, "go not on PATH want go1.26.5")
}

func TestToolchainRootsMissingDrift(t *testing.T) {
	t.Parallel()
	b := conformingBench(t)
	if err := os.RemoveAll(filepath.Join(b.home, "go", "pkg", "mod")); err != nil {
		t.Fatal(err)
	}
	code, output := b.standard()
	want := "toolchain root " + filepath.Join(b.home, "go/pkg/mod") + " missing; the sandbox wall grants this path and a card's go lives under it"
	wantOnlyDrift(t, code, output, want)
}

// The card's environment is the sdk env file's, so a caller's PATH that puts a
// shadow go first does not decide the verdict.
func TestTheCardEnvironmentFromSdkEnvShBeatsACallersPath(t *testing.T) {
	t.Parallel()
	b := conformingBench(t)
	shadow := filepath.Join(filepath.Dir(b.bin), "shadow")
	if err := os.MkdirAll(shadow, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(shadow, "go"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	b.h.reply("go", []string{"version"}, out("go version go1.20.0 linux/amd64\n"))
	// Only the real go answers go1.26.5: the shadow's answer is the poisoned one.
	b.h.first(func(s runSpec) bool {
		return s.name == filepath.Join(b.bin, "go") && len(s.args) == 1 && s.args[0] == "version"
	}, out("go version go1.26.5 linux/amd64\n"))
	b.setEnv("PATH=" + shadow + ":" + b.bin)
	b.write("sdk/env.sh", "export PATH=...\n", false)
	// Without the env file the shadow go is found first and the verdict is poisoned.
	code, output := b.standard()
	if code != 1 || !strings.Contains(output, "go1.20.0") {
		t.Fatalf("control: the caller's shadow go did not decide the verdict without the env file (exit %d):\n%s", code, output)
	}
	b.h.sourced = map[string]string{"PATH": b.bin}
	code, output = b.standard()
	if code != 0 || strings.Contains(output, "go1.20.0") {
		t.Errorf("the env file's PATH did not decide the verdict (exit %d):\n%s", code, output)
	}
}

func TestSourcedVariablesReachLaterChecks(t *testing.T) {
	t.Parallel()
	b := conformingBench(t)
	b.unsetEnv("NOVA_SLOT_SHARE")
	b.write("sdk/env.sh", "export NOVA_SLOT_SHARE=12\n", false)
	b.h.sourced = map[string]string{"NOVA_SLOT_SHARE": "12", "PATH": b.bin}
	if code, output := b.standard(); code != 0 {
		t.Errorf("a slot share set by the sdk env file was not read (exit %d):\n%s", code, output)
	}
}

func TestSbclRows(t *testing.T) {
	t.Parallel()
	t.Run("absent", func(t *testing.T) {
		t.Parallel()
		b := conformingBench(t)
		os.Remove(filepath.Join(b.bin, "sbcl"))
		code, output := b.standard()
		wantOnlyDrift(t, code, output, "sbcl not on PATH")
		if n := len(drifts(output)); n != 1 {
			t.Errorf("an absent sbcl drew %d lines, want 1 (the pin rows wait for a present one):\n%s", n, output)
		}
	})
	t.Run("the pin is a whole version", func(t *testing.T) {
		t.Parallel()
		b := conformingBench(t)
		b.h.reply("sbcl", []string{"--version"}, out("SBCL 2.6.0.debian\n"))
		code, output := b.standard()
		wantOnlyDrift(t, code, output, "sbcl version [SBCL 2.6.0.debian] want 2.5.8 (NOVA_SBCL)")
		// 2.5.80 is not 2.5.8.
		b.h.reply("sbcl", []string{"--version"}, out("SBCL 2.5.80\n"))
		if code, output = b.standard(); code != 1 {
			t.Errorf("2.5.80 passed the 2.5.8 pin:\n%s", output)
		}
	})
	t.Run("NOVA_SBCL overrides the pin", func(t *testing.T) {
		t.Parallel()
		b := conformingBench(t)
		b.h.reply("sbcl", []string{"--version"}, out("SBCL 2.6.0.debian\n"))
		b.setEnv("NOVA_SBCL=2.6.0.debian")
		if code, output := b.standard(); code != 0 {
			t.Errorf("exit %d:\n%s", code, output)
		}
	})
	t.Run("an sbcl outside sdk", func(t *testing.T) {
		t.Parallel()
		b := conformingBench(t)
		elsewhere := filepath.Join(filepath.Dir(b.bin), "elsewhere")
		if err := os.MkdirAll(elsewhere, 0o755); err != nil {
			t.Fatal(err)
		}
		real := filepath.Join(elsewhere, "sbcl")
		if err := os.WriteFile(real, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		os.Remove(filepath.Join(b.bin, "sbcl"))
		if err := os.Symlink(real, filepath.Join(b.bin, "sbcl")); err != nil {
			t.Fatal(err)
		}
		code, output := b.standard()
		if code != 1 || len(driftWith(output, "sbcl at ")) != 1 || !strings.Contains(output, "not under "+b.home+"/sdk (want "+b.home+"/sdk/sbcl-2.5.8/)") {
			t.Errorf("exit %d:\n%s", code, output)
		}
	})
}

func TestProRungRows(t *testing.T) {
	t.Parallel()
	b := conformingBench(t)
	if err := os.Remove(filepath.Join(b.home, "nova-bench", "rungs", "pro")); err != nil {
		t.Fatal(err)
	}
	code, output := b.standard()
	wantOnlyDrift(t, code, output, "pro rung missing (no executable NOVA_PRO_RUNG, no "+b.home+"/nova-bench/rungs/pro, no "+b.home+"/nova-bench/pro)")

	// NOVA_PRO_RUNG naming an executable satisfies it.
	b.setEnv("NOVA_PRO_RUNG=" + filepath.Join(b.home, "nova-bench", "harness-v1", "opencode"))
	if code, output = b.standard(); code != 0 {
		t.Errorf("an executable NOVA_PRO_RUNG still drifted:\n%s", output)
	}
	// So does the other directory.
	b.unsetEnv("NOVA_PRO_RUNG")
	b.mkdir("nova-bench", "pro")
	if code, output = b.standard(); code != 0 {
		t.Errorf("nova-bench/pro still drifted:\n%s", output)
	}
	// A rung file with no execute bit is not one.
	b2 := conformingBench(t)
	os.Remove(filepath.Join(b2.home, "nova-bench", "rungs", "pro"))
	b2.setEnv("NOVA_PRO_RUNG=" + b2.write("rung", "x", false))
	if code, _ := b2.standard(); code != 1 {
		t.Errorf("a non-executable NOVA_PRO_RUNG satisfied the row")
	}
}

func TestSqliteRows(t *testing.T) {
	t.Parallel()
	b := conformingBench(t)
	os.Remove(filepath.Join(b.bin, "sqlite3"))
	code, output := b.standard()
	wantOnlyDrift(t, code, output, "sqlite3 not on PATH (want "+b.home+"/sdk/sqlite3-<ver>/bin/sqlite3)")

	// One outside sdk is named with where it resolves.
	b2 := conformingBench(t)
	elsewhere := filepath.Join(filepath.Dir(b2.bin), "elsewhere")
	os.MkdirAll(elsewhere, 0o755)
	real := filepath.Join(elsewhere, "sqlite3")
	os.WriteFile(real, []byte("#!/bin/sh\n"), 0o755)
	os.Remove(filepath.Join(b2.bin, "sqlite3"))
	os.Symlink(real, filepath.Join(b2.bin, "sqlite3"))
	code, output = b2.standard()
	if code != 1 || len(driftWith(output, "sqlite3 at ")) != 1 || !strings.Contains(output, "not under "+b2.home+"/sdk (want "+b2.home+"/sdk/sqlite3-<ver>/bin/sqlite3)") {
		t.Errorf("exit %d:\n%s", code, output)
	}
}

func TestSlotShareRows(t *testing.T) {
	t.Parallel()
	b := conformingBench(t)
	b.unsetEnv("NOVA_SLOT_SHARE")
	code, output := b.standard()
	wantOnlyDrift(t, code, output, "NOVA_SLOT_SHARE unset (declare the bench's slot share, a positive whole number of slots)")
	for _, bad := range []string{"lots", "0", "07", "1.5", "-3", "6 4"} {
		b.setEnv("NOVA_SLOT_SHARE=" + bad)
		code, output = b.standard()
		wantOnlyDrift(t, code, output, "NOVA_SLOT_SHARE="+bad+" is not a positive whole number of slots")
	}
	for _, good := range []string{"1", "64", "512"} {
		b.setEnv("NOVA_SLOT_SHARE=" + good)
		if code, output = b.standard(); code != 0 {
			t.Errorf("NOVA_SLOT_SHARE=%s drifted:\n%s", good, output)
		}
	}
}

func TestHarnessRows(t *testing.T) {
	t.Parallel()
	b := conformingBench(t)
	os.Remove(filepath.Join(b.home, "nova-bench", "harness-v1", "opencode"))
	code, output := b.standard()
	wantOnlyDrift(t, code, output, "harness missing at "+b.home+"/nova-bench/harness-<ver>/opencode")
	if len(sandboxRuns(b.h)) != 1 { // only the network probe; no canary without a harness
		t.Errorf("the canary ran without a harness: %v", b.h.ran)
	}

	b2 := conformingBench(t)
	b2.setEnv("NOVA_HARNESS=" + filepath.Join(b2.home, "no-such-harness"))
	code, output = b2.standard()
	wantOnlyDrift(t, code, output, "harness missing at NOVA_HARNESS="+b2.home+"/no-such-harness")

	// An executable NOVA_HARNESS is the harness whatever else is on the bench.
	b3 := conformingBench(t)
	os.Remove(filepath.Join(b3.home, "nova-bench", "harness-v1", "opencode"))
	b3.setEnv("NOVA_HARNESS=" + b3.write("nova-bench/mine/opencode", "x", true))
	if code, output = b3.standard(); code != 0 {
		t.Errorf("exit %d:\n%s", code, output)
	}
}

// A harness the wall refuses means every card would die at startup.
func TestHarnessCanaryDetectsSandboxWallDenial(t *testing.T) {
	t.Parallel()
	b := conformingBench(t)
	b.h.first(isCanary, runResult{stderr: "SANDBOX REFUSED: harness denied by wall\n", code: 1})
	code, output := b.standard()
	opencode := filepath.Join(b.home, "nova-bench", "harness-v1", "opencode")
	wantOnlyDrift(t, code, output, "harness cannot start inside the sandbox wall; "+opencode+" --help failed under nova-sandbox")

	// The canary runs the harness under a HOME of its own, inside its scratch
	// directory, with the bench readable and the scratch writable.
	var canary runSpec
	for _, c := range sandboxRuns(b.h) {
		if isCanary(c) {
			canary = c
		}
	}
	if canary.name == "" {
		t.Fatalf("no canary ran: %v", b.h.ran)
	}
	wantArgs := []string{"--read", filepath.Join(b.home, "nova-bench"), "--write", b.h.made[0], "--cwd", b.h.made[0], "--", opencode, "--help"}
	if !reflect.DeepEqual(canary.args, wantArgs) {
		t.Errorf("canary args %v, want %v", canary.args, wantArgs)
	}
	if lookupEnv(canary.env, "HOME") != filepath.Join(b.h.made[0], "home") {
		t.Errorf("canary HOME = %q", lookupEnv(canary.env, "HOME"))
	}
}

// sandboxRuns are the times the witness ran something INSIDE the wall: a
// nova-sandbox started with a --read grant, which is not its version check.
func sandboxRuns(h *fakeHost) []runSpec {
	var runs []runSpec
	for _, c := range h.calls("nova-sandbox") {
		if len(c.args) > 0 && c.args[0] == "--read" {
			runs = append(runs, c)
		}
	}
	return runs
}

// isCanary is the sandbox run that starts the harness; the network probe is the
// other.
func isCanary(s runSpec) bool {
	return filepath.Base(s.name) == "nova-sandbox" && len(s.args) > 7 && s.args[0] == "--read" && s.args[len(s.args)-1] == "--help"
}

// isProbe is the sandbox run of curl.
func isProbe(s runSpec) bool {
	return filepath.Base(s.name) == "nova-sandbox" && len(s.args) > 7 && s.args[0] == "--read" && filepath.Base(s.args[7]) == "curl"
}

func lookupEnv(environ []string, key string) string {
	for i := len(environ) - 1; i >= 0; i-- {
		if k, v, ok := strings.Cut(environ[i], "="); ok && k == key {
			return v
		}
	}
	return ""
}

func TestHarnessCanaryAndNetworkProbeAreLinuxOnly(t *testing.T) {
	t.Parallel()
	b := conformingBench(t)
	b.h.osName = "darwin"
	if code, output := b.standard(); code != 0 {
		t.Fatalf("exit %d:\n%s", code, output)
	}
	if n := len(sandboxRuns(b.h)); n != 0 {
		t.Errorf("the wall probes ran on darwin: %v", b.h.ran)
	}
	if len(b.h.made) != 0 {
		t.Errorf("scratch directories made on darwin: %v", b.h.made)
	}
}

func TestSandboxNetworkRows(t *testing.T) {
	t.Parallel()
	probe := func(b *bench, res runResult) { b.h.first(isProbe, res) }
	cases := []struct {
		name  string
		res   runResult
		drift string // a DRIFT prefix, or "" for none
	}{
		{"200", runResult{stdout: "200"}, ""},
		{"404", runResult{stdout: "404"}, "sandbox-network: curl inside nova-sandbox got http=404 (want 200)"},
		{"unreachable, empty reply", runResult{code: 7}, "sandbox-network: curl inside nova-sandbox got http= (want 200)"},
		{"000", runResult{stdout: "000"}, "sandbox-network: curl inside nova-sandbox got http=000 (want 200)"},
		{"not a status: the sandbox did not run curl", runResult{stdout: "nova-sandbox: refused"}, ""},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			b := conformingBench(t)
			probe(b, tc.res)
			code, output := b.standard()
			if tc.drift == "" {
				if code != 0 {
					t.Errorf("exit %d:\n%s", code, output)
				}
				return
			}
			wantOnlyDrift(t, code, output, tc.drift)
		})
	}

	t.Run("the probe is run by the sandbox, with the URL, never on the host", func(t *testing.T) {
		t.Parallel()
		b := conformingBench(t)
		b.standard()
		if n := len(b.h.calls("curl")); n != 0 {
			t.Errorf("curl was run on the host %d times", n)
		}
		var args []string
		for _, c := range sandboxRuns(b.h) {
			if isProbe(c) {
				args = c.args
			}
		}
		tail := args[len(args)-7:]
		want := []string{filepath.Join(b.bin, "curl"), "-s", "-o", "/dev/null", "-w", "%{http_code}", "https://probe.invalid/api.json"}
		if !reflect.DeepEqual(tail, want) {
			t.Errorf("probe command %v, want %v", tail, want)
		}
	})

	t.Run("the default URL", func(t *testing.T) {
		t.Parallel()
		b := conformingBench(t)
		b.unsetEnv("NOVA_PROBE_URL")
		b.standard()
		var last []string
		for _, c := range sandboxRuns(b.h) {
			if isProbe(c) {
				last = c.args
			}
		}
		if got := last[len(last)-1]; got != "https://models.opencode.ai/api.json" {
			t.Errorf("default URL %q", got)
		}
	})

	t.Run("no sandbox binary", func(t *testing.T) {
		t.Parallel()
		b := conformingBench(t)
		os.Remove(filepath.Join(b.home, ".local", "bin", "nova-sandbox"))
		code, output := b.standard()
		if code != 1 || len(driftWith(output, "sandbox-network: "+filepath.Join(b.home, ".local/bin/nova-sandbox")+" not executable")) != 1 {
			t.Errorf("exit %d:\n%s", code, output)
		}
		if len(b.h.made) != len(b.h.removed) {
			t.Errorf("a scratch directory was left: made %v removed %v", b.h.made, b.h.removed)
		}
	})

	t.Run("no probe directory can be made", func(t *testing.T) {
		t.Parallel()
		b := conformingBench(t)
		os.RemoveAll(filepath.Join(b.home, "nova-bench"))
		_, output := b.standard()
		if len(driftWith(output, "sandbox-network: cannot make probe dir under "+b.home+"/nova-bench")) != 1 {
			t.Errorf("\n%s", output)
		}
	})
}

func TestNovaBinRows(t *testing.T) {
	t.Parallel()
	t.Run("NOVA_WANT unset", func(t *testing.T) {
		t.Parallel()
		b := conformingBench(t)
		b.unsetEnv("NOVA_WANT")
		code, output := b.standard()
		wantOnlyDrift(t, code, output, "NOVA_WANT unset (set NOVA_WANT to the wanted version)")
	})
	t.Run("a binary missing", func(t *testing.T) {
		t.Parallel()
		b := conformingBench(t)
		os.Remove(filepath.Join(b.home, ".local", "bin", "nova-fuse"))
		code, output := b.standard()
		wantOnlyDrift(t, code, output, "nova-fuse missing at "+filepath.Join(b.home, ".local/bin/nova-fuse"))
	})
	t.Run("a wrong version", func(t *testing.T) {
		t.Parallel()
		b := conformingBench(t)
		b.h.reply("nova-swarm", []string{"version"}, out("nova-swarm v0.0.1\n"))
		code, output := b.standard()
		wantOnlyDrift(t, code, output, "nova-swarm version [nova-swarm v0.0.1] want "+benchWant)
	})
	t.Run("--version when version is refused", func(t *testing.T) {
		t.Parallel()
		b := conformingBench(t)
		b.h.reply("nova-check", []string{"version"}, runResult{stderr: "unknown verb\n", code: 2})
		b.h.reply("nova-check", []string{"--version"}, out(benchWant+"\n"))
		if code, output := b.standard(); code != 0 {
			t.Errorf("exit %d:\n%s", code, output)
		}
		b.h.reply("nova-check", []string{"--version"}, out("nova-check 1.0\n"))
		code, output := b.standard()
		wantOnlyDrift(t, code, output, "nova-check version [nova-check 1.0] want "+benchWant)
	})
	t.Run("the eleven", func(t *testing.T) {
		t.Parallel()
		if len(novaBins) != 11 {
			t.Errorf("the standard checks %d binaries, want 11: %v", len(novaBins), novaBins)
		}
	})
}

func TestPlaintextKeyRows(t *testing.T) {
	t.Parallel()
	for _, rel := range []string{".local/share/opencode/auth.json", ".config/deepseek/env"} {
		rel := rel
		t.Run(rel, func(t *testing.T) {
			t.Parallel()
			b := conformingBench(t)
			b.write(rel, "secret", false)
			code, output := b.standard()
			wantOnlyDrift(t, code, output, "plaintext key file "+filepath.Join(b.home, rel)+" present")
		})
	}
	t.Run("a literal apiKey in an opencode json", func(t *testing.T) {
		t.Parallel()
		b := conformingBench(t)
		bad := b.write(".config/opencode/opencode.json", `{"apiKey": "sk-abc"}`, false)
		b.write(".config/opencode/other.json", `{"apiKey": "{env:KEY}"}`, false)
		code, output := b.standard()
		wantOnlyDrift(t, code, output, "plaintext apiKey in "+bad)
		if n := len(drifts(output)); n != 1 {
			t.Errorf("%d findings, want only the literal key:\n%s", n, output)
		}
	})
}

func TestDiskHeadroomDriftsAndNamesTheThreeLargest(t *testing.T) {
	t.Parallel()
	t.Run("room", func(t *testing.T) {
		t.Parallel()
		b := conformingBench(t)
		code, output := b.standard()
		if code != 0 || len(driftWith(output, "disk")) != 0 {
			t.Errorf("a bench with 400G free drifted:\n%s", output)
		}
	})
	t.Run("under the floor", func(t *testing.T) {
		t.Parallel()
		b := conformingBench(t)
		b.h.reply("df", []string{"-Pk"}, out(dfTable(3)))
		for _, n := range []string{"huge", "big", "medium", "small"} {
			b.mkdir(n)
		}
		du := func(s runSpec) bool { return filepath.Base(s.name) == "du" }
		b.h.first(du, out(
			"40\t"+filepath.Join(b.home, "small")+"\n"+
				"300\t"+filepath.Join(b.home, "medium")+"\n"+
				"614400\t"+filepath.Join(b.home, "big")+"\n"+
				"2097152\t"+filepath.Join(b.home, "huge")+"\n"))
		code, output := b.standard()
		lines := driftWith(output, "disk")
		if code != 1 || len(lines) != 1 {
			t.Fatalf("exit %d, %d disk lines:\n%s", code, len(lines), output)
		}
		want := "DRIFT disk free=3G want>=25G (both launchers refuse below it); largest under " + b.home + ": " +
			filepath.Join(b.home, "huge") + " 2G; " + filepath.Join(b.home, "big") + " 600M; " + filepath.Join(b.home, "medium") + " 300K"
		// The walk sorts by size: huge 2G, big 600M, medium 300K, and never small.
		if lines[0] != want {
			t.Errorf("disk line\n got %s\nwant %s", lines[0], want)
		}
		if strings.Contains(lines[0], "small") {
			t.Errorf("more than the three largest:\n%s", lines[0])
		}
	})
	t.Run("nothing readable", func(t *testing.T) {
		t.Parallel()
		b := conformingBench(t)
		b.h.reply("df", []string{"-Pk"}, out(dfTable(3)))
		_, output := b.standard()
		want := "DRIFT disk free=3G want>=25G (both launchers refuse below it); largest under " + b.home + ": nothing readable under " + b.home
		if got := driftWith(output, "disk"); len(got) != 1 || got[0] != want {
			t.Errorf("got %v\nwant %s", got, want)
		}
	})
	t.Run("df is asked in the one POSIX spelling", func(t *testing.T) {
		t.Parallel()
		b := conformingBench(t)
		b.standard()
		dfs := b.h.calls("df")
		if len(dfs) != 1 || !reflect.DeepEqual(dfs[0].args, []string{"-Pk", b.home}) {
			t.Errorf("df was run as %v, want one `-Pk $HOME`", dfs)
		}
		for _, d := range dfs {
			for _, a := range d.args {
				if strings.Contains(a, "BG") {
					t.Errorf("a GNU-only df spelling: %v", d.args)
				}
			}
		}
	})
	t.Run("the walk runs only on the failing path", func(t *testing.T) {
		t.Parallel()
		b := conformingBench(t)
		b.standard()
		if n := len(b.h.calls("du")); n != 0 {
			t.Errorf("du ran %d times on a bench with room", n)
		}
	})
	t.Run("unreadable free space", func(t *testing.T) {
		t.Parallel()
		for name, res := range map[string]runResult{
			"no output":   {},
			"header only": out("Filesystem 1024-blocks Used Available Capacity Mounted on\n"),
			"garbage":     out("Filesystem\nshort row\n"),
			"not a count": out("Filesystem\n/dev/x 1 1 lots 1% /\n"),
			"df fails":    {err: errNoAnswer},
		} {
			res := res
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				b := conformingBench(t)
				b.h.reply("df", []string{"-Pk"}, res)
				code, output := b.standard()
				wantOnlyDrift(t, code, output, "disk free unknown: df answered nothing readable for "+b.home)
			})
		}
	})
	t.Run("the floor is NOVA_MIN_FREE_G", func(t *testing.T) {
		t.Parallel()
		b := conformingBench(t)
		b.h.reply("df", []string{"-Pk"}, out(dfTable(30)))
		b.setEnv("NOVA_MIN_FREE_G=40")
		code, output := b.standard()
		if code != 1 || len(driftWith(output, "disk free=30G want>=40G")) != 1 {
			t.Errorf("exit %d:\n%s", code, output)
		}
		b.setEnv("NOVA_MIN_FREE_G=0")
		b.h.reply("df", []string{"-Pk"}, out(dfTable(0)))
		if code, output = b.standard(); code != 0 {
			t.Errorf("a floor of 0 still drifted:\n%s", output)
		}
	})
	t.Run("a floor that is not a number is refused", func(t *testing.T) {
		t.Parallel()
		b := conformingBench(t)
		b.setEnv("NOVA_MIN_FREE_G=lots")
		code, output := b.standard()
		wantOnlyDrift(t, code, output, "NOVA_MIN_FREE_G=lots is not a whole number of gigabytes")
	})
	t.Run("the floor is exactly the boundary", func(t *testing.T) {
		t.Parallel()
		b := conformingBench(t)
		b.h.reply("df", []string{"-Pk"}, out(dfTable(25)))
		if code, output := b.standard(); code != 0 {
			t.Errorf("25G free against a 25G floor drifted:\n%s", output)
		}
		b.h.reply("df", []string{"-Pk"}, out("Filesystem\n/dev/x 1 1 "+"26214399"+" 1% /\n"))
		if code, _ := b.standard(); code != 1 {
			t.Errorf("one KiB under 25G passed")
		}
	})
}
