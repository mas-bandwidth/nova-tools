package pulse

// Class P's red tests (#828): an unstamped card is refused, a stamped one is admitted, and a
// stamped card whose body was edited after the stamp is refused.
// Class Q's red test: `cut` refuses a card whose STEP text says "whole".

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLaunchRefusesAnUnstampedCard is the whole of class P in three states.
func TestLaunchRefusesAnUnstampedCard(t *testing.T) {
	body := "RESULT: CARD-8 nova-tools #1 fixed with its red test first: a thing\nSOURCE: a b\n1. do it\n"
	stamped := Stamp(body, "v1.2.3")

	t.Run("unstamped is refused with the remedy", func(t *testing.T) {
		ok, refusal, _ := CardGate(body, nil, false)
		if ok {
			t.Fatal("a card nobody cut was admitted")
		}
		if !strings.Contains(refusal, "nova-pulse cut --kind") {
			t.Errorf("the refusal does not name the remedy: %q", refusal)
		}
	})
	t.Run("stamped is admitted", func(t *testing.T) {
		if ok, refusal, _ := CardGate(stamped, nil, false); !ok {
			t.Fatalf("a stamped card was refused: %s", refusal)
		}
		if got := CheckStamp(stamped).Version; got != "v1.2.3" {
			t.Errorf("the stamp names version %q, want v1.2.3", got)
		}
		if !strings.HasPrefix(lastLine(stamped), StampPrefix) {
			t.Errorf("the stamp is not the LAST line:\n%s", stamped)
		}
	})
	t.Run("edited after the stamp is refused", func(t *testing.T) {
		edited := strings.Replace(stamped, "1. do it\n", "1. do it\n2. and this too\n", 1)
		ok, refusal, _ := CardGate(edited, nil, false)
		if ok {
			t.Fatal("a card edited after it was cut was admitted")
		}
		if !strings.Contains(refusal, "edited after it was cut") {
			t.Errorf("the refusal does not say what happened: %q", refusal)
		}
	})
	t.Run("--unstamped-ok is the migration week and nothing more", func(t *testing.T) {
		ok, _, note := CardGate(body, nil, true)
		if !ok {
			t.Fatal("--unstamped-ok did not admit an unstamped card")
		}
		if !strings.Contains(note, "migration week") {
			t.Errorf("the admission is not logged as a migration: %q", note)
		}
		edited := strings.Replace(stamped, "1. do it", "1. do something else", 1)
		if ok, _, _ := CardGate(edited, nil, true); ok {
			t.Error("--unstamped-ok admitted an EDITED card; it is for unsigned cards only")
		}
	})
}

// TestCutKindStampsTheCard is the other end of class P: the one cutter signs what it wrote.
func TestCutKindStampsTheCard(t *testing.T) {
	queue, out := t.TempDir(), t.TempDir()
	mustWrite(t, filepath.Join(queue, "state.tsv"), "next_card\t42\n")
	var stdout, stderr strings.Builder
	code := CutKind(CutKindInput{
		Kind: "fix", Repo: "mas-bandwidth/nova-tools", Issue: 7, Title: "a thing",
		Out: out, Queue: queue, Version: "v9", Stdout: &stdout, Stderr: &stderr,
	})
	if code != 0 {
		t.Fatalf("CutKind = %d: %s", code, stderr.String())
	}
	raw, err := os.ReadFile(filepath.Join(out, "card-42.md"))
	if err != nil {
		t.Fatal(err)
	}
	chk := CheckStamp(string(raw))
	if !chk.Valid {
		t.Fatalf("the cutter did not sign its own card: %s", chk.Reason)
	}
	if chk.Version != "v9" {
		t.Errorf("the stamp names version %q, want v9", chk.Version)
	}
}

// TestCutRefusesWhole is class Q on a fixture card: a step that asks for a whole file is
// refused with one line naming the range or the rule that replaces it.
func TestCutRefusesWhole(t *testing.T) {
	queue, out, fix := t.TempDir(), t.TempDir(), t.TempDir()
	mustWrite(t, filepath.Join(queue, "state.tsv"), "next_card\t50\n")

	cut := func(steps string) (int, string) {
		body := filepath.Join(fix, "body.md")
		mustWrite(t, body, steps)
		var stdout, stderr strings.Builder
		code := CutKind(CutKindInput{
			Kind: "fix", Repo: "mas-bandwidth/nova-tools", Issue: 7, Title: "a thing",
			BodyFile: body, Out: out, Queue: queue, Version: "v9",
			Stdout: &stdout, Stderr: &stderr,
		})
		return code, stderr.String()
	}

	code, errs := cut("1. clone the repo\n2. read docs/SPEC-WORK.md whole and hold every rule\n")
	if code != 2 {
		t.Fatalf("a card saying \"whole\" was cut (exit %d)", code)
	}
	if !strings.Contains(errs, "CUT REFUSED") || !strings.Contains(errs, "--spec-lines") || !strings.Contains(errs, "--rule spec:n") {
		t.Errorf("the refusal does not name the range or the rule:\n%s", errs)
	}
	if n := len(strings.Split(strings.TrimRight(errs, "\n"), "\n")); n != 1 {
		t.Errorf("the refusal is %d lines, want one:\n%s", n, errs)
	}

	if code, errs := cut("1. clone the repo\n2. read docs/SPEC-WORK.md entirely\n"); code != 2 {
		t.Errorf("a card saying \"read <file> entirely\" was cut (exit %d): %s", code, errs)
	}
	if code, errs := cut("1. clone the repo\n2. read docs/SPEC-WORK.md lines 120-164\n"); code != 0 {
		t.Errorf("a card naming a line range was refused (exit %d): %s", code, errs)
	}
}
