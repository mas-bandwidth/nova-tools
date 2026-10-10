package decide

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// The import (SPEC-NOVA-DECIDE, the backfill-2026-10-04.w1 subsection of section 4) turns
// finished decisions into labelled records: a heavy read's VERDICT.md, a judgment file
// joined with the answer the sprint's log holds for it, and a HOLD report. Each item is one
// decision whose outcome is its label, with an id from its source path and content, so the
// same item imported again is the record it already is. It asks no backend: the answers are
// empty and the backend is "import", so a calibration of a model's answers never reads one.

// The kinds an import records, and the decision name each is recorded under.
const (
	ImportVerdict  = "verdict"
	ImportJudgment = "judgment"
	ImportReport   = "report"
)

// ImportKinds is every kind, in the order a result prints them.
var ImportKinds = []string{ImportVerdict, ImportJudgment, ImportReport}

// ImportSources names what to import; an empty field is a source not read. Verdicts and
// Reports are globs of files; Judgments is a directory of judgment files, which needs Log,
// a nova-sprint log --json export, to hold the answers.
type ImportSources struct {
	Verdicts, Judgments, Log, Reports string
}

// ImportCount is how many items of a kind were recorded now and how many were there already.
type ImportCount struct{ New, Existing int }

// ImportResult is the count per kind, and the judgments the log has no answer for (they are
// not recorded: an unlabelled item is not a label).
type ImportResult struct {
	Kinds      map[string]ImportCount
	Unanswered int
}

type importItem struct {
	kind, path, label, state string
}

// Import records every item of src in the record at path, once: an item whose id is there
// already is counted and left. The whole import is one write under the record's lock.
func Import(path string, src ImportSources, now time.Time) (ImportResult, error) {
	var problems []string
	if src == (ImportSources{}) {
		return ImportResult{}, errors.New("no source named: it wants --verdicts, --judgments with --log, or --reports")
	}
	if (src.Judgments == "") != (src.Log == "") {
		problems = append(problems, "--judgments and --log go together: a judgment file holds no answer, the sprint's log export does")
	}
	if len(problems) > 0 {
		return ImportResult{}, errors.New(strings.Join(problems, "; "))
	}
	var items []importItem
	res := ImportResult{Kinds: map[string]ImportCount{}}
	if src.Verdicts != "" {
		got, err := verdictItems(src.Verdicts)
		if err != nil {
			return res, err
		}
		items = append(items, got...)
	}
	if src.Judgments != "" {
		got, unanswered, err := judgmentItems(src.Judgments, src.Log)
		if err != nil {
			return res, err
		}
		items, res.Unanswered = append(items, got...), unanswered
	}
	if src.Reports != "" {
		got, err := reportItems(src.Reports)
		if err != nil {
			return res, err
		}
		items = append(items, got...)
	}
	at := now.UTC().Format(time.RFC3339)
	err := locked(path, func(ds []Decision) ([]line, error) {
		have := map[string]bool{}
		for _, d := range ds {
			have[d.ID] = true
		}
		var add []line
		for _, it := range items {
			id := it.kind + "-" + Sum([]byte(it.path + "\x00" + it.state))[:16]
			c := res.Kinds[it.kind]
			if have[id] {
				c.Existing++
			} else {
				have[id] = true
				c.New++
				add = append(add,
					line{Decision: &Decision{ID: id, Decision: "import-" + it.kind, Schema: "import", Backend: "import", At: at,
						Inputs: map[string]string{"kind": it.kind, "source": it.path}, State: it.state, Answers: map[string]Answer{}}},
					line{Outcome: &Outcome{ID: id, Label: it.label, Note: it.path, At: at}})
			}
			res.Kinds[it.kind] = c
		}
		return add, nil
	})
	return res, err
}

func verdictItems(glob string) ([]importItem, error) {
	return fileItems(glob, ImportVerdict, func(text string) string {
		f := strings.Fields(firstLine(text))
		if len(f) == 0 {
			return ""
		}
		return f[0]
	})
}

func reportItems(glob string) ([]importItem, error) {
	return fileItems(glob, ImportReport, func(text string) string {
		if strings.TrimSpace(firstLine(text)) == "Verdict: HOLD" {
			return "HOLD"
		}
		return ""
	})
}

// fileItems reads every file the glob names; a file whose label is empty is not an item.
func fileItems(glob, kind string, label func(string) string) ([]importItem, error) {
	names, err := filepath.Glob(glob)
	if err != nil {
		return nil, fmt.Errorf("--%ss %q is not a glob: %w", kind, glob, err)
	}
	var out []importItem
	for _, name := range names {
		raw, err := os.ReadFile(name)
		if err != nil {
			return nil, err
		}
		if l := label(string(raw)); l != "" {
			out = append(out, importItem{kind, absPath(name), l, string(raw)})
		}
	}
	return out, nil
}

// judgmentItems reads the *.md files of dir, each labelled by the verb of the last log line
// that answers its judgment (the file's name is the judgment's id); a judgment no line
// answers is counted, not read into the record.
func judgmentItems(dir, logFile string) ([]importItem, int, error) {
	raw, err := os.ReadFile(logFile)
	if err != nil {
		return nil, 0, err
	}
	var export struct {
		Lines []struct {
			Verb    string   `json:"verb"`
			Answers []string `json:"answers"`
		} `json:"lines"`
	}
	if err := json.Unmarshal(raw, &export); err != nil {
		return nil, 0, fmt.Errorf("--log %s is not a nova-sprint log --json export ({\"lines\": [...]}): %w", logFile, err)
	}
	verb := map[string]string{}
	for _, l := range export.Lines {
		for _, id := range l.Answers {
			if l.Verb != "" {
				verb[id] = l.Verb
			}
		}
	}
	names, err := filepath.Glob(filepath.Join(dir, "*.md"))
	if err != nil {
		return nil, 0, err
	}
	slices.Sort(names)
	var out []importItem
	unanswered := 0
	for _, name := range names {
		text, err := os.ReadFile(name)
		if err != nil {
			return nil, 0, err
		}
		v, ok := verb[strings.TrimSuffix(filepath.Base(name), ".md")]
		if !ok {
			unanswered++
			continue
		}
		out = append(out, importItem{ImportJudgment, absPath(name), v, string(text)})
	}
	return out, unanswered, nil
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}

func absPath(p string) string {
	if a, err := filepath.Abs(p); err == nil {
		return a
	}
	return p
}
