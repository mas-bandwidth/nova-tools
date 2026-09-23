package main

import (
	"strings"
	"testing"
)

// TEST-COMMAND ACCEPTS MORE THAN ONE VOCABULARY (issue #1994).
//
// The `test-command` check read a card's bytes through `cardCommandRE`, a four-line whitelist:
// `go test`, `go vet`, `pytest`, `cargo test`, `npm test`, or the words `no tests`. That was
// the gate shape when practice 5 was first written, and it held for a while: every leg in
// `ci-fast.yml` was a `go test`, and a card that named one named the right one.
//
// Then `ci-fast.yml` moved to `make <leg>` (the Makefile is the one entry for build, test
// and lint per AGENTS.md rule 6), and the whitelist kept the same four lines. 155 of 176
// polyglot cards in the `mas-bandwidth/schema` lane tripped on a verbatim `make
// tables-...` gate that the repository would actually run:
//
//	LINT DRIFT card=card-schema14-w6-cs.md test-command: 1: no test command named
//
// The drift names the wrong fix. "Write the gate verbatim" is already done -- the card
// carried the make target exactly as `ci-fast.yml:598-612` runs it. "Say in words that there
// are no tests" would be a lie: the cards have tests, run by make. The only way the
// linter was clearing them was a command the repository would not actually run, which is
// how a wave of cards shipped with `ok ... [no tests to run]` because the test name was
// spelled for the wrong leg. The check is about the linter's vocabulary, not the card's.
//
// THE ACCEPTED SET WIDENS, AND THE REMEDY NAMES IT. A card is now `test-command`-clean when
// it names any command the toolchains it cites can run: `make`, `gmake`, the four it already
// took (`go test`, `go vet`, `pytest`, `cargo test`, `npm test`), the other common runners
// (`dotnet test`, `ctest`, `mvn test`, `gradle test`), the script-and-runner shapes
// (`bash <script>`, `./<script>`), or the words `no tests`. The remedy on the drift says
// the set, so a card writer on a bench with a stale clone reads what the binary actually
// takes. The existing tests on the four-line set stay green by the same logic -- the
// check still accepts them -- and the new tests below are the ones the widening lights up.

// lintCmdCard is a card with one `TEST: ` step line whose body is the verb under test. It
// carries every shape lint --card requires except the test-command verb, which is the
// one under test, so each test below can vary the verb and read its verdict.
func lintCmdCard(t *testing.T, name, verb string) (string, int, string) {
	t.Helper()
	body := strings.Join([]string{
		"RESULT: CARD-1994 widen the test-command check",
		"STEP 1. pwd && git clone -q https://example.com/nova-tools.git repo && cd repo",
		"STEP 2. Write a red test named TestThing.",
		"STEP 3. Run: " + verb,
		"STEP 4. finish within 20 minutes.",
		"STEP 5. Write RESULT.md: line 1 is the RESULT: line above.",
		"",
	}, "\n")
	card := writeLintCard(t, name, body)
	exit, stdout, _ := runSwarm(t, "lint", "--card", card)
	return stdout, exit, body
}

// The make gate the polyglot lane actually ran. A `make <target>` line was the gate in
// `ci-fast.yml:598-612` for every schema-leg card; the linter refused the verbatim copy
// and 155 cards drifted on it.
func TestLintAcceptsAMakeTestCommand(t *testing.T) {
	stdout, exit, _ := lintCmdCard(t, "make.card", "make tables-cs-leg-debug")
	if exit != 0 {
		t.Fatalf("a `make <target>` gate is the gate `ci-fast.yml` runs; lint must accept it, got %d\n%s", exit, stdout)
	}
	if strings.Contains(stdout, "test-command") {
		t.Fatalf("`make <target>` is not a `test-command` drift:\n%s", stdout)
	}
}

// `gmake` is the BSD make on macOS benches; AGENTS.md says benches run the card, and a
// macOS bench that copied `make` runs `gmake`. Accepting only one spelling would refuse the
// gate on the very bench the card names.
func TestLintAcceptsAGmakeTestCommand(t *testing.T) {
	stdout, exit, _ := lintCmdCard(t, "gmake.card", "gmake tables-rust-fixedform")
	if exit != 0 {
		t.Fatalf("a `gmake` gate is the BSD-make gate on macOS benches; lint must accept it, got %d\n%s", exit, stdout)
	}
	if strings.Contains(stdout, "test-command") {
		t.Fatalf("`gmake <target>` is not a `test-command` drift:\n%s", stdout)
	}
}

// The other runners the polyglot lane measured. `dotnet test`, `ctest`, `mvn test` and
// `gradle test` were each probed on 2026-09-19 and came back DRIFT; a card that names
// one as the gate the CI runs is the card the lint must accept.
func TestLintAcceptsCommonPolyglotRunners(t *testing.T) {
	cases := []struct {
		name, verb string
	}{
		{"dotnet.card", "dotnet test"},
		{"ctest.card", "ctest"},
		{"mvn.card", "mvn test"},
		{"gradle.card", "gradle test"},
		{"pytest.card", "pytest -q"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stdout, exit, _ := lintCmdCard(t, tc.name, tc.verb)
			if exit != 0 {
				t.Fatalf("`%s` is a valid gate; lint must accept it, got %d\n%s", tc.verb, exit, stdout)
			}
			if strings.Contains(stdout, "test-command") {
				t.Fatalf("`%s` is not a `test-command` drift:\n%s", tc.verb, stdout)
			}
		})
	}
}

