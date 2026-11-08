//go:build functional

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"
)

// functional_test.go builds nova-fleet and runs it against ansible-core on PATH:
// plays, show install, and the install play under --check against a stub
// inventory file of localhost. It is skipped where ansible-playbook is absent.

func buildFleet(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "nova-fleet")
	out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput()
	if err != nil {
		t.Fatalf("go build: %v: %s", err, out)
	}
	return bin
}

func runFleet(t *testing.T, bin string, args ...string) (int, string, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = t.TempDir()
	cmd.Env = append(os.Environ(), "TMPDIR="+t.TempDir(), "ANSIBLE_LOCAL_TEMP="+t.TempDir(), "ANSIBLE_NOCOLOR=1")
	var o, e bytes.Buffer
	cmd.Stdout, cmd.Stderr = &o, &e
	err := cmd.Run()
	code := 0
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return code, o.String(), e.String()
}

func TestFleetBinaryAgainstAnsible(t *testing.T) {
	if _, err := exec.LookPath("ansible-playbook"); err != nil {
		t.Skip("ansible-core is not installed on this machine")
	}
	bin := buildFleet(t)

	code, out, errs := runFleet(t, bin, "plays")
	if code != 0 || !strings.Contains(out, "install ") || !strings.Contains(out, "services ") {
		t.Fatalf("plays: exit %d stdout %q stderr %q", code, out, errs)
	}
	want, err := os.ReadFile("install.yml")
	if err != nil {
		t.Fatal(err)
	}
	if code, out, _ := runFleet(t, bin, "show", "install"); code != 0 || out != string(want) {
		t.Fatalf("show install: exit %d, stdout is not install.yml", code)
	}

	inv := filepath.Join(t.TempDir(), "hosts.ini")
	if err := os.WriteFile(inv, []byte("[bench]\nlocalhost ansible_connection=local ansible_python_interpreter=auto_silent\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, errs = runFleet(t, bin, "install", "--check", "--inventory", inv, "--", "-e", "nova_tools_version=v0", "-l", "localhost")
	if !strings.Contains(errs, "nova-fleet: ansible-playbook=") || !strings.Contains(errs, "inventory="+inv) {
		t.Errorf("install: stderr does not echo the ansible-playbook and inventory used: %q", errs)
	}
	if !strings.Contains(out, "PLAY [nova-tools release on the machine]") || !strings.Contains(out, "localhost") {
		t.Fatalf("install --check: ansible-playbook did not run the play on localhost (exit %d):\n%s\n%s", code, out, errs)
	}
	// v0 is a tag no release carries: the role's version check refuses it, and
	// nova-fleet exits 1 naming ansible-playbook's status for that (2, a failed host).
	if code != 1 || !strings.Contains(out, "nova-tools-install: name the release") || !strings.Contains(errs, "nova-fleet install: FAILED ansible-playbook exit=2") {
		t.Errorf("install --check -e nova_tools_version=v0: exit %d, want 1 with the role's refusal and ansible-playbook's 2 named:\n%s\n%s", code, out, errs)
	}

	// Every play extracted from the binary passes ansible-playbook --syntax-check.
	for _, p := range plays(tree) {
		if code, out, errs := runFleet(t, bin, p, "--inventory", inv, "--", "--syntax-check"); code != 0 {
			t.Errorf("%s --syntax-check: exit %d\n%s\n%s", p, code, out, errs)
		}
	}
}

// install from a local build: a directory laid out as a release, copied after each
// file is checked against its SHA256SUMS line; a second run changes nothing, and a
// file that does not match its line is refused.
func TestInstallFromALocalBuild(t *testing.T) {
	if _, err := exec.LookPath("ansible-playbook"); err != nil {
		t.Skip("ansible-core is not installed on this machine")
	}
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("the local mode copies to Linux and macOS")
	}
	bin := buildFleet(t)
	dist := t.TempDir()
	asset := "nova-fleet_local_" + runtime.GOOS + "_" + runtime.GOARCH
	b, err := os.ReadFile(bin)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dist, asset), b, 0o755); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(b)
	if err := os.WriteFile(filepath.Join(dist, "SHA256SUMS"), []byte(hex.EncodeToString(sum[:])+"  "+asset+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	inv := filepath.Join(t.TempDir(), "hosts.ini")
	if err := os.WriteFile(inv, []byte("[bench]\nlocalhost ansible_connection=local ansible_python_interpreter=auto_silent\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// home and systemDir stand in for the login's home and /etc/systemd/system: the
	// leftover check at the end of the role reads them, and the machine's own are not this test's.
	home, systemDir := t.TempDir(), t.TempDir()
	binDir := filepath.Join(t.TempDir(), "bin")
	args := []string{"install", "--inventory", inv, "--", "-e", "nova_tools_version=local", "-e", "nova_tools_local_dir=" + dist, "-e", "nova_tools_unit_manager=false",
		"-e", "nova_bin_dir=" + binDir, "-e", "nova_home=" + home, "-e", "nova_tools_system_unit_dir=" + systemDir,
		"-e", `{"nova_tools_names":["nova-fleet"]}`}
	if code, out, errs := runFleet(t, bin, args...); code != 0 {
		t.Fatalf("install from %s: exit %d\n%s\n%s", dist, code, out, errs)
	}
	got, err := os.ReadFile(filepath.Join(binDir, "nova-fleet"))
	if err != nil || !bytes.Equal(got, b) {
		t.Fatalf("the installed nova-fleet is not the local build: %v", err)
	}
	code, out, _ := runFleet(t, bin, append([]string{"install", "--check"}, args[1:]...)...)
	if code != 0 || !strings.Contains(out, "changed=0") || !strings.Contains(out, "INSTALL localhost leftovers=0") {
		t.Errorf("a second run under --check: exit %d, want changed=0 and leftovers=0:\n%s", code, out)
	}

	// A unit fleet/retired-units.txt does not retire is never stopped: refused, named.
	code, out, _ = runFleet(t, bin, append(args, "-e", `{"nova_tools_remove_enabled":true,"nova_tools_remove_units":["tailscaled.service"]}`)...)
	if code == 0 || !strings.Contains(out, "nova_tools_remove_units names tailscaled.service, which fleet/retired-units.txt does not retire") {
		t.Errorf("a unit to retire that is not a retired one: exit %d, want the refusal:\n%s", code, out)
	}

	// Leftovers of an older install beside the new binaries. svc is the run with the
	// inventory's nova_services (none here), which the unit check needs.
	svc := append(append([]string{}, args...), "-e", `{"nova_services":[]}`)
	unitDir := filepath.Join(home, ".config", "systemd", "user")
	if runtime.GOOS == "darwin" {
		unitDir = filepath.Join(home, "Library", "LaunchAgents")
	}
	manifests := filepath.Join(home, ".local", "share", "nova", "installed")
	for _, d := range []string{unitDir, manifests} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	plant := func(files ...string) {
		t.Helper()
		for _, f := range files {
			if err := os.WriteFile(f, []byte("x\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	// What belongs: another release's binary its manifest lists, the fleet's own roles'
	// binary and units, a friend's nova-bus hook; beside them one v1 binary.
	allowed := []string{filepath.Join(binDir, "nova-worker"), filepath.Join(binDir, "nova-secrets-refresh"),
		filepath.Join(unitDir, "nova-secrets-refresh.timer"), filepath.Join(unitDir, "com.nova.secrets-refresh.plist"),
		filepath.Join(unitDir, "nova-bus.hook.x.plist"), filepath.Join(unitDir, "nova-bus-hook-x.service"),
		filepath.Join(systemDir, "nova-disk-report.timer"), filepath.Join(systemDir, "nova-pg-dump.service"), filepath.Join(systemDir, "nova-redis.service")}
	plant(allowed...)
	// Before nova-sprint's manifest is written (a release installed before this check), its
	// binaries are a warning, never a refusal and never advice to remove them.
	// A v1 binary no release ships (mirror-refresh, whose loops saturated the link on
	// 2026-11-07) is refused whatever manifests say, with the play that retires it.
	// nova-push-credential is retired with git's credential helper lines that run it; the gh one stays.
	plant(filepath.Join(binDir, "nova-card"), filepath.Join(binDir, "mirror-refresh"), filepath.Join(binDir, "nova-push-credential"))
	gitconfig := filepath.Join(home, ".gitconfig")
	gh := "!/opt/homebrew/bin/gh auth git-credential"
	if err := os.WriteFile(gitconfig, []byte("[credential \"https://github.com\"]\n\thelper = !"+binDir+"/nova-push-credential\n[credential]\n\thelper = "+gh+
		"\n\thelper = !"+binDir+"/nova-push-credential\n[user]\n\tname = x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, _ = runFleet(t, bin, svc...)
	for _, w := range []string{"INSTALL localhost leftovers=4: gitconfig credential.helper, gitconfig credential.https://github.com.helper, mirror-refresh, nova-push-credential;",
		`nova-fleet install --tag local -- -l localhost -e '{"nova_tools_remove_enabled":true,"nova_tools_remove":["mirror-refresh","nova-push-credential"]}'`,
		"INSTALL localhost WARNING unrecorded=2: nova-card, nova-worker; no release or manifest lists them"} {
		if code == 0 || !strings.Contains(out, w) {
			t.Errorf("binaries no manifest lists yet beside a v1 mirror-refresh: exit %d, want %q:\n%s", code, w, out)
		}
	}
	// The printed command runs: its extra vars (this run's own, the local build's among
	// them, added) move mirror-refresh into <bin>/retired-<date>/ and the host passes.
	m := regexp.MustCompile(`nova-fleet install --tag local -- -l localhost -e '(\{[^']*\})'`).FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("no install command in the refusal:\n%s", out)
	}
	code, out, _ = runFleet(t, bin, append(svc, "-e", m[1])...)
	retired, _ := filepath.Glob(filepath.Join(binDir, "retired-*", "*"))
	if code != 0 || len(retired) != 2 || !strings.Contains(out, "INSTALL localhost leftovers=0; unset git credential.https://github.com.helper, credential.helper (nova-push-credential)") {
		t.Errorf("the printed install command: exit %d, retired %v, want mirror-refresh and nova-push-credential in retired-<date>/, the helpers unset, leftovers=0:\n%s", code, retired, out)
	}
	if got, err := os.ReadFile(gitconfig); err != nil || strings.Contains(string(got), "nova-push-credential") || !strings.Contains(string(got), "helper = "+gh) || !strings.Contains(string(got), "name = x") {
		t.Errorf("~/.gitconfig after the retire: want no nova-push-credential and the gh helper kept (%v):\n%s", err, got)
	}
	if err := os.WriteFile(filepath.Join(manifests, "mas-bandwidth_nova-sprint.names"), []byte("nova-sprint\nnova-worker\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(binDir, "nova-card"), filepath.Join(home, "nova-card.x")); err != nil {
		t.Fatal(err)
	}
	if code, out, _ = runFleet(t, bin, svc...); code != 0 || !strings.Contains(out, "INSTALL localhost leftovers=0") || strings.Contains(out, "WARNING unrecorded") {
		t.Errorf("only what belongs: exit %d, want leftovers=0 and no warning:\n%s", code, out)
	}
	// Moved tools and their backup copies are refused, not merely warned about.
	// The printed remedy retires them; unrelated current tools remain in place.
	moved := []string{"nova-decide", "nova-decide.bak", "nova-doctor", "nova-doctor.old", "nova-tokens", "nova-tokens.before"}
	for _, name := range moved {
		plant(filepath.Join(binDir, name))
	}
	code, out, _ = runFleet(t, bin, svc...)
	if code == 0 || !strings.Contains(out, "INSTALL localhost leftovers=6: "+strings.Join(moved, ", ")+";") || strings.Contains(out, "WARNING unrecorded") {
		t.Fatalf("moved tools and backups: exit %d, want six refused leftovers and no warning:\n%s", code, out)
	}
	for _, name := range moved {
		if _, err := os.Stat(filepath.Join(binDir, name)); err != nil {
			t.Errorf("the refusal changed %s: %v", name, err)
		}
	}
	m = regexp.MustCompile(`nova-fleet install --tag local -- -l localhost -e '(\{[^']*\})'`).FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("no install command for moved leftovers:\n%s", out)
	}
	code, out, _ = runFleet(t, bin, append(svc, "-e", m[1])...)
	if code != 0 || !strings.Contains(out, "INSTALL localhost leftovers=0") {
		t.Fatalf("the printed command for moved leftovers: exit %d, want leftovers=0:\n%s", code, out)
	}
	for _, name := range moved {
		if got, _ := filepath.Glob(filepath.Join(binDir, "retired-*", name)); len(got) != 1 {
			t.Errorf("moved tool not retired: %s (%v)", name, got)
		}
	}
	for _, name := range []string{"nova-fleet", "nova-worker"} {
		if _, err := os.Stat(filepath.Join(binDir, name)); err != nil {
			t.Errorf("the retire changed current tool %s: %v", name, err)
		}
	}
	// A binary no release, manifest or retired row accounts for (nova-card from v1 sprint, or a
	// tool the machine uses) is a warning naming it, never a refusal, manifest or not.
	if err := os.Rename(filepath.Join(home, "nova-card.x"), filepath.Join(binDir, "nova-card")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(binDir, "nova-fleet")); err != nil {
		t.Fatal(err)
	}
	code, out, _ = runFleet(t, bin, svc...)
	if code != 0 || !strings.Contains(out, "INSTALL localhost leftovers=0") || !strings.Contains(out, "INSTALL localhost WARNING unrecorded=1: nova-card;") {
		t.Errorf("a binary nothing accounts for: exit %d, want leftovers=0 and the warning naming it:\n%s", code, out)
	}
	if got, err := os.ReadFile(filepath.Join(binDir, "nova-fleet")); err != nil || !bytes.Equal(got, b) {
		t.Errorf("the install did not happen: %v", err)
	}
	// Without nova_services the units are not read, and the line says so.
	plant(filepath.Join(unitDir, "nova-loop-sweep.timer"))
	code, out, _ = runFleet(t, bin, args...)
	if code != 0 || !strings.Contains(out, "INSTALL localhost leftovers=0; NOTE: the inventory carries no nova_services") {
		t.Errorf("no nova_services: exit %d, want leftovers=0 and the NOTE:\n%s", code, out)
	}

	// v1 binaries and units; the install still happens, then the host is refused, naming
	// them and the plays that retire them. Only retired names and units refuse, and a
	// backup of a retired name (winlock.bak-4slots); muse, nova-fleet.bak and
	// tlacheck.old-2026-10-19 (backups of names not retired) are warnings; age-keygen is
	// join.yml's. A v1
	// unit fleet/retired-units.txt names (nova-swarm-*, novadog-*) is retired by the
	// install command. On macOS a headless Mac's units are in /Library/LaunchDaemons
	// (systemDir stands in).
	plant(filepath.Join(binDir, "mirror-refresh"), filepath.Join(binDir, "nova-fleet.bak"), filepath.Join(binDir, "winlock.bak-4slots"),
		filepath.Join(binDir, "muse"), filepath.Join(binDir, "tlacheck.old-2026-10-19"), filepath.Join(binDir, "age-keygen"),
		filepath.Join(unitDir, "nova-service-bus-hook.service"), filepath.Join(unitDir, "com.nova.loop.sweep.plist"), filepath.Join(unitDir, "nova-swarm-mirror-refresh.service"),
		filepath.Join(systemDir, "novadog-studio.service"), filepath.Join(systemDir, "com.nova.loop.old.plist"), filepath.Join(systemDir, "other.service"))
	code, out, _ = runFleet(t, bin, svc...)
	want := []string{"INSTALL localhost leftovers=8: com.nova.loop.old.plist, com.nova.loop.sweep.plist, mirror-refresh, nova-loop-sweep.timer, nova-service-bus-hook.service, nova-swarm-mirror-refresh.service, novadog-studio.service, winlock.bak-4slots",
		`nova-fleet services -- -l localhost -e '{"nova_retire_units":["bus-hook","old","sweep"]}'`}
	if runtime.GOOS == "linux" {
		want = []string{"INSTALL localhost leftovers=7: com.nova.loop.sweep.plist, mirror-refresh, nova-loop-sweep.timer, nova-service-bus-hook.service, nova-swarm-mirror-refresh.service, novadog-studio.service, winlock.bak-4slots",
			`nova-fleet services -- -l localhost -e '{"nova_retire_units":["bus-hook","sweep"]}'`}
	}
	want = append(want, "INSTALL localhost WARNING unrecorded=4: muse, nova-card, nova-fleet.bak, tlacheck.old-2026-10-19;",
		`nova-fleet install --tag local -- -l localhost -e '{"nova_tools_remove_enabled":true,"nova_tools_remove":["mirror-refresh","winlock.bak-4slots"],"nova_tools_remove_units":["nova-swarm-mirror-refresh.service","novadog-studio.service"]}'`)
	for _, w := range want {
		if code == 0 || !strings.Contains(out, w) {
			t.Errorf("leftovers beside the install: exit %d, want %q:\n%s", code, w, out)
		}
	}
	if strings.Contains(out, "by hand") || strings.Contains(out, "no play retires") {
		t.Errorf("the refusal names something no play retires, or sends the operator to do it by hand:\n%s", out)
	}
	for _, f := range []string{"nova-card", "mirror-refresh", "nova-fleet.bak"} {
		if _, err := os.Stat(filepath.Join(binDir, f)); err != nil {
			t.Errorf("the role removed %s: it reports and refuses only", f)
		}
	}
	// The printed install command runs: mirror-refresh and the v1 units move into
	// retired-<date>/ beside them; what is left is the services play's to retire, and the
	// warned binaries stay where they are.
	m = regexp.MustCompile(`nova-fleet install --tag local -- -l localhost -e '(\{[^']*\})'`).FindStringSubmatch(want[len(want)-1])
	code, out, _ = runFleet(t, bin, append(svc, "-e", m[1])...)
	left := "INSTALL localhost leftovers=4: com.nova.loop.old.plist, com.nova.loop.sweep.plist, nova-loop-sweep.timer, nova-service-bus-hook.service;"
	if runtime.GOOS == "linux" {
		left = "INSTALL localhost leftovers=3: com.nova.loop.sweep.plist, nova-loop-sweep.timer, nova-service-bus-hook.service;"
	}
	if code == 0 || !strings.Contains(out, left) {
		t.Errorf("the printed install command with v1 units: exit %d, want %q:\n%s", code, left, out)
	}
	for _, g := range []string{filepath.Join(unitDir, "retired-*", "nova-swarm-mirror-refresh.service"), filepath.Join(systemDir, "retired-*", "novadog-studio.service"),
		filepath.Join(binDir, "retired-*", "mirror-refresh"), filepath.Join(binDir, "retired-*", "winlock.bak-4slots")} {
		if got, _ := filepath.Glob(g); len(got) != 1 {
			t.Errorf("not moved aside: %s (%v)", g, got)
		}
	}
	for _, f := range []string{"nova-card", "muse", "nova-fleet.bak", "tlacheck.old-2026-10-19", "age-keygen"} {
		if _, err := os.Stat(filepath.Join(binDir, f)); err != nil {
			t.Errorf("%s moved: only retired names are retired", f)
		}
	}
	// Another repository's release records its names and prints no INSTALL line.
	code, out, _ = runFleet(t, bin, append(svc, "-e", "nova_tools_repo=mas-bandwidth/other")...)
	if got, err := os.ReadFile(filepath.Join(manifests, "mas-bandwidth_other.names")); code != 0 || err != nil || string(got) != "nova-fleet\n" || strings.Contains(out, "INSTALL localhost") {
		t.Errorf("a --repo install: exit %d, manifest %q (%v), want nova-fleet and no INSTALL line:\n%s", code, got, err, out)
	}
	for _, f := range append(allowed, filepath.Join(binDir, "nova-card"), filepath.Join(binDir, "mirror-refresh"), filepath.Join(binDir, "nova-fleet.bak"),
		filepath.Join(unitDir, "nova-loop-sweep.timer"), filepath.Join(unitDir, "nova-service-bus-hook.service"), filepath.Join(unitDir, "com.nova.loop.sweep.plist"),
		filepath.Join(systemDir, "novadog-studio.service"), filepath.Join(systemDir, "com.nova.loop.old.plist"), filepath.Join(unitDir, "nova-swarm-mirror-refresh.service"), filepath.Join(binDir, "age-keygen"),
		filepath.Join(binDir, "muse"), filepath.Join(binDir, "tlacheck.old-2026-10-19")) {
		if err := os.Remove(f); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dist, asset), append(b, 0), 0o755); err != nil {
		t.Fatal(err)
	}
	code, out, _ = runFleet(t, bin, args...)
	if code == 0 || !strings.Contains(out, "does not match its SHA256SUMS line") {
		t.Errorf("a file that does not match its line: exit %d, want the refusal:\n%s", code, out)
	}
}

// github-runners asks GitHub for its token with the control node's gh under --check too, and a refusal says why.
func TestRunnerTokenRequestFailsWithItsReason(t *testing.T) {
	if _, err := exec.LookPath("ansible-playbook"); err != nil {
		t.Skip("ansible-core is not installed on this machine")
	}
	bin := buildFleet(t)
	gh := filepath.Join(t.TempDir(), "gh")
	if err := os.WriteFile(gh, []byte("#!/bin/sh\necho 'To get started with GitHub CLI, please run:  gh auth login' >&2\nexit 4\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	inv := filepath.Join(t.TempDir(), "hosts.ini")
	if err := os.WriteFile(inv, []byte("[runner]\nlocalhost ansible_connection=local ansible_python_interpreter=auto_silent runners=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, errs := runFleet(t, bin, "github-runners", "--check", "--inventory", inv, "--", "-e", "github_runner_url=https://github.com/o/r",
		"-e", "github_runner_scope=repo", "-e", "github_runner_gh="+gh, "-e", "github_runner_base_dir="+t.TempDir())
	if code == 0 || !strings.Contains(out, "GITHUB RUNNER TOKEN localhost: gh api on the control node failed, rc=4") || !strings.Contains(out, "please run:  gh auth login") || strings.Contains(out, "censored") {
		t.Fatalf("--check with no gh login: exit %d, want the token refusal with its reason:\n%s\n%s", code, out, errs)
	}
}

// check on a real machine: every line is CHECK OK or CHECK MISSING with a fix, and an
// empty HOME is a fresh node: no store, key, seat or pins line, and the one TAILSCALE_API_KEY line that makes
// them, so the exit is 1 with the count on stderr.
func TestCheckOnThisMachine(t *testing.T) {
	bin := buildFleet(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "check")
	var env []string
	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, "NOVA_SECRETS_") && !strings.HasPrefix(e, "HOME=") {
			env = append(env, e)
		}
	}
	cmd.Env = append(env, "HOME="+t.TempDir())
	var o, e bytes.Buffer
	cmd.Stdout, cmd.Stderr = &o, &e
	err := cmd.Run()
	var ee *exec.ExitError
	if !errors.As(err, &ee) || ee.ExitCode() != 1 || !strings.HasPrefix(e.String(), "CHECK FAILED missing=") {
		t.Fatalf("check with no store: %v, stderr %q", err, e.String())
	}
	for _, line := range strings.Split(strings.TrimSpace(o.String()), "\n") {
		// CHECK traffic and CHECK WARN traffic appear on a runner that is on the tailnet (D8); they never count as missing.
		if !strings.HasPrefix(line, "CHECK OK ") && !strings.HasPrefix(line, "CHECK NOTE ") && !strings.HasPrefix(line, "CHECK traffic ") && !strings.HasPrefix(line, "CHECK WARN traffic ") && !(strings.HasPrefix(line, "CHECK MISSING ") && strings.Contains(line, ": ")) {
			t.Errorf("not a CHECK line: %q", line)
		}
	}
	for _, w := range []string{"CHECK MISSING store", "CHECK MISSING key", "CHECK MISSING seat", "CHECK MISSING pins", "CHECK OK store"} {
		if strings.Contains(o.String(), w) {
			t.Errorf("a fresh node has %q:\n%s", w, o.String())
		}
	}
	if !strings.Contains(o.String(), "CHECK MISSING TAILSCALE_API_KEY: nova-secrets set TAILSCALE_API_KEY  # paste the token when it asks") {
		t.Errorf("no TAILSCALE_API_KEY line:\n%s", o.String())
	}
	t.Logf("\n%s%s", o.String(), e.String())
}

// A child killed by a signal is reported as such, with exit 128+N, never as a plain 1.
func TestSignalReported(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no signals")
	}
	if code, err := runCmd("sh", []string{"-c", "kill -9 $$"}, os.Environ()); err != nil || code != 137 {
		t.Errorf("killed child: code %d err %v, want 137", code, err)
	}
}
