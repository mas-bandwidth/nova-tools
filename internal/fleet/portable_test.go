package fleet

// The portability class, driven red first over throwaway bodies, then over the set that
// SHIPS. Nothing here runs a program, opens a socket or reaches a machine: the checker reads
// text, and so does its test.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// bodyWorkload is a workload made of nothing but a body, which is all the checker reads.
func bodyWorkload(class, body string) Workload {
	return Workload{Class: class, Source: "testdata/" + class + ".card", Body: body}
}

// TestAGNUOnlySpellingIsRefusedWithItsPortableRemedy is the shape of every finding: the
// spelling, the line it is on, and what to write instead. A refusal that does not say what
// to type is a refusal a person works around.
func TestAGNUOnlySpellingIsRefusedWithItsPortableRemedy(t *testing.T) {
	for _, tc := range []struct {
		name   string
		body   string
		want   string
		remedy string
	}{
		{"find -printf", "set -eu\no=$(find \"$d\" -type f -printf '%T@\\n' | sort -n)\n", "find -printf", "stat -f %m"},
		{"getent", "set -eu\nADDR=$(getent hosts \"$HOST\" | awk '{print $1}')\n", "getent", "dscacheutil"},
		{"nproc", "set -eu\nCORES=$(nproc)\n", "nproc", "hw.ncpu"},
		{"which", "set -eu\nGO=$(which go)\n", "which", "command -v"},
		{"go --version", "set -eu\ngo --version\n", "go --version", "go version"},
		{"java --version", "set -eu\njava --version\n", "java --version", "java -version"},
		{"sha256sum", "set -eu\nsha256sum probe.c\n", "sha256sum", "shasum -a 256"},
		{"df -B", "set -eu\ndf -BG /\n", "df -B", "df -k"},
		{"time -f", "set -eu\n/usr/bin/time -f '%e' go build ./...\n", "time -f", "harness"},
		{"/proc", "set -eu\ncat /proc/cpuinfo\n", "/proc", "sysctl"},
		{"stat -c", "set -eu\nstat -c %Y \"$f\"\n", "stat -c", "BSD stat is -f"},
		{"ldd", "set -eu\nldd ./probe\n", "ldd", "otool -L"},
		{"readlink -f", "set -eu\nGO=$(readlink -f \"$g\")\n", "readlink -f", "fallback"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := CheckPortability(bodyWorkload("probe", tc.body))
			if len(got) == 0 {
				t.Fatalf("%q was not refused; a body nobody refuses is a body that dies inside a worker", tc.body)
			}
			var found *PortabilityFinding
			for i := range got {
				if got[i].Spelling == tc.want {
					found = &got[i]
				}
			}
			if found == nil {
				t.Fatalf("refused %v, want a %q finding", got, tc.want)
			}
			if found.Line != 2 {
				t.Errorf("the finding is on line %d, want 2; a finding nobody can locate is a finding nobody fixes", found.Line)
			}
			if !strings.Contains(found.Remedy, tc.remedy) {
				t.Errorf("the remedy %q does not name %q", found.Remedy, tc.remedy)
			}
			if !strings.Contains(found.String(), tc.want) {
				t.Errorf("the line %q does not name the spelling", found.String())
			}
		})
	}
}

// TestALineThatNamesBothHalvesIsThePortableSpelling: the rule refuses an ASSUMPTION, not a
// word. `nproc` beside `hw.ncpu` under a `uname -s` test IS the portable spelling, and a
// checker that refused it would teach people to write the unportable half instead.
func TestALineThatNamesBothHalvesIsThePortableSpelling(t *testing.T) {
	for _, body := range []string{
		"set -eu\nCORES=$(if [ \"$(uname -s)\" = Darwin ]; then sysctl -n hw.ncpu; else nproc; fi)\n",
		"set -eu\nif [ \"$(uname -s)\" = Darwin ]; then M=\"stat -f %m\"; else M=\"stat -c %Y\"; fi\n",
		"set -eu\nGO=$(readlink -f \"$g\" 2>/dev/null || echo \"$g\")\n",
	} {
		if got := CheckPortability(bodyWorkload("probe", body)); len(got) != 0 {
			t.Errorf("%q was refused:\n%v\nthat line IS the portable spelling", body, got)
		}
	}
}

