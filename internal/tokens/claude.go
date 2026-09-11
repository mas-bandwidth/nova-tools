package tokens

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"path/filepath"
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
	Message   *struct {
		ID    string                     `json:"id"`
		Model string                     `json:"model"`
		Usage map[string]json.RawMessage `json:"usage"`
		// `message.content` is a STRING on a user turn and an ARRAY of blocks on an
		// assistant turn, and both are valid transcript lines. Declaring it the array
		// alone made every user turn a type mismatch -- valid JSON that json.Unmarshal
		// refuses -- and the reader called those lines "not JSON": 1,260 of 1,278 files
		// flagged on a clean bench, TOKENS UNREADABLE, exit 1, and a remedy nobody could
		// act on (measured 2026-09-11). Raw here, decoded below only when it is an array.
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

// ReadClaude walks a transcript directory and returns its stream and its accounting.
func ReadClaude(label, dir string, rules *Rules) *Source {
	s := &Source{Label: Label(KindClaude, label), Kind: KindClaude, Path: dir, Reports: ClaudeTypes, Basis: UTC}

	var files []string
	walkErr := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if p == dir {
				return err
			}
			s.unreadable(p, err.Error())
			return nil
		}
		if d.IsDir() {
			return nil
		}
		for _, suf := range claudeSuffixes {
			if strings.HasSuffix(p, suf) {
				files = append(files, p)
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

	for _, path := range files {
		s.Stat.Files++
		f, err := openSource(path)
		if err != nil {
			s.unreadable(path, err.Error())
			continue
		}
		prev := ""
		bad := 0
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
				continue
			}
			if line.Message == nil || line.Message.Usage == nil {
				continue
			}
			if line.Message.Model == syntheticModel {
				continue
			}
			repo := rules.AttributeInputs(toolInputs(line.Message.Content), prev)
			prev = repo
			day, ok := DayOfStamp(line.Timestamp)
			if !ok {
				s.unparsed(path, n, "the timestamp is not an RFC 3339 stamp and is not a day this tool can read: "+line.Timestamp)
				continue
			}
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
		f.Close()
		if scanErr != nil {
			s.unreadable(path, scanErr.Error())
			continue
		}
		if bad > 0 {
			s.unreadable(path, fmt.Sprintf("badline=%d: lines that are not JSON; the rest of the file was read", bad))
		}
	}
	s.Collapse()
	return s
}

// unreadable records a source this run could not read: counted, printed on its own line,
// and the reason the run exits 1.
func (s *Source) unreadable(path, why string) {
	s.Stat.Unreadable++
	s.Unreadables = append(s.Unreadables, Unreadable{Label: s.Label, Path: path, Why: why})
}

// DayOfStamp is the UTC day of an RFC 3339 stamp (rule 17: "A day is a UTC day, from the
// message's own stamp"). The stamp is PARSED and converted, never sliced: its first ten
// characters are the day in whatever zone it was printed in, and a line stamped
// 2026-09-11T20:30:00-07:00 belongs to 2026-09-12. A stamp this tool cannot read is not a
// day, is not dated by a guess, and is not dropped either: every caller counts it and
// prints it (rule 3).
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
			keys := make([]string, 0, len(t))
			for k := range t {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
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
