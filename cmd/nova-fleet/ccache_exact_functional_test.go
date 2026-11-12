//go:build functional

package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestFunctionalCcacheExact runs runner-packages' exactness check for the Windows ccache directory (rp_win_ccache_exact
// in roles/runner-packages/tasks/main.yml) with bash on real directories: the release's two binaries and VERSION are
// EXACT; a planted file, a changed binary, a folder linked in, the directory itself a link, or a wrong VERSION are
// DIFFERS; no directory is ABSENT. It needs GNU find (Linux, as on WSL).
func TestFunctionalCcacheExact(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "linux" {
		t.Skip("the check runs on WSL and uses GNU find -printf")
	}
	c, err := os.ReadFile("roles/runner-packages/tasks/main.yml")
	if err != nil {
		t.Fatal(err)
	}
	s := string(c)
	i := strings.Index(s, "rp_win_ccache_exact: |\n")
	if i < 0 {
		t.Fatal("no rp_win_ccache_exact in runner-packages")
	}
	var body []string
	for _, l := range strings.Split(s[i:], "\n")[1:] {
		if !strings.HasPrefix(l, "      ") {
			break
		}
		body = append(body, l[6:])
	}
	script := strings.Join(body, "\n") + "\n"
	for _, r := range [][2]string{
		{"d={{ nova_runner_windows_ccache_unix | quote }}", `d="$1"`},
		{"src={{ (rp_win_ccache_stage.path | default('') ~ '/ccache-' ~ nova_runner_windows_ccache.version ~ '-windows-x86_64') | quote }}", `src="$2"`},
		{"{{ nova_runner_windows_ccache.version | quote }}", "4.14.3"},
	} {
		if !strings.Contains(script, r[0]) {
			t.Fatalf("rp_win_ccache_exact lacks %q", r[0])
		}
		script = strings.Replace(script, r[0], r[1], 1)
	}
	if strings.Contains(script, "{{") {
		t.Fatalf("an unrendered expression is left:\n%s", script)
	}
	write := func(p, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name  string
		setup func(d, other string)
		want  string
	}{
		{"the release", func(string, string) {}, "EXACT"},
		{"a planted file", func(d, _ string) { write(filepath.Join(d, "msvc", "c1.dll"), "x") }, "DIFFERS"},
		{"a changed binary", func(d, _ string) { write(filepath.Join(d, "msvc", "cl.exe"), "evil") }, "DIFFERS"},
		{"a wrong VERSION", func(d, _ string) { write(filepath.Join(d, "VERSION"), "4.14.2\n") }, "DIFFERS"},
		{"a folder linked in", func(d, other string) {
			if err := os.RemoveAll(filepath.Join(d, "msvc")); err != nil {
				t.Fatal(err)
			}
			write(filepath.Join(other, "cl.exe"), "MZ release\n")
			if err := os.Symlink(other, filepath.Join(d, "msvc")); err != nil {
				t.Fatal(err)
			}
		}, "DIFFERS"},
		{"the directory a link", func(d, other string) {
			if err := os.RemoveAll(other); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(d, other); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(other, d); err != nil {
				t.Fatal(err)
			}
		}, "DIFFERS"},
		{"a folder that cannot be listed", func(d, _ string) {
			if err := os.Chmod(filepath.Join(d, "msvc"), 0); err != nil {
				t.Fatal(err)
			}
		}, "DIFFERS"},
		{"no directory", func(d, _ string) {
			if err := os.RemoveAll(d); err != nil {
				t.Fatal(err)
			}
		}, "ABSENT"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if tc.name == "a folder that cannot be listed" && os.Geteuid() == 0 {
				t.Skip("root lists a folder whatever its mode")
			}
			tmp := t.TempDir()
			src, d, other := filepath.Join(tmp, "src"), filepath.Join(tmp, "ccache"), filepath.Join(tmp, "other")
			write(filepath.Join(src, "ccache.exe"), "MZ release\n")
			write(filepath.Join(d, "ccache.exe"), "MZ release\n")
			write(filepath.Join(d, "msvc", "cl.exe"), "MZ release\n")
			write(filepath.Join(d, "VERSION"), "4.14.3\n")
			tc.setup(d, other)
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			out, err := exec.CommandContext(ctx, "bash", "-c", script, "exact", d, src).Output()
			_ = os.Chmod(filepath.Join(d, "msvc"), 0o755) // so the temporary directory can be removed
			if err != nil || strings.TrimSpace(string(out)) != tc.want {
				t.Errorf("got %q (%v), want %s", out, err, tc.want)
			}
		})
	}
}
