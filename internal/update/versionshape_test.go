//go:build unix

package update

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The two dogfood hurts of 2026-09-18, red before the repair. Every binary here
// is a shell stub, so this file is unix-only and touches no network.

// TestSnapshotAcceptsAToolThatSaysOneMoreTrueThing is #1297.
//
// `nova-version snapshot --bin ~/.local/bin --out /tmp/a.tsv` exited 2 with
// "cannot read nova-merge version (it printed no four-token version line)"
// because nova-merge prints a fifth `build=<hex>` token -- extra metadata, not
// a broken binary. One tool adding a field must never refuse the whole bin.
func TestSnapshotAcceptsAToolThatSaysOneMoreTrueThing(t *testing.T) {
	const stamp = "v0.15.3-0.20260918044559-d576bf6bbabb"
	bin := t.TempDir()
	specStub(t, bin, "nova-bus", "nova-bus "+stamp+" darwin/arm64 go1.27.1")
	specStub(t, bin, "nova-merge", "nova-merge "+stamp+" darwin/arm64 go1.27.1 build=9c1885748f57")
	specStub(t, bin, "nova-sandbox", "nova-sandbox "+stamp+" darwin/arm64 go1.27.1 backend=sandbox-exec platform=darwin")
	out := filepath.Join(t.TempDir(), "snapshot.tsv")
	code, stdout, stderr := specRun(t, Environment{}, "snapshot", "--bin", bin, "--out", out)
	if code != 0 {
		t.Fatalf("snapshot refused a bin holding a tool with extras: exit %d\n%s", code, stderr)
	}
	need(t, stdout, "SNAPSHOT OK", "tools=3", "stamp="+field(stamp))
	body := string(readFileOrFail(t, out))
	for _, want := range []string{
		"nova-bus\t" + stamp + "\td576bf6bbabb\tdarwin/arm64",
		"nova-merge\t" + stamp + "\td576bf6bbabb\tdarwin/arm64",
		"nova-sandbox\t" + stamp + "\td576bf6bbabb\tdarwin/arm64",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("snapshot did not record %q:\n%s", want, body)
		}
	}
	// The extras are metadata about one tool, not a column of the set: the
	// stamp every row carries is the same, so the mixed-set gate still reads
	// field two and nothing else.
	if strings.Contains(body, "build=") || strings.Contains(body, "backend=") {
		t.Errorf("an extra leaked into the snapshot's own columns:\n%s", body)
	}
}

// TestSnapshotStillRefusesALineThatIsNotAVersionLine keeps the repair from
// being "accept anything": a binary that prints a usage refusal is still a
// binary this verb cannot read, and saying so is the whole job.
func TestSnapshotStillRefusesALineThatIsNotAVersionLine(t *testing.T) {
	bin := t.TempDir()
	specStub(t, bin, "nova-bus", "nova-bus v1 darwin/arm64 go1.27.1")
	specStub(t, bin, "nova-broken", "nova-broken: no verb given; run: nova-broken help")
	out := filepath.Join(t.TempDir(), "snapshot.tsv")
	code, _, stderr := specRun(t, Environment{}, "snapshot", "--bin", bin, "--out", out)
	if code != 2 {
		t.Fatalf("exit %d, want 2; stderr=%s", code, stderr)
	}
	need(t, stderr, "SNAPSHOT REFUSED", "nova-broken", "go build ./cmd/nova-broken")
	if _, err := os.Stat(out); err == nil {
		t.Error("a refused snapshot wrote its --out")
	}
}

