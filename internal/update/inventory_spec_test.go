//go:build unix

package update

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The red tests docs/SPEC-VERSION.md's snapshot/diff section demands. Every
// nova-* binary and every built version line is a fake: the stubs below are
// shell scripts, so this file is unix-only.

func specStub(t *testing.T, dir, name, line string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\nprintf '%s\\n' '"+line+"'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func specScript(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func specRun(t *testing.T, env Environment, args ...string) (int, string, string) {
	t.Helper()
	var out, errs strings.Builder
	c := Run("nova-version", args, "test", &out, &errs, env)
	return c, out.String(), errs.String()
}

// 1. TestSnapshotWritesOneRowPerBinary.
func TestSnapshotWritesOneRowPerBinary(t *testing.T) {
	bin := t.TempDir()
	specStub(t, bin, "nova-bus", "nova-bus 20260909112233-0123456789ab linux/amd64 go1.26.0")
	specStub(t, bin, "nova-check", "nova-check 20260909112233-0123456789ab darwin/arm64 go1.26.0")
	specStub(t, bin, "readme.txt", "not a binary")
	out := filepath.Join(t.TempDir(), "snapshot.tsv")
	code, stdout, stderr := specRun(t, Environment{}, "snapshot", "--bin", bin, "--out", out)
	if code != 0 {
		t.Fatalf("exit %d stderr=%s", code, stderr)
	}
	need(t, stdout, "SNAPSHOT OK bin="+field(bin), "out="+field(out), "tools=2", "stamp=20260909112233-0123456789ab")
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	body := string(b)
	lines := strings.Split(strings.TrimSuffix(body, "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("want a header and two rows, got %d:\n%s", len(lines), body)
	}
	if lines[0] != "name\tstamp\trevision\tplatform" {
		t.Fatalf("header=%q", lines[0])
	}
	if lines[1] != "nova-bus\t20260909112233-0123456789ab\t0123456789ab\tlinux/amd64" {
		t.Fatalf("row 1=%q", lines[1])
	}
	if lines[2] != "nova-check\t20260909112233-0123456789ab\t0123456789ab\tdarwin/arm64" {
		t.Fatalf("row 2=%q", lines[2])
	}
	if strings.Contains(body, "readme") {
		t.Fatalf("a non-nova file was recorded:\n%s", body)
	}
}

// 2. TestSnapshotReadsVersionNotTheFileName.
func TestSnapshotReadsVersionNotTheFileName(t *testing.T) {
	bin := t.TempDir()
	specStub(t, bin, "nova-renamed", "nova-bus v9.9.9 linux/amd64 go1.0")
	out := filepath.Join(t.TempDir(), "snapshot.tsv")
	code, _, stderr := specRun(t, Environment{}, "snapshot", "--bin", bin, "--out", out)
	if code != 0 {
		t.Fatalf("exit %d stderr=%s", code, stderr)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "nova-renamed\tv9.9.9\t-") {
		t.Fatalf("the row forged the printed stamp from the name:\n%s", b)
	}
}

// 3. TestSnapshotRefusesAMixedSetNamingThePair.
func TestSnapshotRefusesAMixedSetNamingThePair(t *testing.T) {
	bin := t.TempDir()
	specStub(t, bin, "nova-a", "nova-a v1.0.0 linux/amd64 go1.0")
	specStub(t, bin, "nova-b", "nova-b v2.0.0 linux/amd64 go1.0")
	out := filepath.Join(t.TempDir(), "snapshot.tsv")
	code, _, stderr := specRun(t, Environment{}, "snapshot", "--bin", bin, "--out", out)
	if code != 2 {
		t.Fatalf("exit %d stderr=%s", code, stderr)
	}
	need(t, stderr, "nova-a", "nova-b", "v1.0.0", "v2.0.0")
	if _, err := os.Stat(out); err == nil {
		t.Fatalf("a mixed set was written to --out")
	}
}

// 4. TestSnapshotRefusesAMissingFlag.
func TestSnapshotRefusesAMissingFlag(t *testing.T) {
	bin := t.TempDir()
	specStub(t, bin, "nova-a", "nova-a v1.0.0 linux/amd64 go1.0")
	out := filepath.Join(t.TempDir(), "snapshot.tsv")
	for _, tc := range []struct {
		name string
		args []string
		flag string
	}{
		{"no bin", []string{"snapshot", "--out", out}, "--bin"},
		{"no out", []string{"snapshot", "--bin", bin}, "--out"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, _, stderr := specRun(t, Environment{}, tc.args...)
			if code != 2 {
				t.Fatalf("exit %d stderr=%s", code, stderr)
			}
			need(t, stderr, "refusing to guess", tc.flag)
		})
	}
}

// 5. TestSnapshotRefusesABinaryWithNoVersion.
func TestSnapshotRefusesABinaryWithNoVersion(t *testing.T) {
	t.Run("non-zero exit", func(t *testing.T) {
		bin := t.TempDir()
		specScript(t, bin, "nova-nope", "exit 3")
		code, _, stderr := specRun(t, Environment{}, "snapshot", "--bin", bin, "--out", filepath.Join(t.TempDir(), "s.tsv"))
		if code != 2 {
			t.Fatalf("exit %d stderr=%s", code, stderr)
		}
		need(t, stderr, "nova-nope")
	})
	t.Run("no parseable line", func(t *testing.T) {
		bin := t.TempDir()
		specStub(t, bin, "nova-blank", "not a version line")
		code, _, stderr := specRun(t, Environment{}, "snapshot", "--bin", bin, "--out", filepath.Join(t.TempDir(), "s.tsv"))
		if code != 2 {
			t.Fatalf("exit %d stderr=%s", code, stderr)
		}
		need(t, stderr, "nova-blank")
	})
}

// 6. TestSnapshotRefusesAnUnreadableBin.
func TestSnapshotRefusesAnUnreadableBin(t *testing.T) {
	t.Run("bin is a file", func(t *testing.T) {
		file := filepath.Join(t.TempDir(), "not-a-dir")
		if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		code, _, stderr := specRun(t, Environment{}, "snapshot", "--bin", file, "--out", filepath.Join(t.TempDir(), "s.tsv"))
		if code != 2 {
			t.Fatalf("exit %d stderr=%s", code, stderr)
		}
		need(t, stderr, file, "--bin")
	})
	t.Run("no nova files", func(t *testing.T) {
		bin := t.TempDir()
		if err := os.WriteFile(filepath.Join(bin, "readme.txt"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		code, _, stderr := specRun(t, Environment{}, "snapshot", "--bin", bin, "--out", filepath.Join(t.TempDir(), "s.tsv"))
		if code != 2 {
			t.Fatalf("exit %d stderr=%s", code, stderr)
		}
		need(t, stderr, bin, "--bin")
	})
}

func specSnapshot(t *testing.T, rows ...string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "snap.tsv")
	body := "name\tstamp\trevision\tplatform\n" + strings.Join(rows, "\n") + "\n"
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// 7. TestDiffNamesOneLinePerChangedBinary.
func TestDiffNamesOneLinePerChangedBinary(t *testing.T) {
	a := specSnapshot(t, "alpha\tv1.0.0\t-\tlinux/amd64", "beta\tv1.0.0\t-\tlinux/amd64")
	b := specSnapshot(t, "alpha\tv2.0.0\t-\tlinux/amd64", "beta\tv1.0.0\t-\tlinux/amd64")
	code, stdout, stderr := specRun(t, Environment{}, "diff", "--from", a, "--to", b)
	if code != 0 {
		t.Fatalf("exit %d stderr=%s", code, stderr)
	}
	need(t, stdout, "DIFF CHANGED name=alpha from=v1.0.0 to=v2.0.0")
	if strings.Contains(stdout, "name=beta") {
		t.Fatalf("an unchanged binary printed a line:\n%s", stdout)
	}
	need(t, stdout, "DIFF OK from="+field(a), "to="+field(b), "tools=2", "changed=1")
}

// 8. TestDiffNamesAddedAndRemoved.
func TestDiffNamesAddedAndRemoved(t *testing.T) {
	a := specSnapshot(t, "gone\tv1.0.0\t-\tlinux/amd64", "same\tv1.0.0\t-\tlinux/amd64")
	b := specSnapshot(t, "new\tv2.0.0\t-\tlinux/amd64", "same\tv1.0.0\t-\tlinux/amd64")
	code, stdout, stderr := specRun(t, Environment{}, "diff", "--from", a, "--to", b)
	if code != 0 {
		t.Fatalf("exit %d stderr=%s", code, stderr)
	}
	need(t, stdout, "DIFF CHANGED name=gone from=v1.0.0 to=-", "DIFF CHANGED name=new from=- to=v2.0.0", "changed=2")
}

// 9. TestDiffRefusesANonSnapshotFile.
func TestDiffRefusesANonSnapshotFile(t *testing.T) {
	t.Run("wrong header", func(t *testing.T) {
		bad := filepath.Join(t.TempDir(), "bad.tsv")
		if err := os.WriteFile(bad, []byte("tool\tversion\nx\t1\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		good := specSnapshot(t, "same\tv1.0.0\t-\tlinux/amd64")
		code, _, stderr := specRun(t, Environment{}, "diff", "--from", bad, "--to", good)
		if code != 2 {
			t.Fatalf("exit %d stderr=%s", code, stderr)
		}
		need(t, stderr, bad, "nova-version snapshot")
	})
	t.Run("wrong arity", func(t *testing.T) {
		bad := filepath.Join(t.TempDir(), "bad.tsv")
		if err := os.WriteFile(bad, []byte("name\tstamp\trevision\tplatform\nx\t1\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		good := specSnapshot(t, "same\tv1.0.0\t-\tlinux/amd64")
		code, _, stderr := specRun(t, Environment{}, "diff", "--from", bad, "--to", good)
		if code != 2 {
			t.Fatalf("exit %d stderr=%s", code, stderr)
		}
		need(t, stderr, bad, "nova-version snapshot")
	})
}

// 11. TestSnapshotIsBoundedByTheClock.
func TestSnapshotIsBoundedByTheClock(t *testing.T) {
	old := snapshotChildTimeout
	snapshotChildTimeout = 150 * time.Millisecond
	t.Cleanup(func() { snapshotChildTimeout = old })
	bin := t.TempDir()
	specScript(t, bin, "nova-slow", "sleep 5\nprintf 'nova-slow v1.0.0 linux/amd64 go1.0\\n'")
	out := filepath.Join(t.TempDir(), "s.tsv")
	env := Environment{Now: func() time.Time { return time.Date(2026, 9, 17, 0, 0, 0, 0, time.UTC) }}
	code, _, stderr := specRun(t, env, "snapshot", "--bin", bin, "--out", out)
	if code != 2 {
		t.Fatalf("exit %d stderr=%s", code, stderr)
	}
	need(t, stderr, "nova-slow", snapshotChildTimeout.String())
	if _, err := os.Stat(out); err == nil {
		t.Fatalf("a partial --out was written")
	}
}
