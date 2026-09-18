package main

// `nova-pulse fleet certify`: the flags, the two seams, and the launchd agent that runs the
// verb with nobody watching.
//
// Glenn, 2026-09-18: "certify fleet machines", and then "we want this certification to be
// *mechanized*". The verb is the primitive; the loop is the deliverable, so the plist is
// held here by a test as well, byte for byte against the command it is meant to run --
// a timer that runs a command nobody checked is a timer that runs the wrong command
// quietly, every six hours.
//
// Every test wires a Go fake into fleetNewCertifyRemote and fleetNewCertifyForge and a fixed
// instant into fleetNow. No process starts, no shell runs, nothing waits on a clock.

import (
	"bytes"
	"context"
	"encoding/xml"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/fleet"
)

// certifyFakeRemote answers every script from a table keyed by the workload marker, and
// records what it was sent. A target or a class it has no answer for gets what a machine
// says about a command it does not have, never a cheerful empty success.
type certifyFakeRemote struct {
	answers map[string]string

	mu      sync.Mutex
	scripts []string
}

func (f *certifyFakeRemote) Run(ctx context.Context, target, script string) (string, error) {
	f.mu.Lock()
	f.scripts = append(f.scripts, script)
	f.mu.Unlock()
	class := ""
	for _, line := range strings.Split(script, "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "# nova-certify workload "); ok {
			class = strings.TrimSpace(v)
			break
		}
	}
	if answer, ok := f.answers[target+"|"+class]; ok {
		return answer, nil
	}
	return "sh: " + class + ": command not found\n", context.Canceled
}

type certifyFakeForge struct{ runners []fleet.RunnerStatus }

func (f certifyFakeForge) Runners(string) ([]fleet.RunnerStatus, error) { return f.runners, nil }

// withCertifyFakes wires both seams and the clock, and restores them.
//
// It also says this machine is NO machine of the registry. That is a safety rule, not a
// convenience: the fleet's benches are called space, hulk and vision, the test registries
// name them, and a shard running on one of them would otherwise take the local path and run
// the real workloads against the real machine. A test that wants the local path says so by
// setting fleetLocalHost itself, and replaces the local runner with a fake in the same
// breath.
func withCertifyFakes(t *testing.T, remote fleet.Remote, forge fleet.Forge) {
	t.Helper()
	oldRemote, oldForge, oldNow := fleetNewCertifyRemote, fleetNewCertifyForge, fleetNow
	oldLocal, oldHost := fleetNewCertifyLocal, fleetLocalHost
	fleetNewCertifyRemote = func(string) fleet.Remote { return remote }
	fleetNewCertifyForge = func(time.Duration) fleet.Forge { return forge }
	fleetNewCertifyLocal = func() fleet.Remote { return remote }
	fleetLocalHost = func() string { return "" }
	fleetNow = func() time.Time { return time.Date(2026, 9, 18, 18, 0, 0, 0, time.UTC) }
	t.Cleanup(func() {
		fleetNewCertifyRemote, fleetNewCertifyForge, fleetNow = oldRemote, oldForge, oldNow
		fleetNewCertifyLocal, fleetLocalHost = oldLocal, oldHost
	})
}

