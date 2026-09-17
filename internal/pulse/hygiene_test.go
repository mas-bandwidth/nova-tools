package pulse

// Red tests for nova-pulse fleet hygiene (docs/SPEC-PULSE.md ## Fleet hygiene). Each fakes
// what a test cannot have: a fake root tree, a fake clock, and fake sudo, systemctl, df,
// du, pgrep and ssh on PATH. No test reaches a machine.

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

const hygGB = 1024 * 1024 // KB in a GB

func hygWrite(t *testing.T, path, body string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
}

func hygAge(t *testing.T, path string, age time.Duration, now time.Time) {
	t.Helper()
	ts := now.Add(-age)
	if err := os.Chtimes(path, ts, ts); err != nil {
		t.Fatal(err)
	}
}

func hygFake(t *testing.T, dir, name, body string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the hygiene fakes are shell programs; the bench is linux")
	}
	hygWrite(t, filepath.Join(dir, name), body, 0o755)
}

func hygPATH(t *testing.T, fakeBin string) {
	t.Helper()
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func hygInstallTools(t *testing.T, sudoExit, systemctlExit int) string {
	t.Helper()
	bin := t.TempDir()
	hygFake(t, bin, "sudo", "#!/bin/sh\nexit "+itoa(sudoExit)+"\n")
	hygFake(t, bin, "systemctl", "#!/bin/sh\necho \"$@\" >> \"$HYG_SYSTEMCTL_LOG\"\nexit "+itoa(systemctlExit)+"\n")
	hygPATH(t, bin)
	return bin
}

func hygCacheTools(t *testing.T, freeKB, cacheKB int) string {
	t.Helper()
	bin := t.TempDir()
	hygFake(t, bin, "df", "#!/bin/sh\necho 'Filesystem 1024-blocks Used Available Capacity Mounted on'\necho \"/dev/sda1 100000000 1 "+itoa(freeKB)+" 1% /\"\n")
	hygFake(t, bin, "du", "#!/bin/sh\necho \""+itoa(cacheKB)+"\t$2\"\n")
	hygPATH(t, bin)
	return bin
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

func hygLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if strings.HasPrefix(line, "HYGIENE ") {
			return line
		}
	}
	return ""
}

func hygField(t *testing.T, line, key string) string {
	t.Helper()
	for _, f := range strings.Fields(line) {
		if v, ok := strings.CutPrefix(f, key+"="); ok {
			return v
		}
	}
	t.Fatalf("no %s= in %q", key, line)
	return ""
}

func TestHygieneInstallWritesScriptAndTimer(t *testing.T) {
	root := t.TempDir()
	log := filepath.Join(t.TempDir(), "systemctl.log")
	t.Setenv("HYG_SYSTEMCTL_LOG", log)
	hygInstallTools(t, 0, 0)
	now := time.Now().UTC()
	in := HygieneInput{Root: root, Now: func() time.Time { return now }, Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}}

	if code := HygieneInstall(in); code != 0 {
		t.Fatalf("first install exit = %d", code)
	}
	script := filepath.Join(root, "nova-bench", "bench-hygiene.sh")
	timer := filepath.Join(root, "nova-bench", "bench-hygiene.timer")
	if _, err := os.Stat(script); err != nil {
		t.Fatalf("install did not write the script: %v", err)
	}
	if _, err := os.Stat(timer); err != nil {
		t.Fatalf("install did not write the timer: %v", err)
	}
	raw, err := os.ReadFile(timer)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "10min") {
		t.Errorf("the timer is not ten minutes: %q", raw)
	}
	old := now.Add(-48 * time.Hour)
	if err := os.Chtimes(script, old, old); err != nil {
		t.Fatal(err)
	}
	if code := HygieneInstall(in); code != 0 {
		t.Fatalf("second install exit = %d", code)
	}
	info, err := os.Stat(script)
	if err != nil {
		t.Fatal(err)
	}
	if !info.ModTime().Equal(old) {
		t.Errorf("second install rewrote the script (mtime %v, want %v)", info.ModTime(), old)
	}
}

