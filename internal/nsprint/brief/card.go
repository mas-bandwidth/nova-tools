package brief

import (
	"bytes"
	"fmt"
	"strings"
	"text/template"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/task"
)

// Card brief (nova-tools #4095): friend serve renders each card's brief from
// its record, task:<id>, by the card's kind, in process and with no second
// read: the caller already holds the record it took. The kind's template is
// the same file brief render uses (build, fix, read, rebase), so a card child
// and a sprint-task child are told the same rules.

// Header lines of a card brief.
const (
	headerCard = "CARD: "
	// CardContract is the last paragraph of every card brief: the one line
	// serve ends the card with.
	CardContract = "End with one typed line on stdout: DONE <what> for a build, fix or rebase; " +
		"SCORE who=<you> head=<sha> score=<n>/10 for a read; BLOCKED <why> or ABSTAIN <why> when you cannot. " +
		"Serve ends the card with that line; a child that exits without one fails the card."
)

// cardBlockFields are the record fields a card brief carries after the rules,
// in this order, when the record has them.
var cardBlockFields = []string{"origin", "ref", "stream", "sprint", "repo", "pr", "head", "title"}

// CardKind is the template kind of a card kind: work is build and review is
// read; ok is false for a kind with no template.
func CardKind(kind string) (string, bool) {
	switch kind {
	case "work":
		kind = "build"
	case "review":
		kind = "read"
	}
	if _, ok := templateFor(kind); !ok {
		return kind, false
	}
	return kind, true
}

// RenderCard renders card id's brief from its record rec (the HGETALL of
// task:<id>). The kind picks the template; the title grammar fills PATHS,
// BASE, DEPENDS-ON, DONE-WHEN, CHECK and EXPECT ("-" when absent); model is
// the model the dispatch runs for this kind and decides the co-author line
// (else the title's MODEL, else the record's model field). A kind without a
// template or no model at all is an error naming the field.
func RenderCard(id string, rec map[string]string, model string) ([]byte, error) {
	kind, ok := CardKind(rec["kind"])
	if !ok {
		return nil, fmt.Errorf("BRIEF REFUSED card=%s field=kind want=%s got=%s", id, Kinds(), orAbsentStr(rec["kind"]))
	}
	title := rec["title"]
	if model == "" {
		model = modelOf(title)
	}
	if model == "" {
		model = strings.ToLower(strings.TrimSpace(rec["model"]))
	}
	if model == "" {
		return nil, fmt.Errorf("BRIEF REFUSED card=%s field=model got=ABSENT (serve --model %s=<model>, or MODEL: in the title)", id, kind)
	}
	f := fields{Model: model, Coauthor: coauthorName(model), Base: "-", BaseSHA: "-"}
	f.Paths = orDash(task.TitleField(title, "PATHS"))
	if base, ok := task.TitleField(title, "BASE"); ok {
		if tok := strings.Fields(base); len(tok) > 0 {
			f.Base = tok[0]
		}
	}
	if m := baseSHARx.FindStringSubmatch(title); m != nil {
		f.BaseSHA = m[1]
	}
	f.DependsOn = orDash(task.TitleField(title, "DEPENDS-ON"))
	if f.DependsOn == "-" && rec["blocked_on"] != "" {
		f.DependsOn = oneLine(rec["blocked_on"])
	}
	f.DoneWhen = orDash(task.TitleField(title, "DONE-WHEN"))
	f.Check = "the DONE-WHEN command above, exactly as written"
	if c, ok := task.TitleField(title, "CHECK"); ok && c != "" {
		f.Check = oneLine(c)
	}
	f.Expect = "exit 0; every named test PASS; zero SKIP"
	if e, ok := task.TitleField(title, "EXPECT"); ok && e != "" {
		f.Expect = oneLine(e)
	}
	f.Report = "the typed line below; nova-sprint card show --id " + id + " holds the rest"
	tmplBytes, _ := templateFor(kind)
	tmpl, err := template.New(kind).Option("missingkey=error").Parse(string(tmplBytes))
	if err != nil {
		return nil, fmt.Errorf("brief card %s: template %s: %w", id, kind, err)
	}
	var b bytes.Buffer
	b.WriteString(headerCard + id + "\n")
	b.WriteString(headerRules + sha256hex(tmplBytes) + "\n")
	if err := tmpl.Execute(&b, f); err != nil {
		return nil, fmt.Errorf("brief card %s: template %s: %w", id, kind, err)
	}
	b.WriteString("\nKIND: " + kind + "\n")
	for _, k := range cardBlockFields {
		if v := oneLine(rec[k]); v != "" {
			fmt.Fprintf(&b, "%s: %s\n", strings.ToUpper(k), v)
		}
	}
	b.WriteString("\n" + CardContract + "\n")
	return b.Bytes(), nil
}

func orAbsentStr(s string) string {
	if strings.TrimSpace(s) == "" {
		return absent
	}
	return s
}