// `bash <script>` and a bare `./<script>` are how a script-driven gate looks in a card,
// and they were on the DRIFT side of the same probe table.
func TestLintAcceptsScriptStyleTestCommands(t *testing.T) {
	cases := []struct {
		name, verb string
	}{
		{"bash.card", "bash run-tests.sh"},
		{"script.card", "./run-tests.sh"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stdout, exit, _ := lintCmdCard(t, tc.name, tc.verb)
			if exit != 0 {
				t.Fatalf("`%s` is a valid gate; lint must accept it, got %d\n%s", tc.verb, exit, stdout)
			}
			if strings.Contains(stdout, "test-command") {
				t.Fatalf("`%s` is not a `test-command` drift:\n%s", tc.verb, stdout)
			}
		})
	}
}

// A `cargo run` is not a test command -- it is what the recipe does AFTER the tests --
// and a card that names it is not declaring a gate. `cargo run --quiet` was on the DRIFT
// side of the probe and stays there: the check is about a TEST command, and widening to
// any `cargo` line would clear cards whose gate the repository never runs.
func TestLintStillRefusesCargoRunNotCargoTest(t *testing.T) {
	stdout, exit, _ := lintCmdCard(t, "cargorun.card", "cargo run --quiet")
	if exit != 2 {
		t.Fatalf("`cargo run` is not a gate, the card drifts at exit 2, got %d\n%s", exit, stdout)
	}
	if !strings.Contains(stdout, "LINT DRIFT card=cargorun.card test-command:") {
		t.Fatalf("the drift is test-command, named by token:\n%s", stdout)
	}
	if !strings.Contains(stdout, "no test command named") {
		t.Fatalf("the drift says the verb in plain text:\n%s", stdout)
	}
}

// The drift's remedy names the wider set so a card writer on a bench with a stale clone
// can read what the binary actually takes without grepping the source. The remedy line
// is part of the contract; a check whose drift offers an out-of-date list sends the
// writer down the wrong path.
func TestLintTestCommandRemedyNamesTheAcceptedSet(t *testing.T) {
	stdout, exit, _ := lintCmdCard(t, "remedy.card", "node test/x.mjs")
	if exit != 2 {
		t.Fatalf("`node test/x.mjs` is not on the accepted list, drifts at exit 2, got %d\n%s", exit, stdout)
	}
	drift := ""
	for _, line := range strings.Split(strings.TrimSuffix(stdout, "\n"), "\n") {
		if strings.HasPrefix(line, "LINT DRIFT ") && strings.Contains(line, "test-command:") {
			drift = line
			break
		}
	}
	if drift == "" {
		t.Fatalf("a card with no accepted gate drifts on test-command, no drift line:\n%s", stdout)
	}
	for _, want := range []string{"make", "pytest", "cargo test", "npm test", "no tests"} {
		if !strings.Contains(drift, want) {
			t.Errorf("remedy names %q so a writer reads the binary's accepted set; got:\n%s", want, drift)
		}
	}
}

// EDGE CASES: the widen-the-whitelist fix must NOT widen the false-positive set. A card
// whose text happens to contain a substring of one of the new verbs stays a drift, and a
// verb that is a build command rather than a test command stays a drift.
func TestLintTestCommandRefusesSubstringsAndFalsePositives(t *testing.T) {
	cases := []struct {
		name, verb string
	}{
		// `remake` is a word, not a verb the linter accepts. The fix must not let it
		// through because `make` is in the accepted set.
		{"remake-prose", "this fix is a remake of the earlier 1244 attempt"},
		// `mvn` is OK only with the `test` argument; a bare `mvn package` is a build
		// command, not a gate, and stays a drift.
		{"mvn-package", "mvn package"},
		// `cargo build` is a build command, not a gate.
		{"cargo-build", "cargo build"},
		// `go build` is a build command, not a gate. The old regex accepted `go test`
		// and `go vet`; it did not accept `go build` and the new one must not either.
		// The `./...` ellipsis must not let the build line through `./<script>`.
		{"go-build", "go build ./..."},
		// `make` without a target is a build invocation, not a gate.
		{"make-no-target", "make"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, exit, _ := lintCmdCard(t, tc.name+".card", tc.verb)
			if exit != 2 {
				t.Fatalf("`%s` is not a test command and stays a drift, got exit %d", tc.verb, exit)
			}
		})
	}
}

// `make <target>` where the target contains a hyphen must match. The polyglot lane's
// make targets are hyphenated (`tables-cs-leg-debug`), and the regex's target character
// class must include `-` so a hyphenated target is not rejected on its own merits.
func TestLintAcceptsHyphenatedMakeTargets(t *testing.T) {
	cases := []string{
		"make tables-cs-leg-debug",
		"make tables-rust-fixedform",
		"make test-e2e",
		"make lint-fix",
		"gmake a-b-c-d-e",
	}
	for _, verb := range cases {
		t.Run(strings.ReplaceAll(verb, " ", "_"), func(t *testing.T) {
			stdout, exit, _ := lintCmdCard(t, "hyphen.card", verb)
			if exit != 0 {
				t.Fatalf("`%s` is a valid gate, got exit %d\n%s", verb, exit, stdout)
			}
		})
	}
}

// `make` with a path-shaped target (`make ./scripts/test.sh`) must also match; the
// hyphen and the slash are both in the target character class.
func TestLintAcceptsMakeWithPathTarget(t *testing.T) {
	stdout, exit, _ := lintCmdCard(t, "path.card", "make ./scripts/test.sh")
	if exit != 0 {
		t.Fatalf("`make ./scripts/test.sh` is a valid gate, got exit %d\n%s", exit, stdout)
	}
}
