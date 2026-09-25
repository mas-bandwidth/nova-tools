package tokens

import (
	"strings"
	"testing"
)

// TestPublishFrictionsAppend pins the append-only transition that `publish --frictions`
// enforces: a published frictions file may only grow at its tail, never change, shrink or
// shuffle what is already there.

func TestPublishFrictionsAppend(t *testing.T) {
	existing := FormatFrictionsPublication([]string{
		"gap=build issue=#1 cost=~",
		"gap=review issue=#2 cost=12000",
	})

	t.Run("allows appending new records at the end", func(t *testing.T) {
		next := FormatFrictionsPublication([]string{
			"gap=build issue=#1 cost=~",
			"gap=review issue=#2 cost=12000",
			"gap=review issue=#3 cost=800",
			"gap=adoption issue=#4 cost=~",
		})
		if err := ValidateFrictionsAppendTransition(existing, next); err != nil {
			t.Fatalf("appending valid records is refused: %v", err)
		}
	})

	t.Run("accepts a first publication from empty", func(t *testing.T) {
		next := FormatFrictionsPublication([]string{"gap=build issue=#1 cost=~"})
		if err := ValidateFrictionsAppendTransition("", next); err != nil {
			t.Fatalf("a first publication onto an empty file is refused: %v", err)
		}
	})

	t.Run("rejects a mutation of an existing record", func(t *testing.T) {
		next := FormatFrictionsPublication([]string{
			"gap=build issue=#1 cost=~",
			"gap=review issue=#2 cost=99999", // the cost changed
		})
		err := ValidateFrictionsAppendTransition(existing, next)
		if err == nil {
			t.Fatal("a mutation of an existing record was accepted")
		}
		if !strings.Contains(err.Error(), "record 2") {
			t.Errorf("the error names the wrong record: %v", err)
		}
	})

	t.Run("rejects a deletion of an existing record", func(t *testing.T) {
		next := FormatFrictionsPublication([]string{
			"gap=build issue=#1 cost=~",
		})
		err := ValidateFrictionsAppendTransition(existing, next)
		if err == nil {
			t.Fatal("a deletion of an existing record was accepted")
		}
		if !strings.Contains(err.Error(), "removed") {
			t.Errorf("the error does not say the record was removed: %v", err)
		}
	})

	t.Run("rejects a reordering of existing records", func(t *testing.T) {
		next := FormatFrictionsPublication([]string{
			"gap=review issue=#2 cost=12000",
			"gap=build issue=#1 cost=~",
		})
		err := ValidateFrictionsAppendTransition(existing, next)
		if err == nil {
			t.Fatal("a reordering of existing records was accepted")
		}
		if !strings.Contains(err.Error(), "reordered or changed") {
			t.Errorf("the error does not say the record was reordered or changed: %v", err)
		}
	})
}
