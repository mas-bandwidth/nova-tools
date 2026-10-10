package tokens

// The cover card for claude.go: the four functions the unit tier's table showed at 0.0% --
// toolInputs, ReadClaude, unreadable and jsonStrings -- each with its main path and a
// refusal, folded from files a test writes into its own temporary directory.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// claudeCoverRules is a one-rule rules file, the way every test here loads one: `schema`
// names any path that mentions it.
func claudeCoverRules(t *testing.T) *Rules {
	t.Helper()
	path := filepath.Join(t.TempDir(), "repos.tsv")
	require.NoError(t, os.WriteFile(path, []byte("schema\t(^|/)schema($|/)\n"), 0o644))
	rules, err := LoadRules(path)
	require.NoError(t, err)
	return rules
}

// claudeCoverTranscript writes one transcript body into dir under name, making the
// directories it needs, and returns the path.
func claudeCoverTranscript(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
	return path
}

// claudeCoverMessage finds one folded message by id, so an assertion names the turn it pins.
func claudeCoverMessage(stream []Message, id string) Message {
	for _, m := range stream {
		if m.ID == id {
			return m
		}
	}
	return Message{}
}

// TestClaudeCoverToolInputsPullsEveryToolUseInput: a content array's tool_use inputs are
// the strings the reader attributes from, and a content of any other shape -- a user
// turn's string, a null, prose blocks, text that is not JSON at all -- is a turn with
// nothing to attribute from, never an error.
func TestClaudeCoverToolInputsPullsEveryToolUseInput(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		content string
		want    []string
	}{
		{
			name:    "a tool_use block's input strings are the inputs",
			content: `[{"type":"text","text":"reading"},{"type":"tool_use","name":"Read","input":{"file_path":"/w/schema/a.go"}}]`,
			want:    []string{"/w/schema/a.go"},
		},
		{
			name:    "every tool_use block contributes, in block order, nested strings included",
			content: `[{"type":"tool_use","input":{"a":"/w/schema/one.go"}},{"type":"tool_use","input":{"b":"note","c":["/w/schema/two.go"]}}]`,
			want:    []string{"/w/schema/one.go", "note", "/w/schema/two.go"},
		},
		{
			name:    "an array with no tool_use block names no input",
			content: `[{"type":"text","text":"just prose"}]`,
			want:    nil,
		},
		{
			name:    "a string content is a user turn, not an error",
			content: `"a user turn carries no tool call"`,
			want:    nil,
		},
		{
			name:    "a content that is not an array is refused",
			content: `{"type":"tool_use","input":{"a":"/w/schema/x.go"}}`,
			want:    nil,
		},
		{
			name:    "an array that is not an array of blocks is refused",
			content: `[1,2]`,
			want:    nil,
		},
		{
			name:    "a content that is not JSON is refused",
			content: `not json`,
			want:    nil,
		},
		{
			name:    "an empty content has nothing to read",
			content: ``,
			want:    nil,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := toolInputs(json.RawMessage(tc.content))
			assert.Equalf(t, tc.want, got, "toolInputs(%s) = %v, want %v", tc.content, got, tc.want)
		})
	}
}

// TestClaudeCoverJSONStringsWalksEveryStringInTheValue: a tool_use input is a shape this
// tool does not know, so a path may sit under any key or inside any array, and the walk
// reaches every string of it; text that is not JSON names no string and is not an error.
func TestClaudeCoverJSONStringsWalksEveryStringInTheValue(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		raw  string
		want []string
	}{
		{
			name: "an object's strings come out in sorted key order",
			raw:  `{"b":"/w/schema/two","a":"/w/schema/one"}`,
			want: []string{"/w/schema/one", "/w/schema/two"},
		},
		{
			name: "an array's strings come out in order, nested values included",
			raw:  `["/w/schema/one",{"k":["/w/schema/two","note"]},true]`,
			want: []string{"/w/schema/one", "/w/schema/two", "note"},
		},
		{
			name: "a bare string is the one string",
			raw:  `"just prose"`,
			want: []string{"just prose"},
		},
		{
			name: "numbers, booleans and nulls are not strings",
			raw:  `{"n":3,"ok":true,"z":null}`,
			want: nil,
		},
		{
			name: "text that is not JSON names no string",
			raw:  `not json`,
			want: nil,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := jsonStrings(json.RawMessage(tc.raw))
			assert.Equalf(t, tc.want, got, "jsonStrings(%s) = %v, want %v", tc.raw, got, tc.want)
		})
	}
}

