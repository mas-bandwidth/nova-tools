package card

// prompt.go is the card the MODEL reads (nova-tools#3956), apart from the
// header the wrapper reads. Glenn 2026-09-25 2:35 PM ET, on swarm-0925a
// (kimi-k3 0/14 NATIVE INCOMPLETE with no RESULT.md, deepseek-v4-pro 8/9):
// "I believe the models are fine, it's just how we are using them."
//
// The prompt is rendered from the card's record by one template per model
// family (Families; a route's family is FamilyOf its model in routes.yaml).
// A template is Go text/template text, stored whole at
// cfg:card:template:<family> in Redis to override the built-in default for
// the family. Every template is held to the same shape before a byte of it
// is printed (checkPrompt), whichever source it came from:
//
//   - the goal, one sentence, on the first line;
//   - the exact files, where the test goes, the DONE-WHEN command and what
//     fails-then-passes means;
//   - the repo layout the model needs, in three lines;
//   - the result contract with a literal RESULT.md example (line 1 verbatim,
//     line 2 DONE | ABSTAIN <why> | BLOCKED <why>);
//   - what not to do (push, PR, subagents, questions);
//   - the issue text last: {{.Issue}} appears once and at most IssueTailMax
//     bytes of literal text (a closing tag) follow it;
//   - no wrapper key (typedrec.WrapperOwned): the wrapper fills those from facts, so
//     nothing is asked of the model that it could copy wrong;
//   - under PromptBudget bytes, the issue text excluded.

import (
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"text/template"

	"github.com/mas-bandwidth/nova-tools/internal/typedrec"
)

// The model families a card template is written for, from routes.yaml's
// models (FamilyOf). A model of no family reads FamilyDefault.
const (
	FamilyQwen     = "qwen"
	FamilyDeepSeek = "deepseek"
	FamilyKimi     = "kimi"
	FamilyGLM      = "glm"
	FamilyMercury  = "mercury"
	FamilyClaude   = "claude"
	FamilyDefault  = "default"
)

// Families are the six named families, in the order the issue names them.
var Families = []string{FamilyQwen, FamilyDeepSeek, FamilyKimi, FamilyGLM, FamilyMercury, FamilyClaude}

// PromptBudget is the most bytes a rendered prompt may carry, the issue text
// excluded.
const PromptBudget = 2500

// IssueTailMax is the most literal bytes a template may put after the issue
// text (a closing tag such as </issue>).
const IssueTailMax = 40

// IssueSlot is the one placeholder a template must carry, where the issue
// text goes.
const IssueSlot = "{{.Issue}}"

// TemplateKey is where a family's template override lives in Redis.
func TemplateKey(family string) string { return "cfg:card:template:" + family }

var familyWords = []struct{ word, family string }{
	{"qwen", FamilyQwen}, {"deepseek", FamilyDeepSeek}, {"kimi", FamilyKimi}, {"moonshot", FamilyKimi},
	{"glm", FamilyGLM}, {"z-ai/", FamilyGLM}, {"mercury", FamilyMercury}, {"inception/", FamilyMercury},
	{"claude", FamilyClaude}, {"anthropic/", FamilyClaude},
}

// FamilyOf is the template family of a model launch string or a family name
// (qwen/qwen3.8-flash, deepseek-v4-pro, moonshotai/kimi-k3, glm-5.3-flash,
// inception/mercury-2.5, anthropic/claude-haiku-4.5): FamilyDefault when it
// names none.
func FamilyOf(model string) string {
	m := strings.ToLower(strings.TrimSpace(model))
	if m == FamilyDefault {
		return FamilyDefault
	}
	for _, fw := range familyWords {
		if strings.Contains(m, fw.word) {
			return fw.family
		}
	}
	return FamilyDefault
}

// PromptSpec is what a prompt is rendered from: the card record's fields.
type PromptSpec struct {
	ID       string // the card id; line 1 names it
	Goal     string // one sentence: the task or the title
	Repo     string // owner/name
	Base     string // the base branch
	BaseSHA  string // 40 hex
	Paths    string // space-separated files to change
	Test     string // `<package> <TestName>`, or none
	DoneWhen string
	Issue    string // the issue text, rendered last
}

// Contract is the card's RESULT line 1, as the harness header prints it.
func (s PromptSpec) Contract() string {
	sha := s.BaseSHA
	if len(sha) > 12 {
		sha = sha[:12]
	}
	return fmt.Sprintf("RESULT: %s sha=%s", s.ID, sha)
}

// promptData is what a template sees.
type promptData struct {
	Goal, Repo, Base, BaseSHA8, Paths, DoneWhen, Contract string
	TestName, TestDir, TestCmd                            string
	Issue                                                 string
}

