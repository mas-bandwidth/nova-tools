package card_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// adHocScript matches a go-redis call that sends EVAL or EVALSHA (Eval,
// EvalSha, EvalRO, EvalShaRO, a redis.NewScript's Run/Load), a Script* call,
// or a raw Do whose first argument is an EVAL/EVALSHA/SCRIPT command name. A
// bare "script" literal elsewhere is not a Redis command (KindScript, the
// card KIND in kinds.go, is one), so the command-name form is anchored on Do.
var adHocScript = regexp.MustCompile(`redis\.NewScript\(|\.Eval(Sha)?(RO|Ro)?\(|\.Script(Load|Exists|Flush|Kill)\(|\.Do\(\s*ctx\s*,\s*"(?i:evalsha|eval|eval_ro|evalsha_ro|script)"`)

// TestCardPackageSendsNoAdHocScript is the #3419 source control: no non-test
// Go file of package card sends an ad-hoc script. Every atomic step of the
// card path (claim, launched, beat, end, result, push, release, land, drain's
// resume and import receipt) is a nova_sprint Function called with FCALL,
// because the fleet bench seat may FCALL and never EVAL/EVALSHA.
// TestCardPathMakesNoEvalCall and TestDrainReleasesAndImportsOnce are the
// runtime controls on a real server's command counters.
func TestCardPackageSendsNoAdHocScript(t *testing.T) {
	t.Parallel()

	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		body, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		checked++
		for i, line := range strings.Split(string(body), "\n") {
			code, _, _ := strings.Cut(line, "//")
			if adHocScript.MatchString(code) {
				t.Errorf("%s:%d sends an ad-hoc script; make it a nova_sprint Function and FCALL it: %s", f, i+1, strings.TrimSpace(line))
			}
		}
	}
	if checked == 0 {
		t.Fatal("no Go source found in package card")
	}
}
