package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// foldStamp is the clock every test hands run(), so that `at=` is a fixture and not a
// reading of the machine the test happens to run on.
var foldStamp = time.Date(2026, 9, 11, 23, 55, 2, 0, time.UTC)

type result struct {
	exit   int
	stdout string
	stderr string
}

func (r result) all() string { return r.stdout + r.stderr }

// invoke runs the binary in process, with the clock injected.
func invoke(t *testing.T, args ...string) result {
	t.Helper()
	return invokeAt(t, foldStamp, args...)
}

func invokeAt(t *testing.T, now time.Time, args ...string) result {
	t.Helper()
	var out, errb bytes.Buffer
	exit := run(args, &out, &errb, now)
	return result{exit: exit, stdout: out.String(), stderr: errb.String()}
}

func wantExit(t *testing.T, r result, want int) {
	t.Helper()
	if r.exit != want {
		t.Errorf("exit %d, want %d\nstdout:\n%s\nstderr:\n%s", r.exit, want, r.stdout, r.stderr)
	}
}

func wantContains(t *testing.T, got, want string) {
	t.Helper()
	if !strings.Contains(got, want) {
		t.Errorf("output does not contain %q:\n%s", want, got)
	}
}

func wantNotContains(t *testing.T, got, want string) {
	t.Helper()
	if strings.Contains(got, want) {
		t.Errorf("output contains %q and should not:\n%s", want, got)
	}
}

// lineWith returns the first line of s holding every one of the substrings.
func lineWith(s string, subs ...string) string {
	for _, line := range strings.Split(s, "\n") {
		ok := true
		for _, sub := range subs {
			if !strings.Contains(line, sub) {
				ok = false
				break
			}
		}
		if ok {
			return line
		}
	}
	return ""
}