// TestAOneOSToolIsAllowedWhenTheBodyGuardsIt: the guard is usually three lines above the
// use, so the tools are body-scoped where the spellings are line-scoped.
func TestAOneOSToolIsAllowedWhenTheBodyGuardsIt(t *testing.T) {
	guarded := "set -eu\nif command -v systemctl >/dev/null 2>&1; then\n  systemctl --user list-units\nelif command -v launchctl >/dev/null 2>&1; then\n  launchctl list\nfi\n"
	if got := CheckPortability(bodyWorkload("probe", guarded)); len(got) != 0 {
		t.Errorf("a guarded systemctl/launchctl pair was refused:\n%v", got)
	}
	bare := "set -eu\nsystemctl --user list-units\n"
	got := CheckPortability(bodyWorkload("probe", bare))
	if len(got) != 1 || got[0].Spelling != "systemctl" {
		t.Fatalf("an unguarded systemctl was not refused: %v", got)
	}
	if !strings.Contains(got[0].Remedy, "launchd") {
		t.Errorf("the remedy %q does not name the other half", got[0].Remedy)
	}
}

// TestACommentIsNotTheFault: every card in this set EXPLAINS in its header the spellings it
// refuses. A checker that read the explanation as the fault would refuse every card that
// documents itself -- and the first thing people would do is delete the explanation.
func TestACommentIsNotTheFault(t *testing.T) {
	body := "# nproc and getent are linux-only; this card uses neither\nset -eu\necho ok\n"
	if got := CheckPortability(bodyWorkload("probe", body)); len(got) != 0 {
		t.Errorf("a comment was read as the fault:\n%v", got)
	}
}

// TestTheAllowlistIsMatchedByClassAndSpellingAndNeverByLine is SPEC-CI's shared convention:
// a line-matched list turns dev red the first time an edit above it shifts a line.
func TestTheAllowlistIsMatchedByClassAndSpellingAndNeverByLine(t *testing.T) {
	allowed, err := ParsePortabilityAllowlist("# a comment\n\nprobe nproc\n")
	if err != nil {
		t.Fatal(err)
	}
	loads := []Workload{
		bodyWorkload("probe", "set -eu\n\n\nCORES=$(nproc)\n"),
		bodyWorkload("other", "set -eu\nCORES=$(nproc)\n"),
	}
	findings, spent := CheckPortabilityOf(loads, allowed)
	if len(findings) != 1 || findings[0].Class != "other" {
		t.Fatalf("findings = %v; the allowance covers probe whatever line it is on, and covers nothing else", findings)
	}
	if spent[PortabilityAllowance{Class: "probe", Spelling: "nproc"}] != 1 {
		t.Errorf("the allowance was not counted as spent; a stale allowance nobody counts is one nobody removes")
	}
	if _, err := ParsePortabilityAllowlist("probe\n"); err == nil {
		t.Error("a malformed allowlist line was accepted; an allowlist nobody can parse allows everything")
	}
}

// TestTheAllowlistThatShipsIsEmpty. It is shrink-only, and it starts at nothing.
func TestTheAllowlistThatShipsIsEmpty(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "workload_portability_allowlist.txt"))
	if err != nil {
		t.Fatal(err)
	}
	allowed, err := ParsePortabilityAllowlist(string(raw))
	if err != nil {
		t.Fatal(err)
	}
	if len(allowed) != 0 {
		t.Errorf("the allowlist that ships carries %d entries: %v; it is shrink-only and empty today", len(allowed), allowed)
	}
}