func certifyFiles(t *testing.T) (machines, certs, standard string) {
	t.Helper()
	dir := t.TempDir()
	machines = filepath.Join(dir, "machines.tsv")
	certs = filepath.Join(dir, "certs.tsv")
	standard = filepath.Join(dir, "bench-standard.sh")
	body := strings.Join([]string{
		"space\tspace\tlinux/x64\tbench\trowan\t16\t-",
		"batman\tbatman\tdarwin/arm64\trunner\t-\t10\t-",
	}, "\n") + "\n"
	for path, content := range map[string]string{
		machines: body,
		certs:    "",
		standard: "echo STANDARD OK\n",
	} {
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return machines, certs, standard
}

// spaceAnswers is a bench that passes everything.
func spaceAnswers() map[string]string {
	return map[string]string{
		"space|build":          "nova-merge v0.17.0 linux/amd64\n",
		"space|go-test":        "GO OK go version go1.26.5 linux/amd64 ok 0.4s\n",
		"space|c-build":        "C OK cc 15.2.0\n",
		"space|cpp-build":      "CPP OK c++ 15.2.0\n",
		"space|sbcl":           "SBCL OK SBCL 2.6.0.debian\n",
		"space|git-push":       "GIT PUSH OK head=deadbeef git=git version 2.43.0\n",
		"space|path-resolves":  "PATH OK /home/ubuntu/.local/bin/nova-merge v0.17.0\n",
		"space|go-on-path":     "GO PATH OK /home/ubuntu/go/bin/go go version go1.26.5 linux/amd64\n",
		"space|git-identity":   "GIT IDENTITY OK Rowan Claude <rowan@mas-bandwidth.com>\n",
		"space|wall-toolchain": "WALL TOOLCHAIN OK go version go1.26.5 linux/amd64\n",
		"space|services-reach": "SERVICES OK name=space addr=100.115.99.19 redis=PONG loki=ready\n",
	}
}

func runCertifyVerb(t *testing.T, args ...string) (string, string, int) {
	t.Helper()
	var out, errs bytes.Buffer
	code := cmdFleetCertify(args, &out, &errs)
	return out.String(), errs.String(), code
}

// TestCertifyVerbWiresTheSeamsAndWritesTheRows is the verb end to end through its own flags.
func TestCertifyVerbWiresTheSeamsAndWritesTheRows(t *testing.T) {
	machines, certs, standard := certifyFiles(t)
	remote := &certifyFakeRemote{answers: spaceAnswers()}
	withCertifyFakes(t, remote, certifyFakeForge{})
	out, errs, code := runCertifyVerb(t, "--machines", machines, "--machine", "space",
		"--certs", certs, "--standard", standard)
	if code != 0 {
		t.Fatalf("exit = %d\nstdout:%s\nstderr:%s", code, out, errs)
	}
	if !strings.Contains(out, "CERTIFY space go-test OK evidence=") {
		t.Errorf("no go-test line:\n%s", out)
	}
	rows, err := fleet.ReadCertificates(certs)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 11 {
		t.Fatalf("wrote %d rows, want 11 (the bench classes)", len(rows))
	}
	// The standard hash is computed here, from the standard file and the workloads, and it
	// is the same on every row of one run.
	for _, r := range rows {
		if r.Hash == "" || r.Hash != rows[0].Hash {
			t.Fatalf("row %s carries hash %q, want the run's one hash %q", r.Class, r.Hash, rows[0].Hash)
		}
	}
}

// TestIfStaleSkipsAMachineWhoseEveryClassIsCurrent is the trigger the timer leans on: the
// common case is a fleet with nothing to do, and it must cost one file read and one build
// read per machine, never a fleet-wide pass.
func TestIfStaleSkipsAMachineWhoseEveryClassIsCurrent(t *testing.T) {
	machines, certs, standard := certifyFiles(t)
	remote := &certifyFakeRemote{answers: spaceAnswers()}
	withCertifyFakes(t, remote, certifyFakeForge{})
	args := []string{"--machines", machines, "--machine", "space", "--certs", certs, "--standard", standard}
	if _, errs, code := runCertifyVerb(t, args...); code != 0 {
		t.Fatalf("first run exit = %d: %s", code, errs)
	}
	first := len(remote.scripts)

	out, errs, code := runCertifyVerb(t, append(append([]string{}, args...), "--if-stale")...)
	if code != 0 {
		t.Fatalf("second run exit = %d\n%s%s", code, out, errs)
	}
	if !strings.Contains(out, "CERTIFY space CURRENT classes=11 build=v0.17.0") {
		t.Fatalf("the current machine was not skipped:\n%s", out)
	}
	if !strings.Contains(out, "skipped=1") {
		t.Errorf("the closing line does not count the skip:\n%s", out)
	}
	// One more script only: the `nova-merge version` read that decides currency.
	if got := len(remote.scripts) - first; got != 1 {
		t.Errorf("a skipped machine cost %d remote calls, want 1 (the build read)", got)
	}
}

// TestIfStaleRunsAMachineWhoseStandardMoved: the hash is what makes a certificate expire, so
// a workloads directory with one more card is a fleet with nothing current.
func TestIfStaleRunsAMachineWhoseStandardMoved(t *testing.T) {
	machines, certs, standard := certifyFiles(t)
	remote := &certifyFakeRemote{answers: spaceAnswers()}
	withCertifyFakes(t, remote, certifyFakeForge{})
	args := []string{"--machines", machines, "--machine", "space", "--certs", certs, "--standard", standard}
	if _, errs, code := runCertifyVerb(t, args...); code != 0 {
		t.Fatalf("first run exit = %d: %s", code, errs)
	}
	if err := os.WriteFile(standard, []byte("echo STANDARD OK\n# one more rule\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, _, _ := runCertifyVerb(t, append(append([]string{}, args...), "--if-stale")...)
	if strings.Contains(out, "CURRENT") {
		t.Fatalf("the machine was skipped although the standard moved:\n%s", out)
	}
	if !strings.Contains(out, "CERTIFY space go-test OK") {
		t.Fatalf("the machine was not re-certified:\n%s", out)
	}
}

// TestStatusReadsTheRecordAndReachesNoMachine.
func TestStatusReadsTheRecordAndReachesNoMachine(t *testing.T) {
	machines, certs, standard := certifyFiles(t)
	remote := &certifyFakeRemote{answers: spaceAnswers()}
	withCertifyFakes(t, remote, certifyFakeForge{})
	args := []string{"--machines", machines, "--machine", "space", "--certs", certs, "--standard", standard}
	if _, errs, code := runCertifyVerb(t, args...); code != 0 {
		t.Fatalf("first run exit = %d: %s", code, errs)
	}
	before := len(remote.scripts)

	out, errs, code := runCertifyVerb(t, "--machines", machines, "--certs", certs,
		"--standard", standard, "--status")
	if len(remote.scripts) != before {
		t.Errorf("--status reached a machine %d times", len(remote.scripts)-before)
	}
	if !strings.Contains(out, "CERTIFY STATUS space go-test OK build=v0.17.0 at=") {
		t.Errorf("no status line for a current class:\n%s", out)
	}
	// batman was never certified, so the fleet is not certified and --status says so.
	if code != 1 {
		t.Fatalf("exit = %d, want 1 (batman has no certificate at all)", code)
	}
	if !strings.Contains(errs, "CERTIFY STATUS batman runner-online NONE") {
		t.Errorf("a class nobody ran is not reported as NONE:\n%s", errs)
	}
	if !strings.Contains(errs, "CERTIFY STATUS FAIL current=11 stale=") {
		t.Errorf("no closing status line:\n%s", errs)
	}
}

// TestStatusAloneOrNotAtAll: --status reads the record, so pairing it with a machine or with
// the trigger is two verbs in one command and is refused.
func TestStatusAloneOrNotAtAll(t *testing.T) {
	machines, certs, standard := certifyFiles(t)
	withCertifyFakes(t, &certifyFakeRemote{}, certifyFakeForge{})
	_, errs, code := runCertifyVerb(t, "--machines", machines, "--certs", certs,
		"--standard", standard, "--status", "--all")
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(errs, "--status reads the record") {
		t.Errorf("the refusal does not say why:\n%s", errs)
	}
}

// TestTheLogFileCarriesOneStructuredEventPerCertificate: the same internal/log Emitter the
// launch verb writes through, so one dashboard reads every verb's stream.
func TestTheLogFileCarriesOneStructuredEventPerCertificate(t *testing.T) {
	machines, certs, standard := certifyFiles(t)
	events := filepath.Join(filepath.Dir(certs), "certify.log")
	withCertifyFakes(t, &certifyFakeRemote{answers: spaceAnswers()}, certifyFakeForge{})
	if _, errs, code := runCertifyVerb(t, "--machines", machines, "--machine", "space",
		"--certs", certs, "--standard", standard, "--log", events); code != 0 {
		t.Fatalf("exit = %d: %s", code, errs)
	}
	raw, err := os.ReadFile(events)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 11 {
		t.Fatalf("the stream holds %d events for 11 certificates", len(lines))
	}
	for _, want := range []string{
		`"source":"nova-pulse"`, `"verb":"certify"`, `"event":"certify"`, `"bench":"space"`,
	} {
		if !strings.Contains(lines[0], want) {
			t.Errorf("the event carries no %s:\n%s", want, lines[0])
		}
	}
	if strings.Count(lines[0], "\n") != 0 {
		t.Error("an event is more than one line")
	}
}

// TestNeitherMachineNorAllNorStatusIsARefusal.
func TestNeitherMachineNorAllNorStatusIsARefusal(t *testing.T) {
	machines, certs, standard := certifyFiles(t)
	withCertifyFakes(t, &certifyFakeRemote{}, certifyFakeForge{})
	_, errs, code := runCertifyVerb(t, "--machines", machines, "--certs", certs, "--standard", standard)
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(errs, "--machine") || !strings.Contains(errs, "--all") {
		t.Errorf("the refusal does not name both ways to say which machines:\n%s", errs)
	}
}

// ---------------------------------------------------------------------------
// the loop
// ---------------------------------------------------------------------------

// launchdPlist is enough of a property list to read the fields that matter. A full plist
// parser is a dependency this repository does not need: the four keys below are spelled the
// same way in every macOS this fleet runs on.
type launchdPlist struct {
	Keys   []string `xml:"dict>key"`
	Values []string `xml:"dict>string"`
	Arrays []struct {
		Strings []string `xml:"string"`
	} `xml:"dict>array"`
}

// TestTheLaunchdAgentRunsTheVerbTheLoopNeeds holds the plist against the command it is meant
// to run. A timer nobody reads is a timer that runs the wrong command quietly, every six
// hours, and the only cheap way to know is to read the argv in a test.
func TestTheLaunchdAgentRunsTheVerbTheLoopNeeds(t *testing.T) {
	path := filepath.Join(repoRootFromCmd(t), "fleet", "launchd", "com.rowan.fleet-certify.plist")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var p launchdPlist
	if err := xml.Unmarshal(raw, &p); err != nil {
		t.Fatalf("the plist is not well-formed XML, so launchd will not load it: %v", err)
	}
	if len(p.Arrays) == 0 {
		t.Fatal("the plist carries no ProgramArguments array")
	}
	argv := strings.Join(p.Arrays[0].Strings, " ")
	for _, want := range []string{
		"nova-pulse fleet certify",
		"--all",
		"--if-stale",
		"--machines /Users/glenn/rowan-working/queue/control/machines.tsv",
		"--certs /Users/glenn/rowan-working/queue/control/certs.tsv",
		"--log /Users/glenn/rowan-working/queue/control/certify.log",
		"--standard",
	} {
		if !strings.Contains(argv, want) {
			t.Errorf("the agent's command carries no %q:\n%s", want, argv)
		}
	}
	// THE ESCALATION HAS SOMEWHERE TO GO. `certify` does its repair round and then, for
	// whatever still fails, writes one CERTIFY ESCALATE line and sends one note. Without
	// `--bus`, `--as` and `--to` the note is never sent -- the run says
	// `escalation=unsent reason=no-bus` and carries on -- so the six-hourly agent would
	// find a broken bench, try the repairs, fail, and tell nobody. A line in a log file
	// nobody opens is the loop half-built: the whole reason to mechanize certification is
	// that a machine going bad reaches a person without one being at the terminal.
	for _, want := range []string{
		"--bus /Users/glenn/rowan-working/rowan-stella",
		"--as Rowan",
		"--to fleet",
	} {
		if !strings.Contains(argv, want) {
			t.Errorf("the agent cannot escalate: its command carries no %q:\n%s", want, argv)
		}
	}
	// --bus without --as, or without --to, is exit 2 at the flag check, which for a timer
	// is a refusal every six hours and no certification at all. The three travel together.
	hasBus := strings.Contains(argv, "--bus ")
	if hasBus != strings.Contains(argv, "--as ") || hasBus != strings.Contains(argv, "--to ") {
		t.Errorf("--bus, --as and --to do not all appear; `fleet certify` refuses that invocation at exit 2:\n%s", argv)
	}
	// Every flag in the argv must be one the verb actually takes, or the agent refuses with
	// exit 2 every six hours and the only record of it is a log nobody opens.
	for _, field := range p.Arrays[0].Strings {
		for _, word := range strings.Fields(field) {
			if !strings.HasPrefix(word, "--") {
				continue
			}
			if certifyFlag(t, strings.TrimPrefix(word, "--")) {
				continue
			}
			t.Errorf("the agent passes %s, which `fleet certify` does not take", word)
		}
	}
	if !strings.Contains(string(raw), "<key>Label</key><string>com.rowan.fleet-certify</string>") {
		t.Error("the agent has no label, or not the one it is installed under")
	}
	// Four calendar entries, six hours apart: "every 6 h" is four fixed times to launchd,
	// which has no interval form that survives a reboot the way this does.
	if n := strings.Count(string(raw), "<key>Hour</key>"); n != 4 {
		t.Errorf("the agent fires at %d times a day, want 4 (every six hours)", n)
	}
	// RunAtLoad false: installing the agent must not start a fleet-wide pass under the hand
	// that is installing it.
	if !strings.Contains(string(raw), "<key>RunAtLoad</key><false/>") {
		t.Error("the agent runs at load; installing it would start a fleet-wide pass")
	}
}

// certifyFlag says whether `fleet certify` declares a flag by that name.
func certifyFlag(t *testing.T, name string) bool {
	t.Helper()
	f := newFlags("fleet certify")
	var out bytes.Buffer
	// Parsing an empty argv declares nothing, so the flags are declared by running the verb
	// far enough to define them: cmdFleetCertify declares and then refuses. The refusal text
	// is what names the flags it does not have.
	_ = f
	_ = out
	var errs bytes.Buffer
	cmdFleetCertify([]string{"--" + name + "=x"}, &bytes.Buffer{}, &errs)
	return !strings.Contains(errs.String(), "flag provided but not defined")
}

// repoRootFromCmd walks up from the package directory to the checkout root.
func repoRootFromCmd(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 6; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		dir = filepath.Dir(dir)
	}
	t.Fatal("no go.mod above the working directory")
	return ""
}
