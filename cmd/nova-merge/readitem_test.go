package main

import (
	"encoding/json"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/merge"
)

func TestReadPublishesTheSharedReadItemBytes(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	oid := setupPR(t, l, 951, "feature-a", "a.txt", false)
	exit, stdout, stderr := l.run("read", "--lane", l.lane, "--pr", "951", "--who", "emma",
		"--head", oid, "--verdict", "hold", "--note", "the wire's shape")
	if exit != 0 {
		t.Fatalf("read: exit %d\n%s\n%s", exit, stdout, stderr)
	}
	entries := strings.Fields(l.git(l.lane, "ls-tree", "-r", "--name-only", "HEAD", "reads/951"))
	if len(entries) != 1 {
		t.Fatalf("read record paths = %v, want one", entries)
	}
	raw := []byte(l.git(l.lane, "show", "HEAD:"+entries[0]))
	raw = append(raw, '\n')
	var rec merge.Read
	if err := json.Unmarshal(raw, &rec); err != nil {
		t.Fatalf("published read does not decode: %v", err)
	}
	base := strings.TrimSuffix(path.Base(rec.File), ".json")
	_, rand, ok := strings.Cut(base, "-"+rec.At[0:4]+rec.At[5:7]+rec.At[8:10]+"T"+rec.At[11:13]+rec.At[14:16]+rec.At[17:19]+"Z-")
	if !ok {
		t.Fatalf("read path does not carry its submission identity: %q", rec.File)
	}
	item, err := merge.ReadItem(merge.EntryDirName("951"), rec.Who, rec.Head, rec.Verdict, rec.Note, merge.Submission{At: rec.At, Rand: rand})
	if err != nil {
		t.Fatal(err)
	}
	if item.Path != rec.File {
		t.Fatalf("shared path = %q, published path = %q", item.Path, rec.File)
	}
	if string(item.Body) != string(raw) {
		t.Fatalf("published bytes differ from shared constructor:\nwant %q\ngot  %q", item.Body, raw)
	}
}

func TestReadRefusalsDoNotStageASharedItem(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	l.init("main")
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"short head", []string{"--head", "short", "--verdict", "approve"}, "--head wants the full 40-character sha the reader had open"},
		{"bad verdict", []string{"--head", strings.Repeat("a", 40), "--verdict", "abstain"}, "--verdict is approve or hold"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := []string{"read", "--lane", l.lane, "--pr", "951", "--who", "emma"}
			exit, _, stderr := l.run(append(args, tc.args...)...)
			if exit != 2 {
				t.Fatalf("exit = %d, want 2: %s", exit, stderr)
			}
			contains(t, stderr, tc.want)
			entries, err := os.ReadDir(filepath.Join(l.lane, merge.OutboxDir))
			if err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
			if len(entries) != 0 {
				t.Fatalf("refused read staged items: %v", entries)
			}
		})
	}
}
