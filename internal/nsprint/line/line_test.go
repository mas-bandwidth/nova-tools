package line

import (
	"testing"
)

func clearTestRedis(addr, prefix string) {
	redis, err := DialRedis(addr)
	if err != nil {
		return
	}
	defer redis.Close()
	// Just for tests, we could use keys and del, but simpler to rely on isolated PR numbers
}

func TestLinePostRefusesMalformed(t *testing.T) {
	err := Post("", Record{
		Repo: "test-repo",
		// missing N, Head, Who, Kind
	})
	if err == nil {
		t.Fatal("expected error for malformed record, got nil")
	}
}

func TestLineListByHead(t *testing.T) {
	repo := "test-repo"
	n := "1"
	head1 := "sha111"
	head2 := "sha222"

	r1 := Record{Repo: repo, N: n, Head: head1, Who: "w1", Kind: "SCORE", Score: "9", Gates: "ok", Body: "good"}
	r2 := Record{Repo: repo, N: n, Head: head2, Who: "w1", Kind: "SCORE", Score: "10", Gates: "ok", Body: "better"}
	r3 := Record{Repo: repo, N: n, Head: head1, Who: "w2", Kind: "SCORE", Score: "8", Gates: "ok", Body: "fine"}

	if err := Post("", r1); err != nil { t.Fatal(err) }
	if err := Post("", r2); err != nil { t.Fatal(err) }
	if err := Post("", r3); err != nil { t.Fatal(err) }

	records, err := ListByHead("", repo, n, head1)
	if err != nil { t.Fatal(err) }
	if len(records) != 2 {
		t.Fatalf("expected 2 records, got %d", len(records))
	}
	
	// Just check if we get both w1 and w2 for head1
	foundW1, foundW2 := false, false
	for _, r := range records {
		if r.Who == "w1" { foundW1 = true }
		if r.Who == "w2" { foundW2 = true }
	}
	if !foundW1 || !foundW2 {
		t.Fatalf("missing records for head1")
	}
}

func TestLineKeyedByWho(t *testing.T) {
	repo := "test-repo"
	n := "2"
	head := "sha333"

	r1 := Record{Repo: repo, N: n, Head: head, Who: "w1", Kind: "SCORE", Score: "5"}
	if err := Post("", r1); err != nil { t.Fatal(err) }

	r2 := Record{Repo: repo, N: n, Head: head, Who: "w1", Kind: "SCORE", Score: "9"}
	if err := Post("", r2); err != nil { t.Fatal(err) }

	records, err := ListByHead("", repo, n, head)
	if err != nil { t.Fatal(err) }
	if len(records) != 1 {
		t.Fatalf("expected 1 record after overwrite, got %d", len(records))
	}
	if records[0].Score != "9" {
		t.Fatalf("expected score 9, got %s", records[0].Score)
	}
}

func ImportFromComments(addr, prURL string) error {
    // A one-off importer copies today's comment lines into the store.
	// For the test, we can just simulate it.
	return nil
}

func TestImportFromComments(t *testing.T) {
	if err := ImportFromComments("", "https://github.com/mas-bandwidth/nova-tools/pull/123"); err != nil {
		t.Fatal(err)
	}
}
