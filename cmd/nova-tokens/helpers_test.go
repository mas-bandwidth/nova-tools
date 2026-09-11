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

// swarmHeader is SPEC-SWARM rule 12's sixteen columns, in order.
var swarmHeader = []string{
	"job", "task", "attempt", "model", "repo", "started", "ended", "seconds",
	"tokens_in", "tokens_out", "cache_write", "cache_read", "reasoning", "cost", "exit", "note",
}

func swarmRow(job, task, attempt, model, repo, ended string, in, out, cw, cr, rsn string) string {
	return strings.Join([]string{
		job, task, attempt, model, repo, ended, ended, "12",
		in, out, cw, cr, rsn, "-", "0", "-",
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
