package workreconcile

import (
	"strings"
	"testing"
)

const sampleSprintText = `
NOVA SWARM SPRINT: PRIORITY LIST
Written 2026-09-20 14:30Z by Rowan. Repo: mas-bandwidth/nova-tools. dev = a3abdd4a.

----------------------------------------------------------------------------------
A. RELIABLE: no paid card lost, doubled, or unbounded. (Gate to running wide.)
----------------------------------------------------------------------------------
 1. #1945  fill: fail-closed load, capacity from the slot store
           STATE: DONE 2026-09-20 14:54Z. Landed in 16aq (dev 86abcf23); adopted on all seven
 2. #2029  fill: one dealer at a time; the same registry seat for probe, count, launch
           STATE: Stella HOLD with two failing controls written.
 3. #2045 + #2040  launch: attempt identity and fencing; an unseen start is UNKNOWN and
           is never requeued; two attempts total, provider failures counted; launcher output kept.
 4. #1984  harvest drains only its own finished cards (the 151-marker drain, #1950)
 5. #1615 #1635 #1712  the spend cap enforced by machinery, usage recorded on every path

----------------------------------------------------------------------------------
C. BETTER: what comes back can be trusted.
----------------------------------------------------------------------------------
15. schema#1376  go leg: a card-added test runs in no ci-fast job, so green proves nothing.
`

func TestParseSprintPriorities(t *testing.T) {
	priorities, err := ParseSprintPriorities(sampleSprintText, "mas-bandwidth/nova-tools")
	if err != nil {
		t.Fatalf("ParseSprintPriorities failed: %v", err)
	}

	if len(priorities) != 6 {
		t.Fatalf("expected 6 priority rows, got %d", len(priorities))
	}

	// Row 1: #1945
	if priorities[0].Rank != 1 || len(priorities[0].Refs) != 1 || priorities[0].Refs[0].IssueNumber != 1945 {
		t.Errorf("row 1 mismatch: %+v", priorities[0])
	}
	if priorities[0].Refs[0].Repo != "mas-bandwidth/nova-tools" {
		t.Errorf("row 1 repo mismatch: %s", priorities[0].Refs[0].Repo)
	}

	// Row 3: #2045 + #2040
	if priorities[2].Rank != 3 || len(priorities[2].Refs) != 2 {
		t.Errorf("row 3 mismatch: %+v", priorities[2])
	}
	if priorities[2].Refs[0].IssueNumber != 2045 || priorities[2].Refs[1].IssueNumber != 2040 {
		t.Errorf("row 3 issue refs mismatch: %+v", priorities[2].Refs)
	}

	// Row 5: #1615 #1635 #1712
	if priorities[4].Rank != 5 || len(priorities[4].Refs) != 3 {
		t.Errorf("row 5 mismatch: %+v", priorities[4])
	}

	// Row 15: schema#1376
	if priorities[5].Rank != 15 || len(priorities[5].Refs) != 1 {
		t.Errorf("row 15 mismatch: %+v", priorities[5])
	}
	if priorities[5].Refs[0].Repo != "mas-bandwidth/schema" || priorities[5].Refs[0].IssueNumber != 1376 {
		t.Errorf("row 15 ref mismatch: %+v", priorities[5].Refs[0])
	}
}

