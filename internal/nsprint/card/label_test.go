package card_test

import (
	"context"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
	"github.com/mas-bandwidth/nova-tools/internal/typedrec"
)

// TestLintNamesWhereACardNamesItsID (#4399 item 2): a card with every
// required header and no id is refused naming the two lines that name one
// (the cold session found LABEL: only in this package's source), and
// FileShape, card push's refusal with no file, names the id lines, every
// required key and every value KIND takes.
func TestLintNamesWhereACardNamesItsID(t *testing.T) {
	t.Parallel()
	body := "BASE: dev\nBASE-SHA: " + strings.Repeat("a", 40) + "\nPATHS: a.go\nDEPENDS-ON: none\nDONE-WHEN: it holds\n\nbody\n"
	err := card.Lint(context.Background(), []byte(body))
	if err == nil || err.Error() != "missing label: "+card.LabelLine {
		t.Fatalf("got %v; want missing label: %s", err, card.LabelLine)
	}
	for _, want := range []string{"RESULT: <id>", "LABEL: <id>", "docs/CLI.md"} {
		if !strings.Contains(card.LabelLine, want) {
			t.Errorf("LabelLine lacks %q", want)
		}
	}
	shape := card.FileShape()
	for _, want := range append([]string{"KEY: value", "LABEL: <id>", "BASE, BASE-SHA, PATHS, DEPENDS-ON, DONE-WHEN", "KIND: one of"}, typedrec.Kinds...) {
		if !strings.Contains(shape, want) {
			t.Errorf("FileShape lacks %q: %s", want, shape)
		}
	}
}