// TestReportAsksOurOwnToolsTheVerbTheyAnswer is #1264.
//
// A manifest row whose installed column is the executable itself -- which is
// what `nova-version snapshot` and every friend's hand-written manifest hold --
// ran the binary BARE. Every nova tool answers a bare invocation with a usage
// refusal, so every one of our own tools reported UNKNOWN. A tool is asked
// `version`, then `--version`, then bare, and the first answer that carries a
// version line is the reading.
func TestReportAsksOurOwnToolsTheVerbTheyAnswer(t *testing.T) {
	const stamp = "v0.15.3-0.20260918044559-d576bf6bbabb"
	bin := t.TempDir()
	// Answers `version` and refuses anything else, exactly like a nova tool.
	novaish := specScript(t, bin, "nova-swarm", `case "$1" in
version) printf '%s\n' 'nova-swarm `+stamp+` darwin/arm64 go1.27.1' ;;
*) printf '%s\n' 'nova-swarm: no verb given; run: nova-swarm help' >&2; exit 2 ;;
esac`)
	// Answers only `--version`, the other spelling a friend's tool may hold.
	dashed := specScript(t, bin, "nova-secrets", `case "$1" in
--version) printf '%s\n' 'nova-secrets `+stamp+` darwin/arm64 go1.27.1 build=9c1885748f57' ;;
*) printf '%s\n' 'nova-secrets: no verb given' >&2; exit 2 ;;
esac`)
	// Answers bare, the way a foreign tool does: the ladder must not break it.
	bare := specStub(t, bin, "nova-foreign", "nova-foreign 1.2.3 darwin/arm64 go1.27.1")

	file := manifest(t,
		row("nova-swarm", "tool", novaish, "local:"+novaish, "none"),
		row("nova-secrets", "tool", dashed, "local:"+dashed, "none"),
		row("nova-foreign", "tool", bare, "local:"+bare, "none"),
	)
	code, stdout, stderr := specRun(t, Environment{}, "report", "--file", file)
	if strings.Contains(stdout, "REPORT UNKNOWN") {
		t.Errorf("report ran our own tools bare and could not read them:\n%s", stdout)
	}
	for _, want := range []string{
		"REPORT TOOL name=nova-swarm",
		"REPORT TOOL name=nova-secrets",
		"REPORT TOOL name=nova-foreign",
		"known=3",
		"unknown=0",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("missing %q in:\n%s\n%s", want, stdout, stderr)
		}
	}
	if code != 0 {
		t.Errorf("exit %d, want 0; stderr=%s", code, stderr)
	}
}

// TestReportStillRefusesAToolThatAnswersNothing: the ladder tries three
// invocations, and a tool that answers none of them is still UNKNOWN with the
// remedy naming what to do -- a ladder that invents a reading is worse than the
// bare run it replaced.
func TestReportStillRefusesAToolThatAnswersNothing(t *testing.T) {
	bin := t.TempDir()
	mute := specScript(t, bin, "nova-mute", `printf '%s\n' 'nova-mute: no verb given' >&2; exit 2`)
	file := manifest(t, row("nova-mute", "tool", mute, "local:"+mute, "none"))
	code, stdout, stderr := specRun(t, Environment{}, "report", "--file", file)
	need(t, stdout, "REPORT UNKNOWN name=nova-mute")
	// The closing count is on stderr when the check ran and failed, which is where a
	// FAIL line belongs.
	need(t, stdout+stderr, "unknown=1")
	if code != 1 {
		t.Errorf("exit %d, want 1 (the check ran and failed)", code)
	}
}

// TestReportRunsAnExplicitArgvExactlyAsWritten: a manifest that names its own
// argv -- `go version`, `sops --version` -- is the caller's sentence and the
// ladder never appends to it.
func TestReportRunsAnExplicitArgvExactlyAsWritten(t *testing.T) {
	bin := t.TempDir()
	// Refuses any argument at all: proof that nothing was appended.
	strict := specScript(t, bin, "strict", `if [ $# -ne 1 ] || [ "$1" != "report" ]; then printf 'argv was %s\n' "$*" >&2; exit 3; fi
printf '%s\n' 'strict 4.5.6'`)
	file := manifest(t, row("strict", "tool", strict+" report", "local:"+strict, "none"))
	code, stdout, stderr := specRun(t, Environment{}, "report", "--file", file)
	if code != 0 || strings.Contains(stdout, "UNKNOWN") {
		t.Fatalf("the ladder rewrote an explicit argv: exit %d\n%s\n%s", code, stdout, stderr)
	}
	need(t, stdout, "version=4.5.6")
}

func readFileOrFail(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