// TestClaudeCoverReadClaudeFoldsTheTranscriptTree: every *.jsonl and *.output under the
// directory, in sorted path order, folded through the one AddMessage rule -- the last line
// of a streamed id wins and counts as dup= once, a line with no id is noid=, and the
// harness talking to itself is not a message a model is paid for.
func TestClaudeCoverReadClaudeFoldsTheTranscriptTree(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	claudeCoverTranscript(t, dir, "log.output", `{"timestamp":"2026-09-16T08:00:00Z","message":{"id":"o1","model":"claude-opus-5","usage":{"input_tokens":1,"output_tokens":1}}}
`)
	claudeCoverTranscript(t, dir, "nested/nested.jsonl", `{"timestamp":"2026-09-16T08:30:00Z","message":{"id":"n1","model":"claude-opus-5","usage":{"input_tokens":2,"output_tokens":2}}}
`)
	claudeCoverTranscript(t, dir, "notes.txt", "a file of another suffix is not a transcript")
	claudeCoverTranscript(t, dir, "session.jsonl", strings.Join([]string{
		// The cwd is the lowest rung: no tool call has named a path yet, and no repo went before.
		`{"timestamp":"2026-09-16T09:00:00Z","cwd":"/w/schema","message":{"id":"m1","model":"claude-opus-5","usage":{"input_tokens":10,"output_tokens":2}}}`,
		// A tool_use input names the repo; the four usage keys each land on their own type.
		`{"timestamp":"2026-09-16T09:01:00Z","message":{"id":"m2","model":"claude-opus-5","content":[{"type":"tool_use","name":"Bash","input":{"command":"cd /w/schema && ls"}}],"usage":{"input_tokens":20,"cache_creation_input_tokens":5,"cache_read_input_tokens":7,"output_tokens":3}}}`,
		// A streamed turn: the same id twice with a growing usage block, the LAST line the message.
		`{"timestamp":"2026-09-16T09:02:00Z","message":{"id":"m3","model":"claude-opus-5","usage":{"input_tokens":7,"output_tokens":100}}}`,
		`{"timestamp":"2026-09-16T09:02:02Z","message":{"id":"m3","model":"claude-opus-5","usage":{"input_tokens":7,"output_tokens":210}}}`,
		// A message with no usage block is not a measurement, and not an error.
		`{"timestamp":"2026-09-16T09:04:00Z","message":{"id":"m5","model":"claude-opus-5"}}`,
		// The harness talking to itself: counted nowhere.
		`{"timestamp":"2026-09-16T09:03:00Z","message":{"id":"m4","model":"<synthetic>","usage":{"input_tokens":9999,"output_tokens":9999}}}`,
		// A user turn with no id: counted in noid=, never folded as a second row.
		`{"timestamp":"2026-09-16T09:05:00Z","message":{"content":"fix the test","usage":{"input_tokens":1,"output_tokens":1}}}`,
		"",
	}, "\n"))

	s := ReadClaude("seat-a", dir, os.DirFS(dir), claudeCoverRules(t))

	assert.Equalf(t, "claude:seat-a", s.Label, "label=%q, want claude:seat-a", s.Label)
	assert.Equalf(t, KindClaude, s.Kind, "kind=%q, want %q", s.Kind, KindClaude)
	assert.Equalf(t, UTC, s.Basis, "basis=%q, want %q: every row is dated from a stamp", s.Basis, UTC)
	assert.Equalf(t, 3, s.Stat.Files, "files=%d, want 3: two .jsonl and one .output, and the .txt is not a transcript", s.Stat.Files)
	assert.Equalf(t, 0, s.Stat.Unreadable, "unreadable=%d, want 0", s.Stat.Unreadable)
	assert.Equalf(t, 0, s.Stat.Unparsed, "unparsed=%d, want 0", s.Stat.Unparsed)
	assert.Equalf(t, 1, s.Stat.Dup, "dup=%d, want 1: m3 wrote its id on two lines", s.Stat.Dup)
	assert.Equalf(t, 1, s.Stat.NoID, "noid=%d, want 1: the user turn carries no id", s.Stat.NoID)
	assert.Equalf(t, 5, s.Stat.Messages, "messages=%d, want 5: o1, n1, m1, m2 and m3, with the synthetic turn absent", s.Stat.Messages)

	o1 := claudeCoverMessage(s.Stream, "o1")
	assert.Equalf(t, Unknown, o1.Repo, "o1 repo=%q, want %q: nothing has named a repo yet", o1.Repo, Unknown)
	m1 := claudeCoverMessage(s.Stream, "m1")
	assert.Equalf(t, "2026-09-16", m1.Day, "m1 day=%q, want 2026-09-16", m1.Day)
	assert.Equalf(t, "schema", m1.Repo, "m1 repo=%q, want schema: the cwd is the lowest rung of the ladder", m1.Repo)
	assert.Equalf(t, "10", m1.Counts.Cell(Input), "m1 input=%s, want 10", m1.Counts.Cell(Input))
	assert.Equalf(t, "2", m1.Counts.Cell(Output), "m1 output=%s, want 2", m1.Counts.Cell(Output))
	assert.Truef(t, m1.Turn, "m1 turn=%t, want true: a transcript counts its messages", m1.Turn)
	m2 := claudeCoverMessage(s.Stream, "m2")
	assert.Equalf(t, "schema", m2.Repo, "m2 repo=%q, want schema: the tool_use input named it", m2.Repo)
	assert.Equalf(t, "5", m2.Counts.Cell(CacheWrite), "m2 cache_write=%s, want 5", m2.Counts.Cell(CacheWrite))
	assert.Equalf(t, "7", m2.Counts.Cell(CacheRead), "m2 cache_read=%s, want 7", m2.Counts.Cell(CacheRead))
	m3 := claudeCoverMessage(s.Stream, "m3")
	assert.Equalf(t, "210", m3.Counts.Cell(Output), "m3 output=%s, want 210: the last line for an id is the message", m3.Counts.Cell(Output))
	assert.Equalf(t, Message{}, claudeCoverMessage(s.Stream, "m4"), "the synthetic turn is not a message a model is paid for")
	assert.Equalf(t, Message{}, claudeCoverMessage(s.Stream, "m5"), "a message with no usage block is not a measurement")
	assert.Equalf(t, Message{}, claudeCoverMessage(s.Stream, ""), "the user turn with no id is counted in noid= and never folded")
}