func TestHygieneInstallRefusesWithoutSudoOrSystemd(t *testing.T) {
	t.Run("no-sudo", func(t *testing.T) {
		root := t.TempDir()
		hygInstallTools(t, 1, 0)
		var out, errb bytes.Buffer
		in := HygieneInput{Root: root, Now: time.Now, Stdout: &out, Stderr: &errb}
		if code := HygieneInstall(in); code != 2 {
			t.Fatalf("exit = %d, want 2", code)
		}
		if !strings.Contains(errb.String(), "FLEET REFUSED bench=") || !strings.Contains(errb.String(), "no-sudo (run it under sudo, or install the script by hand)") {
			t.Fatalf("no-sudo refusal = %q", errb.String())
		}
		if _, err := os.Stat(filepath.Join(root, "nova-bench", "bench-hygiene.sh")); !os.IsNotExist(err) {
			t.Errorf("a refusing install wrote a script")
		}
	})
	t.Run("no-systemd", func(t *testing.T) {
		root := t.TempDir()
		hygInstallTools(t, 0, 1)
		var out, errb bytes.Buffer
		in := HygieneInput{Root: root, Now: time.Now, Stdout: &out, Stderr: &errb}
		if code := HygieneInstall(in); code != 2 {
			t.Fatalf("exit = %d, want 2", code)
		}
		if !strings.Contains(errb.String(), "FLEET REFUSED bench=") || !strings.Contains(errb.String(), "no-systemd (install the timer by hand)") {
			t.Fatalf("no-systemd refusal = %q", errb.String())
		}
		if _, err := os.Stat(filepath.Join(root, "nova-bench", "bench-hygiene.timer")); !os.IsNotExist(err) {
			t.Errorf("a refusing install wrote a timer")
		}
	})
}

func TestHygieneDryRunPrintsWouldAndDeletesNothing(t *testing.T) {
	root := t.TempDir()
	now := time.Now().UTC()
	hygCacheTools(t, 40*hygGB, 10*hygGB)
	slot := filepath.Join(root, "rowan-swarm-root", "s1")
	stale := filepath.Join(slot, "jobs", "stale")
	keep := filepath.Join(slot, "jobs", "keep")
	harvested := filepath.Join(slot, "jobs", "harvested")
	hygWrite(t, filepath.Join(stale, "harness-output.log"), "old\n", 0o644)
	hygAge(t, filepath.Join(stale, "harness-output.log"), 7*time.Hour, now)
	hygWrite(t, filepath.Join(keep, "harness-output.log"), "fresh\n", 0o644)
	hygAge(t, filepath.Join(keep, "harness-output.log"), time.Minute, now)
	hygWrite(t, filepath.Join(harvested, ".harvested"), "", 0o644)
	empty := filepath.Join(root, "rowan-swarm-root", "s2")
	hygWrite(t, filepath.Join(empty, "README"), "x\n", 0o644)

	before := hygSnapshot(t, root)
	var out, errb bytes.Buffer
	in := HygieneInput{Root: root, Now: func() time.Time { return now }, Stdout: &out, Stderr: &errb}
	if code := HygieneDryRun(in); code != 0 {
		t.Fatalf("dry-run exit = %d, stderr=%s", code, errb.String())
	}
	s := out.String()
	for _, want := range []string{"WOULD rm -rf " + stale, "WOULD rm -rf " + harvested, "WOULD rm -rf " + empty} {
		if !strings.Contains(s, want) {
			t.Errorf("dry-run did not print %q:\n%s", want, s)
		}
	}
	if got := strings.Count(s, "WOULD rm -rf "); got != 3 {
		t.Errorf("dry-run printed %d WOULD lines, want 3:\n%s", got, s)
	}
	if after := hygSnapshot(t, root); after != before {
		t.Errorf("dry-run changed the tree:\nbefore=%v\nafter=%v", before, after)
	}
}