func TestSprintPriorityQueryCompletion(t *testing.T) {
	store := NewStore()

	// Import issue nodes:
	// Issue #1945 is CLOSED
	uid1, _ := MintUID()
	store.AddNode(&WorkNode{
		UID:         uid1,
		Repo:        "mas-bandwidth/nova-tools",
		IssueNumber: 1945,
		State:       "closed",
	})
	// Issue #2029 is OPEN
	uid2, _ := MintUID()
	store.AddNode(&WorkNode{
		UID:         uid2,
		Repo:        "mas-bandwidth/nova-tools",
		IssueNumber: 2029,
		State:       "open",
	})
	// Issue #2045 is CLOSED, but #2040 is OPEN
	uid3, _ := MintUID()
	store.AddNode(&WorkNode{
		UID:         uid3,
		Repo:        "mas-bandwidth/nova-tools",
		IssueNumber: 2045,
		State:       "closed",
	})
	uid4, _ := MintUID()
	store.AddNode(&WorkNode{
		UID:         uid4,
		Repo:        "mas-bandwidth/nova-tools",
		IssueNumber: 2040,
		State:       "open",
	})
	// Issue #1984 is CLOSED
	uid5, _ := MintUID()
	store.AddNode(&WorkNode{
		UID:         uid5,
		Repo:        "mas-bandwidth/nova-tools",
		IssueNumber: 1984,
		State:       "closed",
	})

	priorities := []SprintPriority{
		{
			Rank:  1,
			Title: "fill: fail-closed load",
			Refs:  []SprintRef{{Repo: "mas-bandwidth/nova-tools", IssueNumber: 1945}},
		},
		{
			Rank:  2,
			Title: "fill: one dealer at a time",
			Refs:  []SprintRef{{Repo: "mas-bandwidth/nova-tools", IssueNumber: 2029}},
		},
		{
			Rank:  3,
			Title: "launch: attempt identity and fencing",
			Refs: []SprintRef{
				{Repo: "mas-bandwidth/nova-tools", IssueNumber: 2045},
				{Repo: "mas-bandwidth/nova-tools", IssueNumber: 2040},
			},
		},
		{
			Rank:  4,
			Title: "harvest drains only finished cards",
			Refs:  []SprintRef{{Repo: "mas-bandwidth/nova-tools", IssueNumber: 1984}},
		},
	}

	// Register sprint priority nodes into store
	nodes, err := BuildSprintPriorityNodes(store, priorities)
	if err != nil {
		t.Fatalf("BuildSprintPriorityNodes failed: %v", err)
	}
	if len(nodes) != 4 {
		t.Fatalf("expected 4 sprint nodes, got %d", len(nodes))
	}

	// Verify priority 3 depends on uids of #2045 and #2040
	p3Node, ok := store.GetNodeByIssue("sprint-priorities", 3)
	if !ok || len(p3Node.Needs) != 2 {
		t.Fatalf("p3Node needs mismatch: found=%v, node=%+v", ok, p3Node)
	}
	if p3Node.Needs[0] != uid3 || p3Node.Needs[1] != uid4 {
		t.Fatalf("p3Node needs uids mismatch: %+v vs [%s, %s]", p3Node.Needs, uid3, uid4)
	}

	// Query completion
	result := QuerySprintCompletion(store, priorities)

	// Total priorities = 4
	// Completed = 2 (row 1 and row 4)
	// Row 2 is open (#2029 is open)
	// Row 3 is in_progress (#2045 is closed, but #2040 is open)
	// Completion percentage = 2/4 = 50.0%
	if result.TotalPriorities != 4 {
		t.Errorf("expected total priorities 4, got %d", result.TotalPriorities)
	}
	if result.CompletedPriorities != 2 {
		t.Errorf("expected completed priorities 2, got %d", result.CompletedPriorities)
	}
	if result.CompletionPercentage != 50.0 {
		t.Errorf("expected completion percentage 50.0, got %f", result.CompletionPercentage)
	}

	// Total issues = 5 (#1945, #2029, #2045, #2040, #1984)
	// Completed issues = 3 (#1945, #2045, #1984)
	// Issue completion percentage = 3/5 = 60.0%
	if result.TotalReferencedIssues != 5 {
		t.Errorf("expected 5 total referenced issues, got %d", result.TotalReferencedIssues)
	}
	if result.CompletedReferencedIssues != 3 {
		t.Errorf("expected 3 completed issues, got %d", result.CompletedReferencedIssues)
	}
	if result.IssueCompletionPercentage != 60.0 {
		t.Errorf("expected issue completion 60.0%%, got %f", result.IssueCompletionPercentage)
	}

	// Verify summary text
	text := result.SummaryText()
	if !strings.Contains(text, "2 / 4 rows complete (50.0%)") {
		t.Errorf("summary text missing row percentage:\n%s", text)
	}
	if !strings.Contains(text, "3 / 5 issues closed (60.0%)") {
		t.Errorf("summary text missing issue percentage:\n%s", text)
	}
	if !strings.Contains(text, "ROW 01 DONE") || !strings.Contains(text, "ROW 04 DONE") {
		t.Errorf("summary text missing done rows:\n%s", text)
	}
	if !strings.Contains(text, "ROW 03 IN_PROGRESS") {
		t.Errorf("summary text missing in_progress row 3:\n%s", text)
	}
}
