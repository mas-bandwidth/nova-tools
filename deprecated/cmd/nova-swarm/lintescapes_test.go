package main

import (
	"strings"
	"testing"
)

// EMMA'S ITEM-4 ESCAPES, AT THE VERB (#1853).
//
// The typed-header rules live in internal/swarm and the PATHS: validator in
// internal/hygiene. These four are the card writer's half: `lint --card` names the
// escape on a LINT DRIFT line and exits 2, so a card that would have printed
// `LINT OK checks=16` is refused before any spend. One test per class.

func lintEscapeCard(t *testing.T, name string, header ...string) (string, int) {
	t.Helper()
	card := writeLintCard(t, name, typedCardText(t, header...))
	exit, stdout, stderr := runSwarm(t, "lint", "--card", card, "--max", "0")
	if stderr != "" && exit != 2 {
		t.Fatalf("stderr on a lint: %q", stderr)
	}
	return stdout, exit
}

// #1853.1 A WINDOWS DRIVE LETTER IS AN ABSOLUTE PATH. `PATHS: C:/foo/bar` used to
// lint clean as repo-relative because the copy of the glob rule only looked for a
// leading `/` or `\`.
func TestLintCardRefusesAWindowsDriveLetterPath(t *testing.T) {
	t.Parallel()

	stdout, exit := lintEscapeCard(t, "drive.card",
		"KIND: fix-red",
		"PATHS: C:/foo/bar",
		"TEST: ./internal/x TestA",
	)
	if exit != 2 {
		t.Fatalf("a drive-letter PATHS: is a drift, exit %d\n%s", exit, stdout)
	}
	if !strings.Contains(stdout, "paths-declared") {
		t.Fatalf("the drift is paths-declared:\n%s", stdout)
	}
	if !strings.Contains(stdout, "C:/foo/bar") {
		t.Fatalf("the drift names the glob:\n%s", stdout)
	}
}

// #1853.2 THE CAP ON PATHS: IS EIGHT (SPEC-TOOLWORK.md:579-580). Nine globs used to
// lint clean; a bound that names nine files has stopped bounding anything.
func TestLintCardRefusesMoreThanEightPathGlobs(t *testing.T) {
	t.Parallel()

	nine := "p1/**, p2/**, p3/**, p4/**, p5/**, p6/**, p7/**, p8/**, p9/**"
	stdout, exit := lintEscapeCard(t, "nine.card",
		"KIND: fix-red",
		"PATHS: "+nine,
		"TEST: ./internal/x TestA",
	)
	if exit != 2 {
		t.Fatalf("nine PATHS: globs are a drift, exit %d\n%s", exit, stdout)
	}
	if !strings.Contains(stdout, "paths-declared") {
		t.Fatalf("the drift is paths-declared:\n%s", stdout)
	}

	eight := "p1/**, p2/**, p3/**, p4/**, p5/**, p6/**, p7/**, p8/**"
	stdout, exit = lintEscapeCard(t, "eight.card",
		"KIND: fix-red",
		"PATHS: "+eight,
		"TEST: ./internal/x TestA",
	)
	if exit != 0 {
		t.Fatalf("eight globs is the cap and is clean, exit %d\n%s", exit, stdout)
	}
}

// #1853.3 A COMMA-ONLY PATHS: LINE DECLARES NOTHING. `PATHS: , , ` is not
// `PATHS: none` and it is not a list of globs.
func TestLintCardRefusesACommaOnlyPathsLine(t *testing.T) {
	t.Parallel()

	stdout, exit := lintEscapeCard(t, "commas.card",
		"KIND: fix-red",
		"PATHS: , , ",
		"TEST: ./internal/x TestA",
	)
	if exit != 2 {
		t.Fatalf("a comma-only PATHS: is a drift, exit %d\n%s", exit, stdout)
	}
	if !strings.Contains(stdout, "paths-declared") {
		t.Fatalf("the drift is paths-declared:\n%s", stdout)
	}
}

// #1853.4 AN UNKNOWN KIND IS NOT A KIND. `KIND: completely-unknown-kind` used to
// lint clean (LINT OK checks=16) because the typed header asked only that the
// line exist. The name set is internal/hygiene/kinds.txt.
func TestLintCardRefusesAnUnknownKind(t *testing.T) {
	t.Parallel()

	stdout, exit := lintEscapeCard(t, "unknown.card",
		"KIND: completely-unknown-kind",
		"PATHS: internal/hygiene/glob.go",
		"TEST: internal/hygiene TestValidatePathsRefusesAWindowsDriveLetter",
	)
	if exit != 2 {
		t.Fatalf("an unknown KIND: is a drift, exit %d\n%s", exit, stdout)
	}
	if !strings.Contains(stdout, "kind-declared") {
		t.Fatalf("the drift is kind-declared:\n%s", stdout)
	}
	if !strings.Contains(stdout, "completely-unknown-kind") {
		t.Fatalf("the drift names the kind:\n%s", stdout)
	}
}
