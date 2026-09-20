package workreconcile

import (
	"encoding/hex"
	"testing"
)

func TestMintUID(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 5000; i++ {
		uid, err := MintUID()
		if err != nil {
			t.Fatalf("MintUID failed: %v", err)
		}
		if len(uid) != 32 {
			t.Fatalf("expected 32 hex chars, got %d (%q)", len(uid), uid)
		}
		// Must be valid hex
		if _, err := hex.DecodeString(uid); err != nil {
			t.Fatalf("uid is not valid hex: %v", err)
		}
		if seen[uid] {
			t.Fatalf("collision detected on UID: %s", uid)
		}
		seen[uid] = true
	}
}

func TestStoreOperations(t *testing.T) {
	store := NewStore()

	node1 := &WorkNode{
		UID:         "11111111111111111111111111111111",
		ID:          "mas-bandwidth/nova-tools/issues/101",
		Provider:    "github",
		Repo:        "mas-bandwidth/nova-tools",
		IssueNumber: 101,
		Title:       "First Issue",
		State:       "open",
		Revision:    "rev-1",
	}

	if err := store.AddNode(node1); err != nil {
		t.Fatalf("AddNode failed: %v", err)
	}

	// Cannot add duplicate UID
	dupUID := &WorkNode{
		UID:         "11111111111111111111111111111111",
		Repo:        "mas-bandwidth/nova-tools",
		IssueNumber: 102,
	}
	if err := store.AddNode(dupUID); err == nil {
		t.Fatal("expected error adding node with duplicate UID")
	}

	// Cannot add duplicate repo#number
	dupIssue := &WorkNode{
		UID:         "22222222222222222222222222222222",
		Repo:        "mas-bandwidth/nova-tools",
		IssueNumber: 101,
	}
	if err := store.AddNode(dupIssue); err == nil {
		t.Fatal("expected error adding node with duplicate issue number in same repo")
	}

	// Lookup by UID
	foundUID, ok := store.GetNodeByUID("11111111111111111111111111111111")
	if !ok || foundUID.Title != "First Issue" {
		t.Fatalf("GetNodeByUID failed: found=%v, node=%v", ok, foundUID)
	}

	// Lookup by issue
	foundIssue, ok := store.GetNodeByIssue("mas-bandwidth/nova-tools", 101)
	if !ok || foundIssue.UID != "11111111111111111111111111111111" {
		t.Fatalf("GetNodeByIssue failed: found=%v, node=%v", ok, foundIssue)
	}

	// Update node
	node1.Title = "Updated Title"
	node1.Revision = "rev-2"
	if err := store.UpdateNode(node1); err != nil {
		t.Fatalf("UpdateNode failed: %v", err)
	}
	updated, _ := store.GetNodeByIssue("mas-bandwidth/nova-tools", 101)
	if updated.Title != "Updated Title" || updated.Revision != "rev-2" {
		t.Fatalf("node not updated properly: %+v", updated)
	}

	// Checkpoint operations
	store.SaveCheckpoint(&Checkpoint{
		Repo:              "mas-bandwidth/nova-tools",
		LastAppliedNumber: 101,
		BatchNumber:       1,
		ImportedCount:     1,
	})
	cp, ok := store.GetCheckpoint("mas-bandwidth/nova-tools")
	if !ok || cp.LastAppliedNumber != 101 || cp.BatchNumber != 1 {
		t.Fatalf("GetCheckpoint failed: ok=%v cp=%+v", ok, cp)
	}
}
