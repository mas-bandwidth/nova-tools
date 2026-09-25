// Package brief renders a child brief from the task record and lints a brief
// against it (nova-tools#3154 rev 6). The task hash decides what a child is
// told: render is the only producer of a brief, it records the template and
// brief digests on the task through ns_task_brief, and lint refuses any brief
// that disagrees with the record, including after the task changes.
package brief

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/task"
)

//go:embed tmpl/*.tmpl
var templates embed.FS

// Header lines: the first two lines of every rendered brief.
const (
	headerTask  = "TASK: "
	headerRules = "RULES-SHA: "
)

// absent is the want= or got= value of a field that is not there.
const absent = "ABSENT"

// afterRead runs between render's round trip 1 and round trip 2. It is nil in
// production; TestBriefRenderRefusesTaskChangedBetweenRoundTrips sets it to
// edit the task in that window.
var afterRead func()

// templateFor returns the template bytes for a task kind. The kind names the
// file; `work`, the task package's build kind, renders build.tmpl.
func templateFor(kind string) ([]byte, bool) {
	name := kind
	if kind == "work" {
		name = "build"
	}
	if name == "" || strings.ContainsAny(name, "/\\.") {
		return nil, false
	}
	b, err := templates.ReadFile("tmpl/" + name + ".tmpl")
	if err != nil {
		return nil, false
	}
	return b, true
}

// Kinds lists the kinds that have a template, for refusal lines.
func Kinds() string { return "build|fix|read|rebase" }

func sha256hex(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// parseRef splits <sprint>/<id>.
func parseRef(ref string) (sprint, id string, err error) {
	sprint, id, ok := strings.Cut(strings.TrimSpace(ref), "/")
	if !ok || sprint == "" || id == "" || strings.ContainsAny(sprint+id, " \t\n") {
		return "", "", fmt.Errorf("task %q: want <sprint>/<id>", ref)
	}
	return sprint, id, nil
}

// fields are the task values a brief carries, read from the title grammar.
type fields struct {
	Paths, Base, BaseSHA, DependsOn, DoneWhen, Check, Expect, Report, Model, Coauthor string
}

var baseSHARx = regexp.MustCompile(`base-sha:\s*([^\s|]+)`)

// taskFields reads the brief fields from a title. MODEL and PATHS are
// required; every other missing field renders as "-".
func taskFields(sprint, id, title string) (fields, error) {
	var f fields
	model := modelOf(title)
	if model == "" {
		return f, fmt.Errorf("model")
	}
	paths, ok := task.TitleField(title, "PATHS")
	if !ok || strings.TrimSpace(paths) == "" {
		return f, fmt.Errorf("paths")
	}
	f.Model = model
	f.Coauthor = coauthorName(model)
	f.Paths = oneLine(paths)
	f.Base, f.BaseSHA = "-", "-"
	if base, ok := task.TitleField(title, "BASE"); ok {
		if tok := strings.Fields(base); len(tok) > 0 {
			f.Base = tok[0]
		}
	}
	if m := baseSHARx.FindStringSubmatch(title); m != nil {
		f.BaseSHA = m[1]
	}
	f.DependsOn = orDash(task.TitleField(title, "DEPENDS-ON"))
	f.DoneWhen = orDash(task.TitleField(title, "DONE-WHEN"))
	f.Check = "the DONE-WHEN command above, exactly as written"
	if c, ok := task.TitleField(title, "CHECK"); ok && c != "" {
		f.Check = oneLine(c)
	}
	f.Expect = "exit 0; every named test PASS; zero SKIP"
	if e, ok := task.TitleField(title, "EXPECT"); ok && e != "" {
		f.Expect = oneLine(e)
	}
	f.Report = "reports/" + sprint + "/" + id + ".md in the owner's self repo"
	return f, nil
}

func orDash(s string, ok bool) string {
	if !ok || strings.TrimSpace(s) == "" {
		return "-"
	}
	return oneLine(s)
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// modelOf is the task's MODEL, lower case, first token: `opus-5.5`.
func modelOf(title string) string {
	m, ok := task.TitleField(title, "MODEL")
	if !ok {
		return ""
	}
	tok := strings.Fields(m)
	if len(tok) == 0 {
		return ""
	}
	return strings.ToLower(tok[0])
}

// coauthorName turns a MODEL into the co-author name: opus-5.5 -> Claude Opus 5.5.
func coauthorName(model string) string {
	parts := strings.Split(model, "-")
	for i, p := range parts {
		if p != "" {
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		}
	}
	return "Claude " + strings.Join(parts, " ")
}

var coauthorRx = regexp.MustCompile(`Co-Authored-By:\s*Claude\s+([^<\n` + "`" + `]+?)\s*<`)

// coauthorModels returns every model a brief's co-author lines name, as MODEL
// spellings: Claude Fable 5.1 -> fable-5.1.
func coauthorModels(text string) []string {
	var out []string
	for _, m := range coauthorRx.FindAllStringSubmatch(text, -1) {
		out = append(out, strings.ToLower(strings.Join(strings.Fields(m[1]), "-")))
	}
	return out
}

// printf writes one output line; a failed write to stdout or stderr has no
// better place to be reported.
func printf(w io.Writer, format string, a ...any) { _, _ = fmt.Fprintf(w, format, a...) }
