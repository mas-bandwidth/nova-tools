package main

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

// One --brief-file is the brief of the cards the ids, --count or --sentinel
// name; given alone it names no card, and the refusal says so and names the
// forms that do: the id with the file, the file given twice or more, or
// --brief-dir. The usage line says the same.
func TestOneBriefFileAloneIsRefusedNamingTheFormsThatAdmitACard(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	a1 := writeNeedsBrief(t, t.TempDir(), "a1", "Fix a1.", "")
	code, _, errs := ta.do("add --stream alpha --brief-file " + a1)
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "one --brief-file is the brief of the cards the ids, --count or --sentinel name, and this add names none")
	assert.Contains(t, errs, "nova-sprint add --stream alpha a1 --brief-file "+a1)
	assert.Contains(t, errs, "--brief-file twice or more, or --brief-dir <dir>")
	// the remedy the refusal names runs
	assert.Contains(t, ta.ok("add --stream alpha a1 --brief-file "+a1), "MOVED a1 -> ready")
	assert.Contains(t, banner(), "--brief-file <f1> --brief-file <f2>...: a card per file, its id the file's name without .md")
}

// A card added from a brief file takes its id from the file's name, and add
// says so on a NOTE line under its ADD line, as its help does.
func TestAddSaysACardsIdIsItsBriefFilesName(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	dir := t.TempDir()
	a := writeNeedsBrief(t, dir, "a", "Fix a.", "")
	b := writeNeedsBrief(t, dir, "b", "Fix b.", "")
	out := ta.ok("add --stream s1 --brief-file " + a + " --brief-file " + b)
	assert.Contains(t, out, "ADD OK stream=s1 cards=2 before=- moved=2 refused=0 notes=0 op=")
	assert.Contains(t, out, "\nNOTE each card's id is its brief file's name without .md ("+a+" is a)\n")
	other := t.TempDir()
	writeNeedsBrief(t, other, "c", "Fix c.", "")
	assert.Contains(t, ta.ok("add --stream s2 --brief-dir "+other), "\nNOTE each card's id is its brief file's name without .md ("+filepath.Join(other, "c.md")+" is c)\n")
	var res output
	d := writeNeedsBrief(t, dir, "d", "Fix d.", "")
	e := writeNeedsBrief(t, dir, "e", "Fix e.", "")
	ta.json("add --stream s3 --brief-file "+d+" --brief-file "+e, &res)
	assert.Equal(t, []string{"each card's id is its brief file's name without .md (" + d + " is d)"}, res.Says, "--json carries the same")
	help := ta.ok("add -h")
	assert.Contains(t, help, "each card's id its file's name without .md (a1.md is a1)")
}
