package main

import (
	"reflect"
	"strings"
	"testing"
)

func TestUpdateAdoptionMatrix(t *testing.T) {
	t.Run("status validation", func(t *testing.T) {
		for _, s := range []string{"adopted", "trying", "declined", "unknown"} {
			input := "emma nova-bus " + s
			if s == "adopted" {
				input += " bus:note:1"
			}
			if _, err := ParseAdoptionMatrix(input + "\n"); err != nil {
				t.Fatalf("status %q refused: %v", s, err)
			}
		}
		for _, s := range []string{"maybe", "done", "ADOPTED", "adopted-already", "unknown-1"} {
			if _, err := ParseAdoptionMatrix("emma nova-bus " + s); err == nil {
				t.Fatalf("status %q accepted", s)
			}
		}
	})

	t.Run("adopted evidence", func(t *testing.T) {
		for _, good := range []string{"bus:note:12", "receipt:abc123", "nova-bus 2.1.0 darwin/arm64"} {
			if _, err := ParseAdoptionMatrix("emma nova-bus adopted " + good); err != nil {
				t.Fatalf("evidence %q refused: %v", good, err)
			}
		}
		if _, err := ParseAdoptionMatrix("emma nova-bus adopted"); err == nil {
			t.Fatal("adopted with no evidence accepted")
		}
	})

	t.Run("ask lines", func(t *testing.T) {
		m, err := ParseAdoptionMatrix(strings.Join([]string{
			"emma nova-bus adopted bus:note:1",
			"emma nova-tokens trying",
			"glenn nova-wake declined",
			"rowan nova-swarm unknown",
		}, "\n"))
		if err != nil {
			t.Fatal(err)
		}
		got := GenerateAskLines(m)
		want := []string{
			"ASK line=emma tool=nova-tokens status=trying",
			"ASK line=glenn tool=nova-wake status=declined",
			"ASK line=rowan tool=nova-swarm status=unknown",
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("ASK lines %v, want %v", got, want)
		}
	})

	t.Run("exit code", func(t *testing.T) {
		cases := []struct {
			name  string
			input string
			want  int
		}{
			{"resolved adopted and declined", "a x adopted bus:1\nb y declined", 0},
			{"trying present", "a x adopted bus:1\nb y trying", 1},
			{"unknown present", "a x unknown", 1},
			{"declined only", "a x declined", 0},
			{"adopted only", "a x adopted bus:1", 0},
			{"empty", "", 0},
		}
		for _, tc := range cases {
			m, err := ParseAdoptionMatrix(tc.input)
			if err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			if got := EvaluateAdoptionExitCode(m); got != tc.want {
				t.Fatalf("%s: exit %d, want %d", tc.name, got, tc.want)
			}
		}
	})
}
