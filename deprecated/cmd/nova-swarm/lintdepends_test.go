package main

import (
	"path/filepath"
	"strings"
	"testing"
)

// #2636, THE LINT THIRD OF `nova-swarm lint --card --typed`. Three refusals and two
// passes. The card's own id is the first word of lintGoodCard's contract line,
// CARD-0000. The remedy on every refusal is `DEPENDS-ON: <card-id>[, ...] or DEPENDS-ON: -`.

func dependsOnCard(t *testing.T, name, depends string) string {
	t.Helper()
	header := []string{
		"KIND: fix-red",
		"PATHS: internal/swarm/lintheader.go",
		"TEST: internal/swarm TestCardHeaderFullHeaderDrawsNothing",
		"LEGS: go",
		"SOURCE: mas-bandwidth/nova-tools#2636",
	}
	if depends != "" {
		header = append(header, depends)
	}
	return writeLintCard(t, name, typedCardText(t, header...))
}

func dependsLineup(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "ORDER.tsv")
	write(t, p, body)
	return p
}

const dependsRemedy = "DEPENDS-ON: <card-id>[, ...] or DEPENDS-ON: -"

func TestLintTypedRefusesACardWithNoDependsOn(t *testing.T) {
	t.Parallel()

	card := dependsOnCard(t, "nokey.card", "")
	exit, stdout, stderr := runSwarm(t, "lint", "--card", card, "--typed", "--max", "0")
	if exit != 2 {
		t.Fatalf("a typed card with no DEPENDS-ON key exits 2, got %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	}
	if !strings.Contains(stdout, "depends-on:") || !strings.Contains(stdout, "DEPENDS-ON") {
		t.Fatalf("the refusal names the key:\n%s", stdout)
	}
	if !strings.Contains(stdout, dependsRemedy) {
		t.Fatalf("the refusal names the remedy %q:\n%s", dependsRemedy, stdout)
	}
	// The same card, not asked to be typed, is the card cut before the key existed.
	if exit, stdout, stderr = runSwarm(t, "lint", "--card", card, "--max", "0"); exit != 0 || strings.Contains(stdout, "depends-on") {
		t.Fatalf("without --typed a card is not refused for a missing DEPENDS-ON: exit %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	}
}

func TestLintTypedRefusesASelfDependency(t *testing.T) {
	t.Parallel()

	card := dependsOnCard(t, "self.card", "DEPENDS-ON: other-card, CARD-0000")
	lineup := dependsLineup(t, "id\tdepends-on\nother-card\t-\n")
	exit, stdout, stderr := runSwarm(t, "lint", "--card", card, "--typed", "--lineup", lineup, "--max", "0")
	if exit != 2 {
		t.Fatalf("a self-dependency exits 2, got %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	}
	if !strings.Contains(stdout, "depends-on:") || !strings.Contains(stdout, "CARD-0000") || !strings.Contains(stdout, "own id") {
		t.Fatalf("the refusal names the card's own id:\n%s", stdout)
	}
	if strings.Contains(stdout, "not in the lineup") {
		t.Fatalf("a self-dependency is not an unknown id:\n%s", stdout)
	}
	if !strings.Contains(stdout, dependsRemedy) {
		t.Fatalf("the refusal names the remedy:\n%s", stdout)
	}
}

func TestLintTypedRefusesAnUnknownDependsOnID(t *testing.T) {
	t.Parallel()

	card := dependsOnCard(t, "unknown.card", "DEPENDS-ON: missing-card")
	lineup := dependsLineup(t, "id\tdepends-on\nother-card\t-\n")
	exit, stdout, stderr := runSwarm(t, "lint", "--card", card, "--typed", "--lineup", lineup, "--max", "0")
	if exit != 2 {
		t.Fatalf("an unknown id exits 2, got %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	}
	if !strings.Contains(stdout, "depends-on:") || !strings.Contains(stdout, "missing-card") || !strings.Contains(stdout, "not in the lineup") {
		t.Fatalf("the refusal names the id and the lineup:\n%s", stdout)
	}
	if !strings.Contains(stdout, dependsRemedy) {
		t.Fatalf("the refusal names the remedy:\n%s", stdout)
	}
}

func TestLintTypedDependsOnDashPasses(t *testing.T) {
	t.Parallel()

	card := dependsOnCard(t, "dash.card", "DEPENDS-ON: -")
	lineup := dependsLineup(t, "id\tdepends-on\nother-card\t-\n")
	exit, stdout, stderr := runSwarm(t, "lint", "--card", card, "--typed", "--lineup", lineup, "--max", "0")
	if exit != 0 || !strings.Contains(stdout, "LINT OK card=dash.card") {
		t.Fatalf("DEPENDS-ON: - passes, exit %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	}
	if strings.Contains(stdout, "depends-on") {
		t.Fatalf("a dash is not a drift:\n%s", stdout)
	}
}

func TestLintTypedDependsOnReferencePasses(t *testing.T) {
	t.Parallel()

	card := dependsOnCard(t, "ref.card", "DEPENDS-ON: mas-bandwidth/nova-tools#2550")
	lineup := dependsLineup(t, "id\tdepends-on\nother-card\t-\n")
	exit, stdout, stderr := runSwarm(t, "lint", "--card", card, "--typed", "--lineup", lineup, "--max", "0")
	if exit != 0 || !strings.Contains(stdout, "LINT OK card=ref.card") {
		t.Fatalf("owner/repo#n passes and is not looked up in the lineup, exit %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	}
	if strings.Contains(stdout, "depends-on") || strings.Contains(stdout, "mas-bandwidth/nova-tools#2550") {
		t.Fatalf("a reference is not a drift:\n%s", stdout)
	}
}

func TestLintTypedRefusesASpaceAndDogfood(t *testing.T) {
	t.Parallel()

	lineup := dependsLineup(t, "id\tdepends-on\nother-card\t-\n")
	card := dependsOnCard(t, "space.card", "DEPENDS-ON: nova-tools #2550")
	exit, stdout, stderr := runSwarm(t, "lint", "--card", card, "--typed", "--lineup", lineup, "--max", "0")
	if exit != 2 || !strings.Contains(stdout, "depends-on:") || !strings.Contains(stdout, "nova-tools #2550") {
		t.Fatalf("nova-tools #2550 (a space) is refused by name, exit %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	}
	card = dependsOnCard(t, "dog.card", "DEPENDS-ON: dogfood")
	exit, stdout, stderr = runSwarm(t, "lint", "--card", card, "--typed", "--lineup", lineup, "--max", "0")
	if exit != 2 || !strings.Contains(stdout, "depends-on:") || !strings.Contains(stdout, "dogfood") {
		t.Fatalf("dogfood is refused by name, exit %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	}
}

func TestLintTypedDependsOnKnownIDPasses(t *testing.T) {
	t.Parallel()

	card := dependsOnCard(t, "known.card", "DEPENDS-ON: other-card, third-card")
	lineup := dependsLineup(t, "id\tdepends-on\nother-card\t-\nthird-card\tother-card\n")
	exit, stdout, stderr := runSwarm(t, "lint", "--card", card, "--typed", "--lineup", lineup, "--max", "0")
	if exit != 0 || !strings.Contains(stdout, "LINT OK card=known.card") {
		t.Fatalf("an id the lineup holds passes, exit %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	}
	if strings.Contains(stdout, "depends-on") {
		t.Fatalf("a known id is not a drift:\n%s", stdout)
	}
}