// TestClaudeCoverReadClaudeRefusesWhatItCannotRead: an absent directory, a file that will
// not open, a line that is not JSON and a stamp this tool cannot read are each counted and
// named -- never silent, and never one instead of the other.
func TestClaudeCoverReadClaudeRefusesWhatItCannotRead(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		write func(t *testing.T) string
		want  func(t *testing.T, s *Source)
	}{
		{
			name: "an absent directory is one unreadable and nothing folded",
			write: func(t *testing.T) string {
				t.Helper()
				return filepath.Join(t.TempDir(), "absent")
			},
			want: func(t *testing.T, s *Source) {
				t.Helper()
				require.Equalf(t, 1, s.Stat.Unreadable, "unreadable=%d, want 1", s.Stat.Unreadable)
				assert.Equalf(t, s.Path, s.Unreadables[0].Path, "the unreadable names the directory itself, got %q", s.Unreadables[0].Path)
				assert.Truef(t, strings.Contains(s.Unreadables[0].Why, "no such file"), "why=%q, want the walk's own error", s.Unreadables[0].Why)
				assert.Equalf(t, 0, s.Stat.Files, "files=%d, want 0", s.Stat.Files)
				assert.Lenf(t, s.Stream, 0, "stream=%d, want 0", len(s.Stream))
			},
		},
		{
			name: "a transcript that will not open is counted and named",
			write: func(t *testing.T) string {
				t.Helper()
				dir := t.TempDir()
				require.NoError(t, os.Symlink(filepath.Join(dir, "nowhere"), filepath.Join(dir, "broken.jsonl")))
				return dir
			},
			want: func(t *testing.T, s *Source) {
				t.Helper()
				assert.Equalf(t, 1, s.Stat.Files, "files=%d, want 1: the file was attempted", s.Stat.Files)
				require.Equalf(t, 1, s.Stat.Unreadable, "unreadable=%d, want 1", s.Stat.Unreadable)
				assert.Truef(t, strings.HasSuffix(s.Unreadables[0].Path, "broken.jsonl"), "path=%q, want the file that would not open", s.Unreadables[0].Path)
				assert.Truef(t, strings.Contains(s.Unreadables[0].Why, "no such file"), "why=%q, want the open's own error", s.Unreadables[0].Why)
			},
		},
		{
			name: "lines that are not JSON are one badline and the rest was read",
			write: func(t *testing.T) string {
				t.Helper()
				dir := t.TempDir()
				claudeCoverTranscript(t, dir, "session.jsonl", strings.Join([]string{
					`{"timestamp":"2026-09-16T09:00:00Z","message":{"id":"m1","model":"m","usage":{"input_tokens":3,"output_tokens":1}}}`,
					"not json at all",
					"",
					`{"timestamp":"2026-09-16T09:01:00Z","message":{"id":"m2","model":"m","usage":{"input_tokens":4,"output_tokens":2}}}`,
					"",
				}, "\n"))
				return dir
			},
			want: func(t *testing.T, s *Source) {
				t.Helper()
				assert.Equalf(t, 2, s.Stat.Messages, "messages=%d, want 2: the readable lines still fold", s.Stat.Messages)
				require.Equalf(t, 1, s.Stat.Unreadable, "unreadable=%d, want 1", s.Stat.Unreadable)
				assert.Truef(t, strings.Contains(s.Unreadables[0].Why, "badline=1"), "why=%q, want the bad line's count", s.Unreadables[0].Why)
				assert.Truef(t, strings.Contains(s.Unreadables[0].Why, "the rest of the file was read"), "why=%q, want the rest of the file read", s.Unreadables[0].Why)
			},
		},
		{
			name: "a stamp this tool cannot read is unparsed, not unreadable",
			write: func(t *testing.T) string {
				t.Helper()
				dir := t.TempDir()
				claudeCoverTranscript(t, dir, "session.jsonl", `{"timestamp":"yesterday noon","message":{"id":"m1","model":"m","usage":{"input_tokens":3,"output_tokens":1}}}`+"\n")
				return dir
			},
			want: func(t *testing.T, s *Source) {
				t.Helper()
				assert.Equalf(t, 0, s.Stat.Unreadable, "unreadable=%d, want 0", s.Stat.Unreadable)
				assert.Equalf(t, 1, s.Stat.Unparsed, "unparsed=%d, want 1", s.Stat.Unparsed)
				require.Lenf(t, s.Unparseds, 1, "unparseds=%d, want 1", len(s.Unparseds))
				assert.Equalf(t, 1, s.Unparseds[0].Line, "line=%d, want 1", s.Unparseds[0].Line)
				assert.Truef(t, strings.Contains(s.Unparseds[0].Text, "yesterday noon"), "text=%q, want the stamp that could not be read", s.Unparseds[0].Text)
			},
		},
		{
			name: "an empty directory folds nothing and refuses nothing",
			write: func(t *testing.T) string {
				t.Helper()
				return t.TempDir()
			},
			want: func(t *testing.T, s *Source) {
				t.Helper()
				assert.Equalf(t, 0, s.Stat.Files, "files=%d, want 0", s.Stat.Files)
				assert.Equalf(t, 0, s.Stat.Unreadable, "unreadable=%d, want 0", s.Stat.Unreadable)
				assert.Equalf(t, 0, s.Stat.Messages, "messages=%d, want 0", s.Stat.Messages)
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := tc.write(t)
			s := ReadClaude("seat-a", dir, os.DirFS(dir), claudeCoverRules(t))
			tc.want(t, s)
		})
	}
}

// TestClaudeCoverUnreadableCountsAndNamesTheSource: a source this run could not read is
// counted, printed on its own line, and carries the label that declared it -- the reason
// the run exits 1.
func TestClaudeCoverUnreadableCountsAndNamesTheSource(t *testing.T) {
	t.Parallel()

	s := &Source{Label: Label(KindClaude, "seat-a")}
	s.unreadable("/w/lost.jsonl", "permission denied")
	s.unreadable("/w/gone.jsonl", "no such file or directory")

	assert.Equalf(t, 2, s.Stat.Unreadable, "unreadable=%d, want 2", s.Stat.Unreadable)
	assert.Equalf(t, []Unreadable{
		{Label: "claude:seat-a", Path: "/w/lost.jsonl", Why: "permission denied"},
		{Label: "claude:seat-a", Path: "/w/gone.jsonl", Why: "no such file or directory"},
	}, s.Unreadables, "the unreadables name the label, the path and the reason: %+v", s.Unreadables)
}
