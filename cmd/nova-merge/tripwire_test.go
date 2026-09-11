package main

import (
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/merge"
)

// Demanded test 22's last clause, absent until read 4b's finding 6: A TRIPWIRE ON EVERY
// PATH OPENED FINDS NO RECORD FILE OPENED FOR WRITING TWICE.
//
// Rule 22: "a record is one immutable file per submission, never edited and never
// replaced, so the reset removes nothing that the outbox does not restore". That is a
// property of the RUNNING tool and of no output -- the CAS loop writes the outbox's bytes
// back over the record's path, and a second open of a path already written is the edit the
// rule forbids. The source tests say which files may write at all; this one watches what
// the verbs actually do.
//
// It is NOT parallel: the tripwire is one hook for the process, and a parallel test's
// writes would land in this one's ledger.
func TestNoRecordFileIsOpenedForWritingTwice(t *testing.T) {
	l := newLab(t)
	oid := setupPR(t, l, 951, "feature-a", "a.txt", true)
	if exit, _, errb := l.run("run", "--lane", l.lane, "--once"); exit != 0 {
		t.Fatalf("run: %s", errb)
	}
	var mu sync.Mutex
	opens := map[string][]string{}
	stop := merge.WatchWrites(func(path string, body []byte) {
		mu.Lock()
		defer mu.Unlock()
		opens[path] = append(opens[path], string(body))
	})
	defer stop()

	// Two reads by two lines, a gate with its summary, and the pass that folds them: every
	// verb that puts a record in the branch, and the CAS loop under each of them.
	base := l.baseSHA()
	for _, who := range []string{"emma", "stella"} {
		if exit, _, errb := l.run("read", "--lane", l.lane, "--pr", "951", "--who", who,
			"--head", oid, "--verdict", "approve"); exit != 0 {
			t.Fatalf("read by %s: %s", who, errb)
		}
	}
	_, stdout, _ := l.run("run", "--lane", l.lane, "--once")
	m := mergeSHAOf(t, stdout, "951")
	if exit, _, errb := l.run("gate", "--lane", l.lane, "--pr", "951", "--head", oid,
		"--base-sha", base, "--merge", m, "--verdict", "green", "--summary", l.summary("tripwire")); exit != 0 {
		t.Fatalf("gate: %s", errb)
	}
	if exit, _, errb := l.run("status", "--lane", l.lane); exit != 0 {
		t.Fatalf("status: %s", errb)
	}
	stop()

	mu.Lock()
	defer mu.Unlock()
	records := 0
	for path, bodies := range opens {
		rel := strings.TrimPrefix(filepath.ToSlash(strings.TrimPrefix(path, l.lane)), "/")
		if !strings.HasPrefix(rel, merge.ReadsDir+"/") && !strings.HasPrefix(rel, merge.GatesDir+"/") {
			continue
		}
		records++
		if len(bodies) != 1 {
			t.Errorf("%s was opened for writing %d times; a record is one immutable file per submission, never edited and never replaced", rel, len(bodies))
			continue
		}
	}
	// Three records reached the branch -- two reads, one gate -- plus the gate's summary
	// beside it. A tripwire that saw nothing would pass by checking nothing.
	if records != 4 {
		t.Fatalf("the tripwire saw %d record paths written, want 4 (two reads, a gate and its summary); it was watching the wrong thing", records)
	}
}
