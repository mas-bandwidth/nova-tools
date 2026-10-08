package tokens

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"maps"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"
)

// --claude <label>=<dir>: Claude Code transcripts.
//
// Every *.jsonl and every *.output under <dir>, recursively, in sorted path order. A
// window transcript and a child (Agent tool) transcript are the same JSONL shape and are
// read the same way, because the window-or-child distinction is not a column: a model on a
// repo on a day is one row whoever drove it.
//
// The one thing this reader knows that the others do not is that a STREAMED message writes
// its id on many lines, each with a usage block that grows. The last line for an id is the
// message, and every earlier one is `dup=`.

// claudeSuffixes are the two file shapes a transcript arrives in.
var claudeSuffixes = []string{".jsonl", ".output"}

type claudeLine struct {
	Timestamp string `json:"timestamp"`
	// Cwd is the session's working directory, which a transcript writes on every line.
	// It is the LOWEST rung of the attribution ladder: a conversational turn, a pure
	// Task fan-out, or any turn before the first tool call named no path at all, and the
	// whole file was `unknown` -- a bucket `check` is happy with. Read only when the
	// tool-call paths and the previous repo have both said nothing.
	Cwd     string `json:"cwd"`
	Message *struct {
		ID    string                     `json:"id"`
		Model string                     `json:"model"`
		Usage map[string]json.RawMessage `json:"usage"`
		// `message.content` is a STRING on a user turn and an ARRAY of blocks on an
		// assistant turn, and both are valid transcript lines. Declaring it the array
		// alone made every user turn a type mismatch, causing the reader to reject
		// those lines as invalid JSON.
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

// contentBlock is one block of an assistant turn's content array. Only `tool_use` carries
// the input a path is found in.
type contentBlock struct {
	Type  string          `json:"type"`
	Input json.RawMessage `json:"input"`
}

// toolInputs is every tool_use input in a message's content. A content that is a string,
// a null, or any other shape has no tool blocks and is not an error: the line is a turn
// this reader has nothing to attribute from, not a line it could not read.
func toolInputs(raw json.RawMessage) []string {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '[' {
		return nil
	}
	var blocks []contentBlock
	if err := json.Unmarshal(trimmed, &blocks); err != nil {
		return nil
	}
	var inputs []string
	for _, c := range blocks {
		if c.Type == "tool_use" && len(c.Input) > 0 {
			inputs = append(inputs, jsonStrings(c.Input)...)
		}
	}
	return inputs
}

// usageKeys maps the transcript's own usage keys onto the five types. `reasoning` is
// absent on purpose: the transcript carries no such count, so every Claude row's
// reasoning cell is a dash and never a zero.
var usageKeys = map[string]Type{
	"input_tokens":                Input,
	"output_tokens":               Output,
	"cache_creation_input_tokens": CacheWrite,
	"cache_read_input_tokens":     CacheRead,
}

// syntheticModel is the model of a line that is not a message: the harness talking to
// itself rather than a model being paid for.
const syntheticModel = "<synthetic>"

// ClaudeBound is the ceiling and the excludes one --claude tree is walked under: at most
// MaxFiles transcript files, with every path matching an Exclude glob (or lying under one)
// left out. The zero value reads the whole tree, the behavior before the bound existed.
// DefaultMaxClaudeFiles is the ceiling the tool states in its help.
//
// The largest plausible state is 3,000 transcript files a day (docs/SPEC-TOKENS.md rule
// 11), so the 20,000-file default sits far above a real tree and still stops the very
// large cost a temporary tree included by mistake produced for a cold reader.
type ClaudeBound struct {
	MaxFiles int
	Exclude  []string
}

// DefaultMaxClaudeFiles is the ceiling one --claude tree starts under.
const DefaultMaxClaudeFiles = 20000

// excludedBy is the one --exclude rule, the same one nova-memory's rootFlags.excluded
// keeps: a path equal to a glob, a path under a glob's directory, or a path.Match hit
// (path.Match, not filepath.Match, because tree paths are slash-separated on every OS).
func excludedBy(globs []string, p string) bool {
	for _, g := range globs {
		if p == g || strings.HasPrefix(p, g+"/") {
			return true
		}
		if ok, _ := path.Match(g, p); ok {
			return true
		}
	}
	return false
}

// ReadClaude walks a transcript tree and returns its stream and its accounting.
//
// Every message carries no unit, which the fold writes as `-`: a transcript names a repo,
// never a piece of work. The tree is read through the caller's fs.FS, rooted at dir
// (os.DirFS(dir) in main, fstest.MapFS in a test), and dir is the path the report names.
//
// The walk runs to completion BEFORE the first file is opened, so a tree over MaxFiles is
// refused with the totals it found and not one byte of it is read (SPEC-TOKENS rule 11:
// the state is bounded, and a source is a claim that the report covers it). An Exclude
// glob is the remedy for the temporary tree that made the tree overrun.
func ReadClaude(label, dir string, fsys fs.FS, rules *Rules, bounds ...ClaudeBound) *Source {
	s := &Source{Label: Label(KindClaude, label), Kind: KindClaude, Path: dir, Reports: ClaudeTypes, Basis: UTC}

	bound := ClaudeBound{}
	if len(bounds) > 0 {
		bound = bounds[0]
	}

	var files []string
	var bytesFound int64
	walkErr := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if p == "." {
				return err
			}
			s.unreadable(filepath.Join(dir, p), err.Error())
			return nil
		}
		if p != "." && excludedBy(bound.Exclude, p) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		for _, suf := range claudeSuffixes {
			if strings.HasSuffix(p, suf) {
				files = append(files, p)
				if fi, err := d.Info(); err == nil {
					bytesFound += fi.Size()
				}
				return nil
			}
		}
		return nil
	})
	if walkErr != nil {
		s.unreadable(dir, walkErr.Error())
		return s
	}
	sort.Strings(files)

	if bound.MaxFiles > 0 && len(files) > bound.MaxFiles {
		s.unreadable(dir, fmt.Sprintf("the tree holds %d transcript files and %d bytes, over the --max-files ceiling of %d; run: raise --max-files or pass --exclude <glob> to keep a temporary tree out", len(files), bytesFound, bound.MaxFiles))
		return s
	}

	for _, name := range files {
		path := filepath.Join(dir, name)
		s.Stat.Files++
		f, err := openSourceFS(fsys, name)
		if err != nil {
			s.unreadable(path, err.Error())
			continue
		}
		prev := ""
		bad := 0
		firstBad := 0
		n := 0
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 0, 256*1024), 16*1024*1024)
		for sc.Scan() {
			n++
			text := strings.TrimSpace(sc.Text())
			if text == "" {
				continue
			}
			var line claudeLine
			if err := json.Unmarshal([]byte(text), &line); err != nil {
				bad++
				if firstBad == 0 {
					firstBad = n
				}
				continue
			}
			if line.Message == nil || line.Message.Usage == nil {
				continue
			}
			if line.Message.Model == syntheticModel {
				continue
			}
			day, ok := DayOfStamp(line.Timestamp)
			if !ok {
				s.unparsed(path, n, "the timestamp is not an RFC 3339 stamp and is not a day this tool can read: "+line.Timestamp)
				continue
			}
			rules.SetDay(day)
			inputs := toolInputs(line.Message.Content)
			repo := rules.AttributeInputs(inputs, prev)
			if repo == Unknown && line.Cwd != "" {
				repo = rules.Attribute(PathTokens([]string{line.Cwd}), "")
			}
			prev = repo
			m := Message{Day: day, Basis: UTC, Model: line.Message.Model, Repo: repo, Turn: true}
			for key, t := range usageKeys {
				if raw, ok := line.Message.Usage[key]; ok {
					if v, ok := jsonInt(raw); ok {
						m.Counts.Set(t, v)
					}
				}
			}
			s.AddMessage(line.Message.ID, m)
		}
		scanErr := sc.Err()
		_ = f.Close() // ignored: the file is opened only for reading
		if scanErr != nil {
			s.unreadable(path, scanErr.Error())
			continue
		}
		if bad > 0 {
			s.unreadableAt(path, firstBad, fmt.Sprintf("badline=%d: lines that are not JSON; the rest of the file was read", bad))
		}
	}
	s.Collapse()
	return s
}

