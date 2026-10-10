package main

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

// formBrief writes a passing brief leading with task and carrying the finish form
// (docs/SPEC-CARDS.md, finish-form-present), under name.md, and returns its path.
func formBrief(t *testing.T, dir, name, task string) string {
	t.Helper()
	return writeNeedsBrief(t, dir, name, task+"\n\nTHE FINISH FORM\nVerdict: LAND|HOLD|FAIL\nHead: <40-hex>", "")
}

// One --brief-file with no id, --count or --sentinel is a card of its own, its id the
// file's name without .md, as in the many-file form (nova-tools#5096 item 19, the wave-2
// card builder: "a one-file --brief-file needs the id positional (the id is in the file
// name)"); the id named with the file is still accepted, and the usage line says both.
func TestOneBriefFileAloneIsACardNamedByItsFile(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	dir := t.TempDir()
	a1 := formBrief(t, dir, "a1", "Fix a1.")
	out := ta.ok("add --stream alpha --one --brief-file " + a1)
	assert.Contains(t, out, "MOVED a1 -> ready")
	assert.Contains(t, out, "NOTE each card's id is its brief file's name without .md ("+a1+" is a1)")
	a2 := formBrief(t, dir, "a2", "Fix a2.")
	assert.Contains(t, ta.ok("add --stream alpha a2 --one --brief-file "+a2), "MOVED a2 -> ready", "the id named with the file")
	assert.Contains(t, banner(), "--brief-file <f1> [--brief-file <f2>...]: a card per file, its id the file's name without .md")
}

// A card added from a brief file takes its id from the file's name, and add
// says so on a NOTE line under its ADD line, as its help does.
func TestAddSaysACardsIdIsItsBriefFilesName(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	dir := t.TempDir()
	a := formBrief(t, dir, "a", "Fix a.")
	b := formBrief(t, dir, "b", "Fix b.")
	out := ta.ok("add --stream s1 --brief-file " + a + " --brief-file " + b)
	assert.Contains(t, out, "ADD OK stream=s1 cards=2 before=- moved=2 refused=0 notes=0 op=")
	assert.Contains(t, out, "\nNOTE each card's id is its brief file's name without .md ("+a+" is a)\n")
	other := t.TempDir()
	formBrief(t, other, "c", "Fix c.")
	assert.Contains(t, ta.ok("add --stream s2 --brief-dir "+other), "\nNOTE each card's id is its brief file's name without .md ("+filepath.Join(other, "c.md")+" is c)\n")
	var res output
	d := formBrief(t, dir, "d", "Fix d.")
	e := formBrief(t, dir, "e", "Fix e.")
	ta.json("add --stream s3 --brief-file "+d+" --brief-file "+e, &res)
	assert.Equal(t, []string{"each card's id is its brief file's name without .md (" + d + " is d)"}, res.Says, "--json carries the same")
	help := ta.ok("add -h")
	assert.Contains(t, help, "each card's id its file's name without .md (a1.md is a1)")
}