// TestEveryEmbeddedWorkloadIsPortable is THE CLASS TEST: the set that ships is held to the
// template class of #1415, because the registry now names a darwin bench and the router
// picks the machine long after the workload was written.
//
// A run that reads NO workload is red. That is how the embedded set going missing -- a
// renamed directory, a dropped //go:embed line -- is found, rather than being reported as a
// clean pass over nothing.
func TestEveryEmbeddedWorkloadIsPortable(t *testing.T) {
	loads, err := StandardWorkloads()
	if err != nil {
		t.Fatal(err)
	}
	if len(loads) == 0 {
		t.Fatal("the embedded set is empty; a checker that reads nothing passes everything")
	}
	raw, err := os.ReadFile(filepath.Join("testdata", "workload_portability_allowlist.txt"))
	if err != nil {
		t.Fatal(err)
	}
	allowed, err := ParsePortabilityAllowlist(string(raw))
	if err != nil {
		t.Fatal(err)
	}
	findings, spent := CheckPortabilityOf(loads, allowed)
	for _, f := range findings {
		t.Errorf("CERTIFY-PORTABLE REFUSED %s", f)
	}
	for _, a := range allowed {
		if spent[a] == 0 {
			t.Errorf("the allowance %s %s is stale; the list is shrink-only", a.Class, a.Spelling)
		}
	}
	t.Logf("CERTIFY-PORTABLE OK workloads=%d allowlisted=%d refused=%d", len(loads), len(spent), len(findings))
}

// TestTheDarwinBenchCanAnswerEveryWorkloadItIsGiven names, one by one, what the first
// certification of the M2 Air found. Each of these is a line a darwin bench needs and a
// linux bench already had; two of the three were SILENT passes before.
func TestTheDarwinBenchCanAnswerEveryWorkloadItIsGiven(t *testing.T) {
	for _, tc := range []struct{ class, want, why string }{
		{"diag-size", "uname", "the oldest-log read must choose its stat spelling at runtime; BSD find has no -printf and the rate was silently the size"},
		{"services-reach", "dscacheutil", "there is no getent on darwin, and the Air's /etc/hosts does carry space"},
		{"runner-path", "LaunchAgents", "a LOADED launchd job and one that SURVIVES A REBOOT are two different things"},
		{"wall-toolchain", "Cellar", "brew's go is a symlink out of /opt/homebrew/bin into the Cellar tree, which is the root the wall must grant"},
		{"sbcl", "Cellar", "sbcl on a darwin bench is brew's, not one under ~/sdk"},
		{"go-test", "Cellar", "the go a darwin bench builds with lives in the Cellar tree"},
	} {
		t.Run(tc.class, func(t *testing.T) {
			if body := workloadBody(t, tc.class); !strings.Contains(body, tc.want) {
				t.Errorf("%s never names %q: %s\n%s", tc.class, tc.want, tc.why, body)
			}
		})
	}
}

// TestTheWallWorkloadsNameTheDarwinToolchainRootsAsReads is #1419's rule inside
// certification: a Mac's toolchains are installed and on PATH and STILL die inside a bare
// wall, because each resolves its runtime from the directory of the launcher that ran it and
// that launcher is a symlink out of any granted tree.
func TestTheWallWorkloadsNameTheDarwinToolchainRootsAsReads(t *testing.T) {
	loads, err := StandardWorkloads()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]string{
		"go-test":        {"/opt/homebrew/Cellar/go"},
		"wall-toolchain": {"/opt/homebrew/Cellar/go", "/opt/homebrew/Cellar/sbcl"},
		"sbcl":           {"/opt/homebrew/Cellar/sbcl"},
	}
	seen := map[string]bool{}
	for _, w := range loads {
		roots, ok := want[w.Class]
		if !ok {
			continue
		}
		seen[w.Class] = true
		if !w.Wall {
			t.Errorf("%s is not a wall workload any more; the darwin roots are about the wall", w.Class)
		}
		for _, root := range roots {
			found := false
			for _, r := range w.Reads {
				if r == root {
					found = true
				}
			}
			if !found {
				t.Errorf("%s does not name %s as a read root; brew's launcher is a symlink into the Cellar tree and the grant is checked against the resolved target (reads=%v)",
					w.Class, root, w.Reads)
			}
		}
	}
	for class := range want {
		if !seen[class] {
			t.Errorf("no %s workload in the embedded set", class)
		}
	}
}