// unreadable records a source this run could not read: counted, printed on its own line,
// and the reason the run exits 1.
func (s *Source) unreadable(path, why string) {
	s.unreadableAt(path, 0, why)
}

// unreadableAt is unreadable for a failure at a known line -- a line that is not JSON --
// so the refusal and the ONE remedy line can name the line to inspect or remove.
func (s *Source) unreadableAt(path string, line int, why string) {
	s.Stat.Unreadable++
	s.Unreadables = append(s.Unreadables, Unreadable{Label: s.Label, Path: path, Line: line, Why: why})
}

// DayOfStamp is the UTC day of an RFC 3339 stamp. The stamp is PARSED and converted,
// never sliced: its first ten characters are the day in whatever zone it was printed
// in, so a stamp offset from UTC can belong to a different UTC day. A stamp this tool
// cannot read is not a day, is not dated by a guess, and is not dropped: every caller
// counts and prints it.
func DayOfStamp(stamp string) (string, bool) {
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(stamp))
	if err != nil {
		return "", false
	}
	return t.UTC().Format(dayLayout), true
}

// dayLayout is how a UTC day is written, in the day file and in every `date` column.
const dayLayout = "2006-01-02"

// jsonStrings is every string anywhere inside a JSON value: a tool_use block's input is a
// shape this tool does not know, and a path may be under any key of it.
func jsonStrings(raw json.RawMessage) []string {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil
	}
	var out []string
	var walk func(any)
	walk = func(v any) {
		switch t := v.(type) {
		case string:
			out = append(out, t)
		case []any:
			for _, e := range t {
				walk(e)
			}
		case map[string]any:
			for _, k := range slices.Sorted(maps.Keys(t)) {
				walk(t[k])
			}
		}
	}
	walk(v)
	return out
}

// jsonInt reads a usage number. A key whose value is not a number is not a measurement.
func jsonInt(raw json.RawMessage) (int64, bool) {
	var n json.Number
	if err := json.Unmarshal(raw, &n); err != nil {
		return 0, false
	}
	v, err := n.Int64()
	if err != nil {
		return 0, false
	}
	return v, true
}
