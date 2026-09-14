package main

import "testing"

func TestExactRevisionAndSelectorRefusals(t *testing.T) {
	if err := exactHead("0123456789abcdef0123456789abcdef01234567"); err != nil {
		t.Fatal(err)
	}
	if err := exactHead("0123456789ab"); err == nil {
		t.Fatal("short sha accepted")
	}
	if _, err := entry(0, ""); err == nil {
		t.Fatal("missing selector accepted")
	}
	if _, err := entry(7, "topic"); err == nil {
		t.Fatal("two selectors accepted")
	}
}

func TestPolicyActorCannotWaiveOwnRow(t *testing.T) {
	if !has(names("emma, stella"), "stella") {
		t.Fatal("name parsing lost a reader")
	}
}