func (s PromptSpec) data() promptData {
	d := promptData{Goal: oneLine(s.Goal), Repo: s.Repo, Base: s.Base, Paths: oneLine(s.Paths),
		DoneWhen: oneLine(s.DoneWhen), Contract: s.Contract(), Issue: IssueSlot}
	d.BaseSHA8 = s.BaseSHA
	if len(d.BaseSHA8) > 8 {
		d.BaseSHA8 = d.BaseSHA8[:8]
	}
	if argv, _ := TestCommand(s.Test); argv != nil {
		d.setTest(strings.Fields(s.Test))
	}
	for _, p := range []*string{&d.Goal, &d.Repo, &d.Base, &d.Paths, &d.DoneWhen} {
		if strings.TrimSpace(*p) == "" {
			*p = "-"
		}
	}
	return d
}

// setTest fills the test fields from a TEST line's `<package> <TestName>`
// fields; anything but two leaves them empty (the prompt then names only the
// DONE-WHEN sentence).
func (d *promptData) setTest(f []string) {
	if len(f) != 2 {
		return
	}
	d.TestName = f[1]
	d.TestDir = strings.TrimSuffix(strings.TrimPrefix(f[0], "./"), "/...")
	if d.TestDir == "." || d.TestDir == "" {
		d.TestDir = "the repo root"
	}
	d.TestCmd = fmt.Sprintf("go test %s -run '^%s$' -count=1", f[0], f[1])
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// PromptRefused is a template or a record the prompt cannot be rendered
// from; Why names the rule it breaks.
type PromptRefused struct{ Why string }

func (e *PromptRefused) Error() string { return e.Why }

// Prompt is one rendered prompt: the bytes, the family and where its
// template came from (builtin or redis), and the prompt's size with the issue
// text excluded.
type Prompt struct {
	Text        []byte
	Family      string
	Source      string
	PromptBytes int
}

// RenderPrompt renders spec by tmpl (the family's text; "" is the built-in
// default for family) and holds the result to the prompt's shape. A template
// or a record that breaks it is a *PromptRefused and nothing is rendered.
func RenderPrompt(family, tmpl string, spec PromptSpec) (Prompt, error) {
	source := "redis"
	if strings.TrimSpace(tmpl) == "" {
		t, ok := BuiltinTemplate(family)
		if !ok {
			return Prompt{}, &PromptRefused{Why: fmt.Sprintf("no template for family %q; families: %s, %s", family, strings.Join(Families, ", "), FamilyDefault)}
		}
		tmpl, source = t, "builtin"
	}
	head, tail, err := splitIssue(tmpl)
	if err != nil {
		return Prompt{}, &PromptRefused{Why: "template " + family + ": " + err.Error()}
	}
	t, err := template.New(family).Option("missingkey=error").Parse(head)
	if err != nil {
		return Prompt{}, &PromptRefused{Why: "template " + family + ": " + err.Error()}
	}
	// The template's own words are checked on a neutral record, so an issue
	// that quotes a wrapper key never refuses its card.
	neutral := PromptSpec{ID: "card-id", Goal: "Goal sentence.", Repo: "owner/name", Base: "dev",
		BaseSHA: strings.Repeat("0", 40), Paths: "a/b.go", Test: "./a TestA", DoneWhen: "done-when sentence"}
	var nb bytes.Buffer
	if err := t.Execute(&nb, neutral.data()); err != nil {
		return Prompt{}, &PromptRefused{Why: "template " + family + ": " + err.Error()}
	}
	if err := checkPrompt(nb.String(), tail, neutral, true); err != nil {
		return Prompt{}, &PromptRefused{Why: "template " + family + ": " + err.Error()}
	}
	var b bytes.Buffer
	if err := t.Execute(&b, spec.data()); err != nil {
		return Prompt{}, &PromptRefused{Why: "template " + family + ": " + err.Error()}
	}
	if err := checkPrompt(b.String(), tail, spec, false); err != nil {
		return Prompt{}, &PromptRefused{Why: "card " + spec.ID + ": " + err.Error()}
	}
	n := b.Len() + len(tail)
	b.WriteString(strings.TrimSpace(spec.Issue))
	b.WriteString(tail)
	if !bytes.HasSuffix(b.Bytes(), []byte("\n")) {
		b.WriteByte('\n')
	}
	return Prompt{Text: b.Bytes(), Family: family, Source: source, PromptBytes: n}, nil
}

// splitIssue cuts a template at its one IssueSlot: the head is executed, the
// tail is literal and short.
func splitIssue(tmpl string) (head, tail string, err error) {
	if strings.Count(tmpl, IssueSlot) != 1 {
		return "", "", errors.New("a template carries " + IssueSlot + " exactly once (the issue text is last)")
	}
	head, tail, _ = strings.Cut(tmpl, IssueSlot)
	if len(tail) > IssueTailMax || strings.Contains(tail, "{{") {
		return "", "", fmt.Errorf("the issue text is last: at most %d literal bytes may follow %s", IssueTailMax, IssueSlot)
	}
	return head, tail, nil
}

// wrapperKeyRE matches a typed field the wrapper writes from facts
// (typedrec.Synthesize): a prompt that shows one invites the model to write it.
var wrapperKeyRE = regexp.MustCompile(`\b(` + strings.Join(typedrec.WrapperOwned, "|") + `)\b`)

// checkPrompt holds a rendered head (everything before the issue text) to the
// prompt's shape. words is true for the neutral render, whose words are all
// the template's own: only then is a wrapper key a refusal.
func checkPrompt(head, tail string, spec PromptSpec, words bool) error {
	d := spec.data()
	first, _, _ := strings.Cut(strings.TrimLeft(head, "\n"), "\n")
	switch {
	case !strings.Contains(first, d.Goal):
		return fmt.Errorf("the goal is not the first line (first line %q)", first)
	case !strings.Contains(head, d.Contract+"\n"+typedrec.StatusDone+"\n"):
		return errors.New("no literal RESULT.md example (line 1, then DONE on the next line)")
	case !strings.Contains(head, typedrec.StatusAbstain) || !strings.Contains(head, typedrec.StatusBlocked):
		return errors.New("line 2's other words (ABSTAIN <why>, BLOCKED <why>) are not named")
	case !strings.Contains(head, d.Paths):
		return errors.New("the files to change are not named")
	case !strings.Contains(head, d.DoneWhen):
		return errors.New("the DONE-WHEN sentence is not in the prompt")
	case d.TestCmd != "" && !strings.Contains(head, d.TestCmd):
		return errors.New("the test command is not in the prompt")
	case len(head)+len(tail) > PromptBudget:
		return fmt.Errorf("the prompt is %d bytes without the issue text, over %d (shorten the goal or DONE-WHEN)", len(head)+len(tail), PromptBudget)
	}
	if words {
		if k := wrapperKeyRE.FindString(head + tail); k != "" {
			return fmt.Errorf("wrapper key %s is named; the wrapper fills it from facts", k)
		}
		for _, w := range []string{"push", "subagent", "question"} {
			if !strings.Contains(strings.ToLower(head), w) {
				return fmt.Errorf("what not to do does not name %q", w)
			}
		}
	}
	return nil
}

// BuiltinTemplate is the default template of a family (FamilyDefault
// included).
func BuiltinTemplate(family string) (string, bool) {
	t, ok := builtinTemplates[family]
	return t, ok
}

// The built-in templates carry the same content; the framing is the family's:
// markdown headings (qwen, default), an explicit numbered step list and a
// fenced example (deepseek), a short checklist with the final action said
// twice (kimi: 0/14 wrote no RESULT.md on swarm-0925a), plain numbered lines
// (glm), the fewest words (mercury), and XML sections (claude).
var builtinTemplates = map[string]string{
	FamilyDefault: tmplMarkdown,
	FamilyQwen:    tmplMarkdown,
	FamilyDeepSeek: `Goal: {{.Goal}}

Follow these steps in order.

1. Open the files to change: {{.Paths}}
{{- if .TestCmd}}
2. Write the test {{.TestName}} in {{.TestDir}} (a _test.go file there). Run ` + "`{{.TestCmd}}`" + ` in repo/: it must fail before your change.
{{- else}}
2. Find how to show the goal holds; you will need it in step 4.
{{- end}}
3. Make the change.
4. Run the test again until it passes. Done when: {{.DoneWhen}}
5. Commit in repo/: git add the files, git commit -m "<one line>".
6. Write RESULT.md in the job directory (the parent of repo/). This step is required, even when you stop early.

Layout:
- repo/ is a clone of {{.Repo}} at {{.Base}} ({{.BaseSHA8}}); change files only there.
- The job directory is the parent of repo/; RESULT.md goes there, never inside repo/.
- Your commit in repo/ is the work; the harness pushes it.

RESULT.md is exactly this, line for line, without the fence lines:
` + "```" + `
{{.Contract}}
DONE
` + "```" + `
Copy line 1 as shown. Line 2 is DONE, or ABSTAIN <why>, or BLOCKED <why>. A third line may hold a short note. Nothing else.

Never: push, open a PR, start a subagent, or ask a question. Decide and go on.

The last thing you do is write RESULT.md.

The issue:
{{.Issue}}`,
	FamilyKimi: `Goal: {{.Goal}}

Checklist (do each, in order, then stop):
- [ ] Change only: {{.Paths}}
{{- if .TestCmd}}
- [ ] Test {{.TestName}} in {{.TestDir}}; ` + "`{{.TestCmd}}`" + ` fails first, then passes.
{{- end}}
- [ ] Done when: {{.DoneWhen}}
- [ ] git commit in repo/ (one-line message).
- [ ] Write RESULT.md in the job directory, the parent of repo/.

Layout:
repo/ is {{.Repo}} at {{.Base}} ({{.BaseSHA8}}); edit only there.
The job directory is the parent of repo/; RESULT.md goes there.
Your commit is the work; the harness pushes it.

RESULT.md, two lines, exactly:
{{.Contract}}
DONE

Line 1 copied as shown. Line 2: DONE, or ABSTAIN <why>, or BLOCKED <why>. Optional line 3: a note.

Keep going without pausing: no plan to approve, no question to ask, no push, no PR, no subagent. Do not read beyond the files and the test.

As soon as the commit is made, write RESULT.md. A run that ends without RESULT.md is lost.

Issue:
{{.Issue}}`,
	FamilyGLM: `Goal: {{.Goal}}

1. Files to change: {{.Paths}}
{{- if .TestCmd}}
2. Test: {{.TestName}} in {{.TestDir}}. Run ` + "`{{.TestCmd}}`" + `: fails before, passes after.
{{- else}}
2. Show the goal holds.
{{- end}}
3. Done when: {{.DoneWhen}}
4. Commit in repo/.
5. Write RESULT.md in the job directory (parent of repo/):

{{.Contract}}
DONE

Line 1 as shown. Line 2: DONE, or ABSTAIN <why>, or BLOCKED <why>. Optional line 3: a note.

Layout: repo/ is {{.Repo}} at {{.Base}} ({{.BaseSHA8}}).
The job directory is the parent of repo/.
Commit in repo/; the harness pushes.

Do not push, open a PR, use a subagent or ask a question.

Issue:
{{.Issue}}`,
	FamilyMercury: `Goal: {{.Goal}}
Change: {{.Paths}}
{{- if .TestCmd}}
Test: {{.TestName}} in {{.TestDir}}; ` + "`{{.TestCmd}}`" + ` fails, then passes.
{{- end}}
Done when: {{.DoneWhen}}
repo/ is {{.Repo}} at {{.Base}} ({{.BaseSHA8}}).
The job directory is the parent of repo/.
Commit in repo/; the harness pushes.
Then write RESULT.md in the job directory:
{{.Contract}}
DONE
(Line 2: DONE, ABSTAIN <why> or BLOCKED <why>.)
No push, no PR, no subagent, no question.
Issue:
{{.Issue}}`,
	FamilyClaude: `<goal>{{.Goal}}</goal>

<files>{{.Paths}}</files>
{{- if .TestCmd}}
<test>Add {{.TestName}} in {{.TestDir}}. Run ` + "`{{.TestCmd}}`" + ` in repo/: it fails before your change and passes after.</test>
{{- end}}
<done_when>{{.DoneWhen}}</done_when>

<layout>
repo/ is a clone of {{.Repo}} at {{.Base}} ({{.BaseSHA8}}); change files only there.
The job directory is the parent of repo/; RESULT.md goes there, never inside repo/.
Commit your change in repo/ with a one-line message; the harness pushes it.
</layout>

<result_file>
When the commit is made, write RESULT.md in the job directory, exactly:
{{.Contract}}
DONE
Copy line 1 as shown. Line 2 is DONE, or ABSTAIN <why>, or BLOCKED <why>. An optional third line is a short note.
</result_file>

<rules>Do not push, open a PR, start a subagent, or ask a question: decide, and say why in the note.</rules>

<issue>
{{.Issue}}
</issue>`,
}

const tmplMarkdown = `Goal: {{.Goal}}

## Where
- Files to change: {{.Paths}}
{{- if .TestCmd}}
- The test: {{.TestName}}, in {{.TestDir}} (a _test.go file there).
{{- end}}

## Done when
{{.DoneWhen}}
{{- if .TestCmd}}
Run ` + "`{{.TestCmd}}`" + ` in repo/: it fails before your change and passes after it.
{{- end}}

## Layout
- repo/ is a clone of {{.Repo}} at {{.Base}} ({{.BaseSHA8}}); change files only there.
- The job directory is the parent of repo/; RESULT.md goes there, never inside repo/.
- Commit your change in repo/ with a one-line message; the harness pushes it.

## Result
When the commit is made, write RESULT.md in the job directory, exactly (without the fence lines):
` + "```" + `
{{.Contract}}
DONE
` + "```" + `
Copy line 1 as shown. Line 2 is DONE, or ABSTAIN <why>, or BLOCKED <why>. An optional third line is a short note. Nothing else.

## Do not
Push, open a PR, start a subagent, or ask a question. Decide and keep going.

## The issue
{{.Issue}}`
