package release

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// THE WINDOWS BENCH IS A TARGET LIKE ANY OTHER, and the Threadripper arriving
// means `release build --platform windows-amd64` and the fan-out that follows
// it have to work from the Studio TODAY. Everything in this file is asserted
// through the fakes, on every runner: no test here reaches a windows machine,
// and none of them may be skipped on a non-windows host, because the whole
// point is that a coordinator that is NOT windows composes all of this.
//
// The far side's shell is Emma's decision, not a guess: docs/BENCH-WINDOWS.md
// names it as Git Bash (`C:\Program Files\Git\bin\bash.exe`), or native
// OpenSSH with Bash in sshd_config, and internal/pulse/fleetstandard.go's
// windows probes are POSIX shell that reach for `powershell.exe -NoProfile
// -Command '...'` only for the Windows-specific questions. So the far side
// parses a POSIX command line -- which is exactly why a backslash may not
// reach it: in that shell `C:\Users\nova` is `C:Usersnova`, silently, and the
// machine then refuses about a path nobody typed.

// windowsMachines writes a machine list naming one windows bench.
func windowsMachines(t *testing.T, lines ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "machines.tsv")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// A person writes the path the way Windows writes it -- `C:\Users\nova\.local\bin`
// is what docs/BENCH-WINDOWS.md puts in the runner's .path -- and the verb has
// to take it. Before this it did not: ValidRemotePath demanded a leading `/`
// or `~/`, so the adopt refused every windows bench at the flag, before ssh.
func TestAdoptTakesWindowsDrivePathsForBinAndDest(t *testing.T) {
	from := built(t, "v0.16.0", "windows-amd64", "nova-bus", "nova-update")
	s := &fakeSSH{answer: map[string]string{"threadripper": "RELEASE INSTALLED version=v0.16.0 tools=2 skipped=0 retired=0\n"}}
	var o, e bytes.Buffer
	code := Run("nova-update", []string{"adopt",
		"--version", "v0.16.0",
		"--machines", windowsMachines(t, "threadripper"),
		"--ssh", "ssh", "--from", from,
		"--bin", `C:\Users\nova\.local\bin`,
		"--dest", `C:\Users\nova\nova-release`,
		"--platform", "windows-amd64"}, &o, &e, Deps{SSH: s})
	if code != 0 {
		t.Fatalf("a windows bench was refused: code=%d errs=%s", code, e.String())
	}
	if !strings.Contains(o.String(), "RELEASE ADOPTED machine=threadripper") {
		t.Fatalf("no adopted line:\n%s", o.String())
	}
}

