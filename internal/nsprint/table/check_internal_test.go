package table

import (
	"testing"
)

func TestCompareCellsIdentical(t *testing.T) {
	a := "name | up | desired\nbench:b1 | up | 4\n"
	b := "name | up | desired\nbench:b1 | up | 4\n"
	if got := compareCells(a, b); got != "" {
		t.Fatalf("identical: got %q", got)
	}
}

func TestCompareCellsMismatch(t *testing.T) {
	a := "name | up | desired\nbench:b1 | up | 4\n"
	b := "name | up | desired\nbench:b1 | up | 99\n"
	got := compareCells(a, b)
	if got == "" {
		t.Fatal("expected mismatch, got none")
	}
}

func TestCompareCellsDifferentLineCount(t *testing.T) {
	a := "name | up\nbench:b1 | up\n"
	b := "name | up\n"
	got := compareCells(a, b)
	if got == "" {
		t.Fatal("expected line count mismatch, got none")
	}
}

func TestCompareCellsDifferentCellCount(t *testing.T) {
	a := "name | up | desired\n"
	b := "name | up\n"
	got := compareCells(a, b)
	if got == "" {
		t.Fatal("expected cell count mismatch, got none")
	}
}
