package main

import (
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
)

func TestWrongFlagNamesTheVerbsActualFlags(t *testing.T) {
	t.Parallel()
	code, out, errout := runTable("view", "set", "work", "--summry", "done")
	for _, want := range []string{"unknown flag --summry", "view set flags:", "--summary", "--tables", "run: nova-table help view set"} {
		if !strings.Contains(errout, want) {
			t.Errorf("missing %q in %q", want, errout)
		}
	}
	if code != 2 || out != "" || strings.Contains(errout, "flag provided but not defined") || strings.Count(errout, "\n") != 1 {
		t.Fatalf("%d %q %q", code, out, errout)
	}
}

func TestHelpExplainsColumnDefaultsAndDeletion(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"help", "create"}, "pct defaults to the pooled fold"},
		{[]string{"help", "drop"}, "saved column definition; keep snapshots from earlier epochs"},
		{[]string{"help", "row", "del"}, "missing row succeeds with existed=0"},
		{[]string{"help", "render"}, "--view"},
	} {
		code, out, errout := runTable(tc.args...)
		if code != 0 || errout != "" || !strings.Contains(out, tc.want) {
			t.Fatalf("%v: %d %q %q", tc.args, code, out, errout)
		}
	}
}

func TestEndFlagsKeepsAllRemainingPositionals(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{
		{"t", "--redis", "socket", "r", "c", "--", "--first", "--second"},
		{"--redis", "socket", "--", "t", "r", "c", "--first", "--second"},
		{"--label", "--", "--receipt", "--redis=socket", "--", "t", "r", "c", "--first", "--second"},
	} {
		fs := verbflag.New("cell remove")
		addr := fs.String("redis", "", "address")
		label := fs.String("label", "--", "label")
		fs.Bool("receipt", false, "receipt")
		pos, err := parseInterleaved(fs, args)
		if err != nil || *addr != "socket" || *label != "--" || strings.Join(pos, ",") != "t,r,c,--first,--second" {
			t.Fatalf("%v: %q %s %s %v", args, pos, *addr, *label, err)
		}
	}
}