// AND WHAT REACHES THE FAR SIDE CARRIES NO BACKSLASH. The remote command is
// parsed by a POSIX shell there, so every path in it is slash-form -- which
// Windows itself accepts everywhere -- and the tool it runs is nova-update.exe
// at an absolute path composed from --dest.
func TestAdoptComposesSlashPathsForAWindowsBench(t *testing.T) {
	from := built(t, "v0.16.0", "windows-amd64", "nova-bus", "nova-update")
	s := &fakeSSH{answer: map[string]string{"threadripper": "RELEASE INSTALLED version=v0.16.0 tools=2 skipped=0 retired=0\n"}}
	var o, e bytes.Buffer
	if code := Run("nova-update", []string{"adopt",
		"--version", "v0.16.0",
		"--machines", windowsMachines(t, "threadripper"),
		"--ssh", "ssh", "--from", from,
		"--bin", `C:\Users\nova\.local\bin`,
		"--dest", `C:\Users\nova\nova-release`,
		"--retire", `C:\Users\nova\go\bin`,
		"--platform", "windows-amd64"}, &o, &e, Deps{SSH: s}); code != 0 {
		t.Fatalf("code=%d errs=%s", code, e.String())
	}
	install := installRun(t, s, "threadripper")
	want := "threadripper: C:/Users/nova/nova-release/v0.16.0/windows-amd64/nova-update.exe release install " +
		"--from C:/Users/nova/nova-release --version v0.16.0 --bin C:/Users/nova/.local/bin " +
		"--platform windows-amd64 --retire C:/Users/nova/go/bin"
	if install != want {
		t.Fatalf("the remote install is\n  %s\nwant\n  %s", install, want)
	}
	for _, run := range s.runs {
		if strings.Contains(run, `\`) {
			t.Fatalf("a backslash reached the far side's shell, where it is an escape: %s", run)
		}
	}
	// The stream lands beside the version directory, by the same slash path.
	if len(s.sends) != 1 || !strings.HasSuffix(s.sends[0], "-> C:/Users/nova/nova-release/v0.16.0") {
		t.Fatalf("the release was not sent to the windows dest: %v", s.sends)
	}
}

// --dry-run asks the machine what it is running, and it has to ask after the
// file that exists there: `C:/Users/nova/.local/bin/nova-update.exe version`.
// A bare `nova-update` resolves against a $PATH nobody here chose, and a
// suffix-less name resolves against nothing at all.
func TestAdoptDryRunProbesTheExeOnAWindowsBench(t *testing.T) {
	from := built(t, "v0.16.0", "windows-amd64", "nova-bus", "nova-update")
	s := &fakeSSH{answer: map[string]string{"threadripper": "nova-update v0.15.0 windows/amd64 go1.26.5\n"}}
	var o, e bytes.Buffer
	if code := Run("nova-update", []string{"adopt",
		"--version", "v0.16.0",
		"--machines", windowsMachines(t, "threadripper"),
		"--ssh", "ssh", "--from", from,
		"--bin", `C:\Users\nova\.local\bin`,
		"--dest", `C:\Users\nova\nova-release`,
		"--platform", "windows-amd64", "--dry-run"}, &o, &e, Deps{SSH: s}); code != 0 {
		t.Fatalf("code=%d errs=%s", code, e.String())
	}
	want := "threadripper: C:/Users/nova/.local/bin/nova-update.exe version"
	var found bool
	for _, run := range s.runs {
		if run == want {
			found = true
		}
	}
	if !found {
		t.Fatalf("the probe was not %q: %v", want, s.runs)
	}
	if len(s.sends) != 0 {
		t.Fatalf("--dry-run streamed something: %v", s.sends)
	}
}

// A windows path on a LINUX target is a mistake, and a loud one: `C:\...` is
// not a path any linux bench has, and taking it would mean composing a remote
// command whose first token cannot exist. The drive form is allowed for the
// windows target and for nothing else.
func TestAdoptRefusesAWindowsPathForALinuxTarget(t *testing.T) {
	from := built(t, "v0.16.0", "linux-amd64", "nova-bus", "nova-update")
	var o, e bytes.Buffer
	code := Run("nova-update", []string{"adopt",
		"--version", "v0.16.0",
		"--machines", windowsMachines(t, "hulk"),
		"--ssh", "ssh", "--from", from,
		"--bin", `C:\Users\nova\.local\bin`,
		"--dest", "~/nova-release",
		"--platform", "linux-amd64"}, &o, &e, Deps{SSH: &fakeSSH{}})
	if code != 2 {
		t.Fatalf("a windows path was taken for a linux bench: code=%d out=%s", code, o.String())
	}
	if !strings.Contains(e.String(), "--bin") || !strings.Contains(e.String(), "linux") {
		t.Fatalf("the refusal names neither the flag nor the target: %s", e.String())
	}
}

// AND THE GATE IS STILL A GATE. Johnny's read (2026-09-18) is that a path is
// checked before any remote command is composed, and widening it to drive
// paths must not widen it to shell syntax: the far side is a POSIX shell
// whatever the operating system under it.
func TestAWindowsPathMayStillCarryNoShellSyntax(t *testing.T) {
	for _, bad := range []string{
		`C:\Users\nova\bin;calc.exe`,
		`C:\Users\nova\bin $(whoami)`,
		"C:/Users/nova/bin`id`",
		`C:\Users\nova\..\..\Windows\System32`,
		`C:Users\nova\bin`, // drive-RELATIVE: resolves against that drive's cwd
		`\\fileserver\share\bin`,
	} {
		t.Run(bad, func(t *testing.T) {
			if err := ValidRemotePathOn("windows", "--bin", bad); err == nil {
				t.Fatalf("%q was taken as a path on a windows bench", bad)
			}
		})
	}
	for _, good := range []string{
		`C:\Users\nova\.local\bin`,
		"C:/Users/nova/.local/bin",
		"D:/nova/bin",
		"~/.local/bin",  // Git Bash has a home and expands it
		"/c/Users/nova", // and its own rooted form
	} {
		t.Run(good, func(t *testing.T) {
			if err := ValidRemotePathOn("windows", "--bin", good); err != nil {
				t.Fatalf("%q was refused on a windows bench: %v", good, err)
			}
		})
	}
}

// RemotePath is the one place a path is turned into the form that goes INSIDE
// a remote command. It is a separate function from the validation because the
// two answer different questions -- "may this be sent" and "what is sent" --
// and because every composition site has to reach the same answer.
func TestRemotePathFoldsBackslashesForTheFarSidesShell(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{`C:\Users\nova\.local\bin`, "C:/Users/nova/.local/bin"},
		{"C:/Users/nova/.local/bin", "C:/Users/nova/.local/bin"},
		{"~/.local/bin", "~/.local/bin"},
		{"/home/nova/.local/bin", "/home/nova/.local/bin"},
	} {
		if got := RemotePath(tc.in); got != tc.want {
			t.Fatalf("RemotePath(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// The --machines columns are the same paths by another route -- the fleet has
// more than one home directory and the odd machine says so in the file -- so
// they take the drive form too, and are normalised the same way.
func TestTheMachineColumnsTakeAWindowsPath(t *testing.T) {
	from := built(t, "v0.16.0", "windows-amd64", "nova-bus", "nova-update")
	s := &fakeSSH{answer: map[string]string{"threadripper": "RELEASE INSTALLED version=v0.16.0 tools=2 skipped=0 retired=0\n"}}
	var o, e bytes.Buffer
	if code := Run("nova-update", []string{"adopt",
		"--version", "v0.16.0",
		"--machines", windowsMachines(t, "threadripper\t"+`C:\nova\bin`+"\t"+`C:\nova\release`),
		"--ssh", "ssh", "--from", from,
		"--bin", "~/.local/bin", "--dest", "~/nova-release",
		"--platform", "windows-amd64"}, &o, &e, Deps{SSH: s}); code != 0 {
		t.Fatalf("code=%d errs=%s", code, e.String())
	}
	install := installRun(t, s, "threadripper")
	if !strings.Contains(install, "--bin C:/nova/bin") || !strings.Contains(install, "--from C:/nova/release") {
		t.Fatalf("the columns did not reach the remote install: %s", install)
	}
	if !strings.HasPrefix(install, "threadripper: C:/nova/release/v0.16.0/windows-amd64/nova-update.exe ") {
		t.Fatalf("the remote tool is not under the column's dest: %s", install)
	}
}

// A release built ON the windows bench and adopted FROM the Studio: the
// `host:dir` --from names that machine's own path, which is a drive path, and
// the fetch has to be composed for it. The host part is two characters or
// more, so `C:\releases` is still read as a local path and not as a host.
func TestAdoptFetchesFromAWindowsBuildHost(t *testing.T) {
	served := ArtifactDir(built(t, "v0.16.0", "windows-amd64", "nova-bus", "nova-update"), "v0.16.0", "windows", "amd64")
	digest, err := fileSum(filepath.Join(served, SumsFile))
	if err != nil {
		t.Fatal(err)
	}
	s := &fakeSSH{
		serves: map[string]string{"threadripper": served},
		answer: map[string]string{"vision": "RELEASE INSTALLED version=v0.16.0 tools=2 skipped=0 retired=0\n"},
	}
	var o, e bytes.Buffer
	if code := Run("nova-update", []string{"adopt",
		"--version", "v0.16.0",
		"--machines", windowsMachines(t, "vision"),
		"--ssh", "ssh",
		"--from", `threadripper:C:\Users\nova\nova-release`,
		"--stage", t.TempDir(), "--expect-sums", digest,
		"--bin", `C:\Users\nova\.local\bin`, "--dest", `C:\Users\nova\nova-release`,
		"--platform", "windows-amd64"}, &o, &e, Deps{SSH: s}); code != 0 {
		t.Fatalf("code=%d errs=%s", code, e.String())
	}
	if len(s.fetches) != 1 {
		t.Fatalf("want one fetch from the build host, got %v", s.fetches)
	}
	if !strings.HasPrefix(s.fetches[0], "threadripper: C:/Users/nova/nova-release/v0.16.0/windows-amd64 -> ") {
		t.Fatalf("the fetch did not name the windows build host's own path: %v", s.fetches)
	}
	// Only the REMOTE half is checked for a backslash. The local half is the staging
	// directory this test was handed, and on a windows runner that is a native path
	// with backslashes in it -- `C:\Users\RUNNER~1\AppData\Local\Temp\...` is
	// correct there and asserting against it made this test fail on the one platform
	// it is about. What must carry no backslash is the path that goes over ssh.
	remote, _, _ := strings.Cut(s.fetches[0], " -> ")
	if strings.Contains(remote, `\`) {
		t.Fatalf("a backslash reached the remote half of the fetch: %v", s.fetches)
	}
}

// `pull` reaches the same machines with the same paths, and a withdrawal that
// cannot name the files on a windows bench is a withdrawal that leaves them
// there.
func TestPullTakesAWindowsDestAndNamesTheExeFiles(t *testing.T) {
	out := built(t, "v0.16.0", "windows-amd64", "nova-bus", "nova-update")
	local := ArtifactDir(out, "v0.16.0", "windows", "amd64")
	body, err := os.ReadFile(filepath.Join(local, SumsFile))
	if err != nil {
		t.Fatal(err)
	}
	changelog := filepath.Join(t.TempDir(), "CHANGELOG.md")
	if err := os.WriteFile(changelog, []byte("# Changelog\n\n## v0.16.0 - 2026-09-18\n\nA release.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := &fakeSSH{remoteSums: map[string]string{"threadripper": string(body)}}
	var o, e bytes.Buffer
	if code := Run("nova-update", []string{"pull",
		"--version", "v0.16.0", "--out", out, "--changelog", changelog,
		"--machines", windowsMachines(t, "threadripper"), "--ssh", "ssh",
		"--dest", `C:\Users\nova\nova-release`,
		"--reason", "a windows bench", "--platform", "windows-amd64"}, &o, &e, Deps{SSH: s}); code != 0 {
		t.Fatalf("code=%d errs=%s out=%s", code, e.String(), o.String())
	}
	var removal string
	for _, run := range s.runs {
		if strings.Contains(run, "nova-bus.exe") {
			removal = run
		}
	}
	if removal == "" {
		t.Fatalf("the pull never named nova-bus.exe on the windows bench: %v", s.runs)
	}
	if strings.Contains(removal, `\`) || !strings.Contains(removal, "C:/Users/nova/nova-release/v0.16.0/windows-amd64/nova-bus.exe") {
		t.Fatalf("the removal does not name the windows path: %s", removal)
	}
}

// ---------------------------------------------------------------------------
// the naming and the SUMS lines
// ---------------------------------------------------------------------------

// The checksum file is what `install` on the bench reads, and it is the ONLY
// thing it trusts about a directory it did not build. For a windows release
// every line in it therefore has to name a .exe -- a SUMS line naming
// `nova-bus` would install a file that is not there and skip the one that is.
func TestTheWindowsSumsFileNamesOnlyExeFiles(t *testing.T) {
	out := built(t, "v0.16.0", "windows-amd64", "nova-bus", "nova-swarm", "nova-update")
	dir := ArtifactDir(out, "v0.16.0", "windows", "amd64")
	body, err := os.ReadFile(filepath.Join(dir, SumsFile))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, line := range strings.Split(strings.TrimSpace(string(body)), "\n") {
		sum, name, ok := strings.Cut(line, "  ")
		if !ok {
			t.Fatalf("%s is not a sha256sum line: %q", SumsFile, line)
		}
		if len(sum) != 64 {
			t.Fatalf("%s line %q does not carry a sha256", SumsFile, line)
		}
		if !strings.HasSuffix(name, ".exe") {
			t.Fatalf("%s names %q, which no windows bench has", SumsFile, name)
		}
		names = append(names, name)
	}
	sort.Strings(names)
	if got := strings.Join(names, " "); got != "nova-bus.exe nova-swarm.exe nova-update.exe" {
		t.Fatalf("%s lists %q", SumsFile, got)
	}
	// And the digest file beside it is NOT one of the lines: it is written
	// after, out of the file it is the digest of.
	if strings.Contains(string(body), DigestFile) {
		t.Fatalf("%s lists %s: a checksum over the digest of itself is a number that changes every build", SumsFile, DigestFile)
	}
}

// THE SELF-VERIFY THAT CANNOT BE RUN. A `release build --platform
// windows-amd64` on the Studio or on hulk produces a nova-update.exe that this
// host cannot execute, so there is no way for the build to ask the artifact
// whether it answers `version`. That is stated rather than faked: the build's
// promise is the checksum round trip, and the version stamp is asserted where
// the binary can actually run -- by `install` on the bench, which probes every
// file it is about to replace through VersionOf.
//
// This test holds that boundary in place: the build's receipt claims
// verified=<n> for the checksum verification and never claims to have run
// anything.
func TestAWindowsBuildDoesNotClaimToHaveRunItsOwnArtifacts(t *testing.T) {
	out := t.TempDir()
	source := t.TempDir()
	for _, tool := range []string{"nova-bus", "nova-update"} {
		if err := os.MkdirAll(filepath.Join(source, "cmd", tool), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(source, "cmd", tool, "main.go"), []byte("package main\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var o, e bytes.Buffer
	if code := Run("nova-update", []string{"build", "--version", "v0.16.0", "--out", out,
		"--source", source, "--platform", "windows-amd64"}, &o, &e, Deps{Toolchain: &fakeToolchain{}}); code != 0 {
		t.Fatalf("code=%d errs=%s", code, e.String())
	}
	if !strings.Contains(o.String(), "platform=windows-amd64") || !strings.Contains(o.String(), "verified=2") {
		t.Fatalf("no windows build receipt with a verified count:\n%s", o.String())
	}
	if strings.Contains(o.String(), "ran=") || strings.Contains(o.String(), "answered=") {
		t.Fatalf("the build claims to have run a windows artifact it cannot execute:\n%s", o.String())
	}
}

// ---------------------------------------------------------------------------
// install, on the one filesystem that refuses to replace a running file
// ---------------------------------------------------------------------------

// WINDOWS WILL NOT REPLACE A FILE THAT IS OPEN FOR EXECUTION, and the file
// being replaced is frequently nova-update.exe replacing itself: `adopt` runs
// the release's own nova-update.exe on the bench and that process is holding
// its own image open. A plain rename over it fails there with a sharing
// violation, which on this side reads as INSTALL FAIL for a release that is
// perfectly good.
//
// Windows DOES allow the running file to be renamed ASIDE -- the handle
// follows the inode, not the name -- so the install moves it out of the way
// first and then renames the new one into place. The rename is a seam here so
// the fallback is exercised on every runner rather than only on the one
// platform that can produce the error.
func TestInstallMovesARunningFileAsideWhenTheRenameIsRefused(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "nova-update.exe.built")
	if err := os.WriteFile(src, []byte("new"), 0o755); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "nova-update.exe")
	if err := os.WriteFile(dst, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	var renames []string
	refusedOnce := false
	rename := func(oldpath, newpath string) error {
		renames = append(renames, filepath.Base(oldpath)+" -> "+filepath.Base(newpath))
		if !refusedOnce && filepath.Base(newpath) == "nova-update.exe" {
			refusedOnce = true
			return fmt.Errorf("Access is denied.")
		}
		return os.Rename(oldpath, newpath)
	}
	if err := installFile(src, dst, rename); err != nil {
		t.Fatalf("the install gave up on a file that was open for execution: %v", err)
	}
	body, err := os.ReadFile(dst)
	if err != nil || string(body) != "new" {
		t.Fatalf("the new binary is not in place: %q %v", body, err)
	}
	// The file it moved aside is DOT-PREFIXED, which is what keeps it out of
	// `nova-version snapshot` (it takes nova-* only) and out of `--retire`.
	var aside string
	for _, r := range renames {
		if strings.HasPrefix(r, "nova-update.exe -> ") {
			aside = strings.TrimPrefix(r, "nova-update.exe -> ")
		}
	}
	if aside == "" {
		t.Fatalf("nothing was moved aside: %v", renames)
	}
	if !strings.HasPrefix(aside, ".") || !strings.Contains(aside, "nova-update.exe") {
		t.Fatalf("the file moved aside is %q; it must be dot-prefixed so snapshot and retire skip it", aside)
	}
}

// AND A RENAME THAT FAILS FOR A REAL REASON STILL FAILS. The fallback is for
// one error on one filesystem; it must not turn a full disk or a read-only
// directory into a silent success, and it must leave nothing behind when it
// cannot finish.
func TestInstallPutsTheOldFileBackWhenTheFallbackAlsoFails(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "nova-bus.exe.built")
	if err := os.WriteFile(src, []byte("new"), 0o755); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "nova-bus.exe")
	if err := os.WriteFile(dst, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	// The new binary can never be moved into place -- a full disk, and it is
	// full both times. Moving the old one aside and putting it back are
	// renames within the directory and they work, so what is asserted is that
	// the verb undoes what it did rather than that it gave up early.
	rename := func(oldpath, newpath string) error {
		if strings.HasSuffix(oldpath, ".new") {
			return fmt.Errorf("no space left on device")
		}
		return os.Rename(oldpath, newpath)
	}
	if err := installFile(src, dst, rename); err == nil {
		t.Fatal("a rename that failed twice reported success")
	}
	body, err := os.ReadFile(dst)
	if err != nil || string(body) != "old" {
		t.Fatalf("the old binary was not put back: %q %v", body, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") {
			t.Fatalf("a temporary file was left behind: %s", entry.Name())
		}
	}
}

// A whole windows install through the verb, from the artifact directory the
// build wrote: the names are the target's, the probe asks after the target's
// names, and nothing rebuilds a name out of this host's runtime.GOOS.
func TestInstallOnAWindowsArtifactDirectoryUsesExeNamesThroughout(t *testing.T) {
	from := built(t, "v0.16.0", "windows-amd64", "nova-bus", "nova-update")
	bin := t.TempDir()
	var probed []string
	var o, e bytes.Buffer
	code := Run("nova-update", []string{"install", "--from", from, "--version", "v0.16.0",
		"--bin", bin, "--platform", "windows-amd64"}, &o, &e, Deps{
		VersionOf: func(_ context.Context, p string) (string, error) {
			probed = append(probed, filepath.Base(p))
			return "", fmt.Errorf("no such file")
		}})
	if code != 0 {
		t.Fatalf("code=%d errs=%s", code, e.String())
	}
	sort.Strings(probed)
	if got := strings.Join(probed, " "); got != "nova-bus.exe nova-update.exe" {
		t.Fatalf("probed %q, want the target's file names", got)
	}
	for _, name := range []string{"nova-bus.exe", "nova-update.exe"} {
		if _, err := os.Stat(filepath.Join(bin, name)); err != nil {
			t.Fatalf("%s was not installed: %v", name, err)
		}
	}
}