func write(t *testing.T, path, content string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func read(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func mkdir(t *testing.T, path string) string {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// reposFile writes a rules file naming schema and serialize.
func reposFile(t *testing.T, dir string) string {
	t.Helper()
	return write(t, filepath.Join(dir, "repos.tsv"), strings.Join([]string{
		"# name<TAB>regexp, in priority order",
		"schema\t(^|/)schema($|/)",
		"serialize\t(^|/)serialize($|/)",
		"",
	}, "\n"))
}

// msg renders one Claude Code transcript line.
func msg(id, stamp, model string, usage map[string]int, paths ...string) string {
	u := "{"
	first := true
	for _, k := range []string{"input_tokens", "output_tokens", "cache_creation_input_tokens", "cache_read_input_tokens"} {
		v, ok := usage[k]
		if !ok {
			continue
		}
		if !first {
			u += ","
		}
		first = false
		u += fmt.Sprintf("%q:%d", k, v)
	}
	u += "}"
	content := ""
	if len(paths) > 0 {
		quoted := make([]string, 0, len(paths))
		for _, p := range paths {
			quoted = append(quoted, fmt.Sprintf("%q", p))
		}
		content = fmt.Sprintf(`,"content":[{"type":"tool_use","name":"Read","input":{"file_path":%s}}]`, quoted[0])
		if len(quoted) > 1 {
			content = fmt.Sprintf(`,"content":[{"type":"tool_use","name":"Read","input":{"file_path":%s,"pattern":%s}}]`, quoted[0], quoted[1])
		}
	}
	idField := ""
	if id != "" {
		idField = fmt.Sprintf(`"id":%q,`, id)
	}
	return fmt.Sprintf(`{"type":"assistant","timestamp":%q,"message":{%s"model":%q,"usage":%s%s}}`, stamp, idField, model, u, content)
}

// fakeSqlite3 puts a stub sqlite3 on PATH whose answers come from the three files named,
// and which records every invocation's argv into a log the test reads.
//
// The three answers are what `sqlite3 -json` prints: a JSON array of row objects keyed by
// the SELECT's own aliases. The shape they describe is OpenCode's REAL schema, read off
// ~/.local/share/opencode/opencode.db on 2026-09-11: `session` carries `parent_id` and
// `directory` as columns of its own, while `message` and `part` carry `id`, `session_id`,
// `time_created` (epoch MILLISECONDS) and one `data` column holding the row as JSON --
// which is where `providerID`, `modelID`, `tokens.input`, `tokens.cache.write`,
// `path.cwd` and a tool part's `state.input.*` live. A fake that answered bare columns
// would be a fixture only this code could read.
func fakeSqlite3(t *testing.T, sessions, messages, parts string) (logPath string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake sqlite3 is a shell script; the OpenCode source is exercised on unix here")
	}
	dir := t.TempDir()
	logPath = filepath.Join(dir, "argv.log")
	answers := filepath.Join(dir, "answers")
	mkdir(t, answers)
	write(t, filepath.Join(answers, "sessions"), sessions)
	write(t, filepath.Join(answers, "messages"), messages)
	write(t, filepath.Join(answers, "parts"), parts)
	bin := mkdir(t, filepath.Join(dir, "bin"))
	script := `#!/bin/sh
echo "$@" >> ` + logPath + `
last=""
for a in "$@"; do last="$a"; done
case "$last" in
  *FROM\ session*) cat ` + answers + `/sessions ;;
  *FROM\ message*) cat ` + answers + `/messages ;;
  *FROM\ part*) cat ` + answers + `/parts ;;
  *) : ;;
esac
`
	p := filepath.Join(bin, "sqlite3")
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return logPath
}

// ocSession is one row of `sqlite3 -json` over the sessions query.
func ocSession(id, parent, dir string) string {
	return fmt.Sprintf(`{"id":%q,"parent_id":%s,"directory":%q}`, id, jsonOrNull(parent), dir)
}

// ocMessage is one assistant message as the real `message` table yields it: the stamp is
// what strftime makes of `time_created`, and the five counts and the model come out of the
// JSON `data` column. A count given as "-" is SQL NULL: the column the row does not carry.
func ocMessage(id, session, stamp, provider, model, in, out, cw, cr, rsn, cwd string) string {
	return fmt.Sprintf(`{"id":%q,"session_id":%q,"stamp":%s,"provider":%q,"model":%q,"input":%s,"output":%s,"cache_write":%s,"cache_read":%s,"reasoning":%s,"cwd":%q}`,
		id, session, jsonOrNull(stamp), provider, model,
		numOrNull(in), numOrNull(out), numOrNull(cw), numOrNull(cr), numOrNull(rsn), cwd)
}

// ocPart is one tool part: the four inputs SPEC-TOKENS names, each NULL when absent.
func ocPart(message, session, command, filePath, path, pattern string) string {
	return fmt.Sprintf(`{"message_id":%q,"session_id":%q,"command":%s,"file_path":%s,"path":%s,"pattern":%s}`,
		message, session, jsonOrNull(command), jsonOrNull(filePath), jsonOrNull(path), jsonOrNull(pattern))
}

// ocRows joins row objects into the array sqlite3 -json prints, or the empty answer.
func ocRows(rows ...string) string {
	if len(rows) == 0 {
		return ""
	}
	return "[" + strings.Join(rows, ",") + "]\n"
}

func jsonOrNull(v string) string {
	if v == "" {
		return "null"
	}
	return fmt.Sprintf("%q", v)
}

func numOrNull(v string) string {
	if v == "" || v == "-" {
		return "null"
	}
	return v
}

// swarmHeader is transcribed from SPEC-SWARM.md rule 12, whose sentence reads: "one
// header line and one row, tab-separated, these columns in this order: `job`, `attempt`,
// `from`, `started`, `ended`, `end`, `rc`, `provider`, `model`, `repo`, `tokens_in`,
// `tokens_out`, `cache_write`, `cache_read`, `reasoning`, `usd`." It is written from that
// text and NEVER from tokens.SwarmColumns: a fixture copied from the constant it is
// meant to check is a test that cannot fail.
var swarmHeader = []string{
	"job", "attempt", "from", "started", "ended", "end", "rc", "provider",
	"model", "repo", "tokens_in", "tokens_out", "cache_write", "cache_read", "reasoning", "usd",
}

// swarmRow writes one row in swarmHeader's order, the way SPEC-SWARM's finalize writes it.
func swarmRow(job, attempt, from, model, repo, ended string, in, out, cw, cr, rsn string) string {
	return strings.Join([]string{
		job, attempt, from, ended, ended, "done", "0", "deepseek",
		model, repo, in, out, cw, cr, rsn, "-",
	}, "\t")
}

func swarmUsage(t *testing.T, pool, job string, row string) {
	t.Helper()
	write(t, filepath.Join(pool, "usage", job+".tsv"), strings.Join(swarmHeader, "\t")+"\n"+row+"\n")
}

// busLane writes a roster and returns the bus directory.
func busDir(t *testing.T, dir string, names ...string) string {
	t.Helper()
	var ps []string
	for _, n := range names {
		ps = append(ps, fmt.Sprintf(`{"name":%q,"lane":"from-%s","git_email":"%s@example.com"}`, strings.Title(n), n, n))
	}
	write(t, filepath.Join(dir, "participants.json"), "{\"participants\":["+strings.Join(ps, ",")+"]}\n")
	return dir
}

// busNote writes one note into a lane and returns its id.
func busNote(t *testing.T, bus, lane, file, id, subject, date, body string) string {
	t.Helper()
	header := fmt.Sprintf("From: %s\nTo: Rowan\nDate: %s\nId: %s\nSubject: %s\n\n", strings.Title(lane), date, id, subject)
	write(t, filepath.Join(bus, "from-"+lane, file), header+body)
	return id
}

const busDate = "Fri Sep 11 20:00:00 UTC 2026"
