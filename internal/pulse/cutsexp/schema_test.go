package cutsexp

import (
	"strings"
	"testing"
)

func TestSchemaCellsOnePerOpenLeaf(t *testing.T) {
	roadmap := `((title "Nova Tools Roadmap")
 (sprint 1 (status done))
 (sprint 2 (status in-progress))
 (leg go (
	(task "task 1" (status open))
	(task "task 2" (status done))
	(task "task 3" (status open) (
		(task "subtask 1" (status open))
	))
 )))
 (leg py (
	(task "task 4" (status open))
 ))
)`
	r := strings.NewReader(roadmap)
	cells, err := CutFromSchema(r, "go")
	if err != nil {
		t.Fatalf("CutFromSchema failed: %v", err)
	}

	// DONE-WHEN: "one lint-clean cell card per open leaf task"
	// "a done task yields no card"
	//
	// In the 'go' leg:
	// - "task 1" is an open leaf. -> 1 cell
	// - "task 2" is done. -> 0 cells
	// - "task 3" is open but not a leaf. -> 0 cells
	// - "subtask 1" is an open leaf. -> 1 cell
	//
	// So for leg 'go', we expect 2 cells.
	expected := 2
	if len(cells) != expected {
		t.Errorf("got %d cells, want %d", len(cells), expected)
	}

	// Verify that the cells are the actual leaf tasks, not the parent container.
	wantCells := []string{"task 1", "subtask 1"}
	if len(cells) == len(wantCells) {
		for i, want := range wantCells {
			if cells[i].Title != want {
				t.Errorf("cell[%d] title = %q, want %q", i, cells[i].Title, want)
			}
		}
	}
}