func hygSnapshot(t *testing.T, root string) string {
	t.Helper()
	var b strings.Builder
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if path == root {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		b.WriteString(rel + "\n")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func TestHygieneNeverTouchesALiveJob(t *testing.T) {
	root := t.TempDir()
	now := time.Now().UTC()
	bin := t.TempDir()
	hygFake(t, bin, "pgrep", "#!/bin/sh\nexit 0\n")
	hygCacheTools(t, 40*hygGB, 10*hygGB)
	hygPATH(t, bin)
	slot := filepath.Join(root, "rowan-swarm-root", "s1")
	fresh := filepath.Join(slot, "jobs", "fresh")
	named := filepath.Join(slot, "jobs", "named")
	hygWrite(t, filepath.Join(fresh, "harness-output.log"), "x\n", 0o644)
	hygAge(t, filepath.Join(fresh, "harness-output.log"), 5*time.Minute, now)
	hygWrite(t, filepath.Join(named, "harness-output.log"), "x\n", 0o644)
	hygAge(t, filepath.Join(named, "harness-output.log"), 7*time.Hour, now)

	var out, errb bytes.Buffer
	in := HygieneInput{Root: root, Now: func() time.Time { return now }, Stdout: &out, Stderr: &errb}
	if code := HygieneDryRun(in); code != 0 {
		t.Fatalf("dry-run exit = %d, stderr=%s", code, errb.String())
	}
	s := out.String()
	if strings.Contains(s, fresh) || strings.Contains(s, named) {
		t.Errorf("dry-run would touch a live job:\n%s", s)
	}
	for _, dir := range []string{fresh, named} {
		if _, err := os.Stat(dir); err != nil {
			t.Errorf("the live job %s was touched: %v", dir, err)
		}
	}
}

func TestHygieneDeletesAHarvestedJobWhole(t *testing.T) {
	root := t.TempDir()
	log := filepath.Join(t.TempDir(), "systemctl.log")
	t.Setenv("HYG_SYSTEMCTL_LOG", log)
	hygInstallTools(t, 0, 0)
	hygCacheTools(t, 40*hygGB, 10*hygGB)
	now := time.Now().UTC()
	job := filepath.Join(root, "rowan-swarm-root", "s1", "jobs", "done")
	hygWrite(t, filepath.Join(job, ".harvested"), "", 0o644)
	hygWrite(t, filepath.Join(job, "RESULT.md"), "ok\n", 0o644)

	var out, errb bytes.Buffer
	in := HygieneInput{Root: root, Now: func() time.Time { return now }, Stdout: &out, Stderr: &errb}
	if code := HygieneInstall(in); code != 0 {
		t.Fatalf("install exit = %d, stderr=%s", code, errb.String())
	}
	if _, err := os.Stat(job); !os.IsNotExist(err) {
		t.Errorf("the harvested job survived: %v", err)
	}
	line := hygLine(out.String())
	if line == "" {
		t.Fatalf("install printed no HYGIENE line: %q", out.String())
	}
	if got := hygField(t, line, "jobs-deleted"); got != "1" {
		t.Errorf("jobs-deleted = %s, want 1 (%q)", got, line)
	}
}

func TestHygieneDeletesAnUnreadFinishedJobAfterSixHours(t *testing.T) {
	root := t.TempDir()
	log := filepath.Join(t.TempDir(), "systemctl.log")
	t.Setenv("HYG_SYSTEMCTL_LOG", log)
	hygInstallTools(t, 0, 0)
	hygCacheTools(t, 40*hygGB, 10*hygGB)
	now := time.Now().UTC()
	old := filepath.Join(root, "rowan-swarm-root", "s1", "jobs", "old")
	young := filepath.Join(root, "rowan-swarm-root", "s1", "jobs", "young")
	hygWrite(t, filepath.Join(old, "RESULT.md"), "done\n", 0o644)
	hygAge(t, filepath.Join(old, "RESULT.md"), 7*time.Hour, now)
	hygWrite(t, filepath.Join(young, "RESULT.md"), "done\n", 0o644)
	hygAge(t, filepath.Join(young, "RESULT.md"), 5*time.Hour, now)

	var out, errb bytes.Buffer
	in := HygieneInput{Root: root, Now: func() time.Time { return now }, Stdout: &out, Stderr: &errb}
	if code := HygieneInstall(in); code != 0 {
		t.Fatalf("install exit = %d, stderr=%s", code, errb.String())
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Errorf("the seven-hour finished job survived: %v", err)
	}
	if _, err := os.Stat(young); err != nil {
		t.Errorf("the five-hour finished job was deleted: %v", err)
	}
	if got := hygField(t, hygLine(out.String()), "jobs-deleted"); got != "1" {
		t.Errorf("jobs-deleted = %s, want 1", got)
	}
}

func TestHygieneDeletesEmptySlots(t *testing.T) {
	root := t.TempDir()
	log := filepath.Join(t.TempDir(), "systemctl.log")
	t.Setenv("HYG_SYSTEMCTL_LOG", log)
	hygInstallTools(t, 0, 0)
	hygCacheTools(t, 40*hygGB, 10*hygGB)
	now := time.Now().UTC()
	empty := filepath.Join(root, "rowan-swarm-root", "empty")
	hygWrite(t, filepath.Join(empty, "README"), "x\n", 0o644)
	liveSlot := filepath.Join(root, "rowan-swarm-root", "live")
	liveJob := filepath.Join(liveSlot, "jobs", "card")
	hygWrite(t, filepath.Join(liveJob, "harness-output.log"), "x\n", 0o644)
	hygAge(t, filepath.Join(liveJob, "harness-output.log"), time.Minute, now)

	var out, errb bytes.Buffer
	in := HygieneInput{Root: root, Now: func() time.Time { return now }, Stdout: &out, Stderr: &errb}
	if code := HygieneInstall(in); code != 0 {
		t.Fatalf("install exit = %d, stderr=%s", code, errb.String())
	}
	if _, err := os.Stat(empty); !os.IsNotExist(err) {
		t.Errorf("the empty slot survived: %v", err)
	}
	if _, err := os.Stat(liveSlot); err != nil {
		t.Errorf("the live slot was deleted: %v", err)
	}
	if _, err := os.Stat(liveJob); err != nil {
		t.Errorf("the live job was deleted: %v", err)
	}
	if got := hygField(t, hygLine(out.String()), "slots-deleted"); got != "1" {
		t.Errorf("slots-deleted = %s, want 1", got)
	}
}

func TestHygienePrunesRunnerTempOlderThanADay(t *testing.T) {
	root := t.TempDir()
	log := filepath.Join(t.TempDir(), "systemctl.log")
	t.Setenv("HYG_SYSTEMCTL_LOG", log)
	hygInstallTools(t, 0, 0)
	hygCacheTools(t, 40*hygGB, 10*hygGB)
	now := time.Now().UTC()
	temp := filepath.Join(root, "runner-nova-tools-1", "_work", "_temp")
	old := filepath.Join(temp, "old")
	young := filepath.Join(temp, "young")
	hygWrite(t, old, "x\n", 0o644)
	hygAge(t, old, 25*time.Hour, now)
	hygWrite(t, young, "x\n", 0o644)
	hygAge(t, young, time.Hour, now)

	var out, errb bytes.Buffer
	in := HygieneInput{Root: root, Now: func() time.Time { return now }, Stdout: &out, Stderr: &errb}
	if code := HygieneInstall(in); code != 0 {
		t.Fatalf("install exit = %d, stderr=%s", code, errb.String())
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Errorf("the day-old runner temp survived: %v", err)
	}
	if _, err := os.Stat(young); err != nil {
		t.Errorf("the young runner temp was deleted: %v", err)
	}
}

func TestHygieneDropsTheGoCacheBelow25GFreeOrAbove20G(t *testing.T) {
	now := time.Now().UTC()
	cases := []struct {
		name   string
		freeGB int
		cache  int
		want   string
	}{
		{"free-low", 20, 10, "dropped(10G)"},
		{"size-high", 40, 25, "dropped(25G)"},
		{"keep", 40, 10, "kept(10G)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			log := filepath.Join(t.TempDir(), "systemctl.log")
			t.Setenv("HYG_SYSTEMCTL_LOG", log)
			hygInstallTools(t, 0, 0)
			hygCacheTools(t, tc.freeGB*hygGB, tc.cache*hygGB)
			cache := filepath.Join(root, ".cache", "go-build")
			hygWrite(t, filepath.Join(cache, "00", "x"), "x\n", 0o644)

			var out, errb bytes.Buffer
			in := HygieneInput{Root: root, Now: func() time.Time { return now }, Stdout: &out, Stderr: &errb}
			if code := HygieneInstall(in); code != 0 {
				t.Fatalf("install exit = %d, stderr=%s", code, errb.String())
			}
			line := hygLine(out.String())
			if !strings.Contains(line, "cache="+tc.want) {
				t.Fatalf("line = %q, want cache=%s", line, tc.want)
			}
			_, err := os.Stat(cache)
			dropped := strings.HasPrefix(tc.want, "dropped")
			if dropped && !os.IsNotExist(err) {
				t.Errorf("the cache was not dropped: %v", err)
			}
			if !dropped && err != nil {
				t.Errorf("the cache was dropped: %v", err)
			}
		})
	}
}

func TestHygieneStatusPrintsTheLastLineAndFreeSpacePerBench(t *testing.T) {
	bin := t.TempDir()
	canned := t.TempDir()
	hygFake(t, bin, "ssh", "#!/bin/sh\ntarget=\"$5\"\ncat \"$HYG_SSH_DIR/$target\" 2>/dev/null\nexit 0\n")
	t.Setenv("HYG_SSH_DIR", canned)
	hygPATH(t, bin)
	hygWrite(t, filepath.Join(canned, "fake-alpha"), "HYGIENE host slots=2 reaped=1 jobs-deleted=3 slots-deleted=1 cache=kept(10G) free 40 -> 42\nFilesystem 1024-blocks Used Available Capacity Mounted on\n/dev/sda1 100000000 1 "+itoa(30*hygGB)+" 1% /\n", 0o644)
	benches := filepath.Join(t.TempDir(), "benches.tsv")
	hygWrite(t, benches, "alpha\tfake-alpha\t/home/alpha\tmock\n", 0o644)

	var out, errb bytes.Buffer
	in := HygieneInput{Benches: benches, SSH: filepath.Join(bin, "ssh"), Max: 20, Timeout: time.Minute, Stdout: &out, Stderr: &errb}
	if code := HygieneStatus(in); code != 0 {
		t.Fatalf("status exit = %d, stderr=%s", code, errb.String())
	}
	want := "FLEET alpha HYGIENE slots=2 reaped=1 jobs-deleted=3 slots-deleted=1 cache=kept(10G) free=30G"
	if !strings.Contains(out.String(), want) {
		t.Fatalf("status output = %q, want %q", out.String(), want)
	}
}

func TestHygieneRefusesStudio(t *testing.T) {
	bin := t.TempDir()
	log := filepath.Join(t.TempDir(), "ssh.log")
	hygFake(t, bin, "ssh", "#!/bin/sh\necho \"$@\" >> \"$HYG_SSH_LOG\"\nexit 0\n")
	t.Setenv("HYG_SSH_LOG", log)
	hygPATH(t, bin)
	benches := filepath.Join(t.TempDir(), "benches.tsv")
	hygWrite(t, benches, "alpha\tfake-alpha\t/home/alpha\tmock\nstudio\tfake-studio\t/home/studio\tmock\n", 0o644)

	var out, errb bytes.Buffer
	in := HygieneInput{Benches: benches, SSH: filepath.Join(bin, "ssh"), Max: 20, Timeout: time.Minute, Stdout: &out, Stderr: &errb}
	if code := HygieneStatus(in); code != 2 {
		t.Fatalf("status exit = %d, want 2", code)
	}
	if !strings.Contains(errb.String(), "FLEET REFUSED bench=studio (the reference bench is never cleaned by this tool)") {
		t.Fatalf("studio refusal = %q", errb.String())
	}
	if raw, err := os.ReadFile(log); err == nil && strings.TrimSpace(string(raw)) != "" {
		t.Errorf("the studio refusal contacted a bench: %q", raw)
	}
}

func TestHygieneStatusWithoutBenchesRefuses(t *testing.T) {
	bin := t.TempDir()
	log := filepath.Join(t.TempDir(), "ssh.log")
	hygFake(t, bin, "ssh", "#!/bin/sh\necho \"$@\" >> \"$HYG_SSH_LOG\"\nexit 0\n")
	t.Setenv("HYG_SSH_LOG", log)
	hygPATH(t, bin)

	var out, errb bytes.Buffer
	in := HygieneInput{SSH: filepath.Join(bin, "ssh"), Max: 20, Timeout: time.Minute, Stdout: &out, Stderr: &errb}
	if code := HygieneStatus(in); code != 2 {
		t.Fatalf("status exit = %d, want 2", code)
	}
	if !strings.Contains(errb.String(), "refusing to guess") {
		t.Fatalf("missing --benches refusal = %q", errb.String())
	}
	if raw, err := os.ReadFile(log); err == nil && strings.TrimSpace(string(raw)) != "" {
		t.Errorf("a refusing status contacted a bench: %q", raw)
	}
}
