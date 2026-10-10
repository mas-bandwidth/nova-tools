package main

import (
	"bytes"
	"fmt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/testbin"
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
	assert.Equal(t, want, r.exit, "exit %d, want %d\nstdout:\n%s\nstderr:\n%s", r.exit, want, r.stdout, r.stderr)
}

func wantContains(t *testing.T, got, want string) {
	t.Helper()
	assert.True(t, strings.Contains(got, want), "output does not contain %q:\n%s", want, got)
}

func wantNotContains(t *testing.T, got, want string) {
	t.Helper()
	assert.False(t, strings.Contains(got, want), "output contains %q and should not:\n%s", want, got)
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
	{
		err := os.MkdirAll(filepath.Dir(path), 0o755)
		require.NoError(t, err)
	}
	{
		err := os.WriteFile(path, []byte(content), 0o644)
		require.NoError(t, err)
	}
	return path
}

func read(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(raw)
}

func mkdir(t *testing.T, path string) string {
	t.Helper()
	{
		err := os.MkdirAll(path, 0o755)
		require.NoError(t, err)
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

// fakeSqlite3 puts a stub sqlite3 on the child's PATH whose answers come from the three
// files named, and which records every invocation's argv into a log the test reads. It
// returns that log's path and the environment (PATH and the stub's mode) the run under
// test needs; the caller hands the environment to runToolChild, so the stub is on the
// child's PATH rather than this test process's, which its parallel neighbours share.
//
// The three answers are what `sqlite3 -json` prints: a JSON array of row objects keyed by
// the SELECT's own aliases. The shape they describe is OpenCode's REAL schema, read off
// ~/.local/share/opencode/opencode.db on 2026-09-11: `session` carries `parent_id` and
// `directory` as columns of its own, while `message` and `part` carry `id`, `session_id`,
// `time_created` (epoch MILLISECONDS) and one `data` column holding the row as JSON --
// which is where `providerID`, `modelID`, `tokens.input`, `tokens.cache.write`,
// `path.cwd` and a tool part's `state.input.*` live. A fake that answered bare columns
// would be a fixture only this code could read.
func fakeSqlite3(t *testing.T, sessions, messages, parts string) (logPath string, env []string) {
	t.Helper()
	answers := mkdir(t, filepath.Join(t.TempDir(), "answers"))
	write(t, filepath.Join(answers, "sessions"), sessions)
	write(t, filepath.Join(answers, "messages"), messages)
	write(t, filepath.Join(answers, "parts"), parts)
	env = fakeSqlite3OnPath(t, answers)
	return filepath.Join(answers, fakeArgvLog), env
}

// The fake sqlite3 is THIS TEST BINARY under another name, re-entered through TestMain.
//
// It used to be a /bin/sh script, which Windows has no way to execute and no way to find
// without the .exe suffix: every OpenCode test skipped there, and rule 19's own stub was
// not even a program, so the fold refused ("sqlite3 is not on PATH") instead of timing out
// and the rule went untested on a whole platform (measured in CI 2026-09-11). A copy of
// the test binary is a real executable on all three, and the behaviour is written once, in
// Go, rather than twice in two shell dialects.
const (
	fakeSqlite3Env = "NOVA_TOKENS_FAKE_SQLITE3"
	fakeArgvLog    = "argv.log"
)

// fakeModes are the fake's modes beyond answering from files, registered by the tier
// whose tests use them (slow_test.go's sleeping sqlite3), so no unit-tier file holds a
// wait on the wall clock.
var fakeModes = map[string]func() int{}

// fakeSqlite3OnPath places the test binary (by link, a copy only where a link is not
// possible) at <tmp>/bin/sqlite3[.exe], and returns the environment that puts that
// directory first on a child's PATH and hands the placed program its mode. The caller
// passes it to runToolChild; this process's PATH is never touched.
func fakeSqlite3OnPath(t *testing.T, mode string) (env []string) {
	t.Helper()
	self, err := os.Executable()
	require.NoError(t, err)
	bin := mkdir(t, filepath.Join(t.TempDir(), "bin"))
	name := "sqlite3"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	{
		err := testbin.Place(self, filepath.Join(bin, name))
		require.NoError(t, err)
	}
	return []string{
		"PATH=" + bin + string(os.PathListSeparator) + os.Getenv("PATH"),
		fakeSqlite3Env + "=" + mode,
	}
}

// childTestEnv marks a test re-entered as a child of this binary. A test that needs a
// process-wide resource t.Parallel forbids in a shared process -- the environment, or the
// package's own opened-file counter -- runs its body in a child with only itself selected,
// so the resource is that process's alone. The parent execs; the child asserts.
const childTestEnv = "NOVA_TOKENS_CHILD_TEST"

// childEnv is this process's environment with testbin's re-exec depth counter dropped, so
// a child test binary starts a fresh chain. Without it the child starts one deep and the
// fake sqlite3 it starts is the third test binary, which testbin.MaxDepth refuses.
func childEnv(extra ...string) []string {
	depth := testbin.DepthEnv("nova-tokens")
	out := make([]string, 0, len(os.Environ())+len(extra))
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, depth+"=") {
			continue
		}
		out = append(out, kv)
	}
	return append(out, extra...)
}

// reenterTest runs this test function again in a child of this binary, with only it
// selected (`-test.run`), so its body may set the environment or read the process-wide
// counter without racing a parallel neighbour. The child's failure fails this test.
func reenterTest(t *testing.T, name string) {
	t.Helper()
	self, err := os.Executable()
	require.NoError(t, err)
	cmd := exec.Command(self, "-test.run=^"+name+"$", "-test.count=1")
	cmd.Env = childEnv(childTestEnv + "=1")
	cmd.WaitDelay = 2 * time.Second
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "the re-entered %s failed:\n%s", name, out)
}

// runToolChild runs this test binary as nova-tokens itself in a child process, so a test
// can give the tool its own working directory (cmd.Dir) and environment (cmd.Env) without
// changing either for the process its parallel neighbours share. The child's clock is
// foldStamp, the one invoke injects, so the two render the same at= stamps.
func runToolChild(t *testing.T, dir string, env []string, args ...string) result {
	t.Helper()
	self, err := os.Executable()
	require.NoError(t, err)
	cmd := exec.Command(self, args...)
	cmd.Dir = dir
	cmd.Env = childEnv(append([]string{asToolEnv + "=1"}, env...)...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err = cmd.Run()
	exit := 0
	if ee, ok := err.(*exec.ExitError); ok {
		exit = ee.ExitCode()
	} else if err != nil {
		require.FailNow(t, "running nova-tokens in a child: %v", err)
	}
	return result{exit: exit, stdout: out.String(), stderr: errb.String()}
}

// TestMain is the fake's other half: with the mode set in the environment this binary is
// not a test run at all but the stub sqlite3 the run under test just executed.
//
// With asToolEnv set it is nova-tokens itself, on the process's real stdout and stderr, so
// a test can see what a library writes to os.Stderr behind run's injected streams (#3463).
// That check comes first: a tool run hands its own environment to the sqlite3 it spawns, so
// its environment names the fake too; clearing the tool marker before run() leaves the fake
// for the sqlite3 child, and one dispatch serves both.
func TestMain(m *testing.M) {
	if os.Getenv(asToolEnv) != "" {
		_ = os.Unsetenv(asToolEnv)
		os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, foldStamp))
	}
	if mode := os.Getenv(fakeSqlite3Env); mode != "" {
		os.Exit(fakeSqlite3Main(mode, os.Args[1:], os.Stdout))
	}
	os.Exit(m.Run())
}

// fakeSqlite3Main records the invocation and answers the last argument, which is the SQL.
func fakeSqlite3Main(mode string, args []string, stdout io.Writer) int {
	if m, ok := fakeModes[mode]; ok {
		return m()
	}
	f, err := os.OpenFile(filepath.Join(mode, fakeArgvLog), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Fprintln(f, strings.Join(args, " "))
	// ignored: the argv line is written unbuffered, and a lost record fails the test that reads the log back
	_ = f.Close()
	if len(args) == 0 {
		return 0
	}
	// The fixture answers in the -json shape and ONLY in it. The spec's --opencode
	// section says `sqlite3 -readonly -tabs`, and a tool part's `command` input holds
	// tabs and newlines, so a tabs answer splits a row on the data inside it: the code
	// asks for -json and this fake is what proves it must.
	if !slices.Contains(args, "-json") {
		fmt.Fprintln(os.Stderr, "this fake sqlite3 answers -json only: a row of the real database carries tabs and newlines inside a tool part's command, and -tabs splits on them")
		return 1
	}
	sql := args[len(args)-1]
	answer := ""
	switch {
	case strings.Contains(sql, "FROM session"):
		answer = "sessions"
	case strings.Contains(sql, "FROM message"):
		answer = "messages"
	case strings.Contains(sql, "FROM part"):
		answer = "parts"
	default:
		return 0
	}
	raw, err := os.ReadFile(filepath.Join(mode, answer))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if _, err := stdout.Write(raw); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
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

// swarmHeader is transcribed from SPEC-WORKER.md rule 12, whose sentence reads: "one
// header line and one row, tab-separated, these columns in this order: `job`, `attempt`,
// `from`, `started`, `ended`, `end`, `rc`, `provider`, `model`, `repo`, `tokens_in`,
// `tokens_out`, `cache_write`, `cache_read`, `reasoning`, `usd`." It is written from that
// text and NEVER from tokens.SwarmColumns: a fixture copied from the constant it is
// meant to check is a test that cannot fail.
var swarmHeader = []string{
	"job", "attempt", "from", "started", "ended", "end", "rc", "provider",
	"model", "repo", "tokens_in", "tokens_out", "cache_write", "cache_read", "reasoning", "usd",
}

// swarmRow writes one row in swarmHeader's order, the way SPEC-WORKER's finalize writes it.
func swarmRow(job, attempt, from, model, repo, ended string, in, out, cw, cr, rsn string) string {
	return strings.Join([]string{
		job, attempt, from, ended, ended, "done", "0", "deepseek",
		model, repo, in, out, cw, cr, rsn, "-",
	}, "\t")
}

// swarmRowCost is swarmRow with the two cells it keeps fixed — provider and usd — made
// explicit, so an AVG-line test can carry a cost and a provider prefix.
func swarmRowCost(job, attempt, from, provider, model, repo, ended string, in, out, cw, cr, rsn, usd string) string {
	return strings.Join([]string{
		job, attempt, from, ended, ended, "done", "0", provider,
		model, repo, in, out, cw, cr, rsn, usd,
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
		ps = append(ps, fmt.Sprintf(`{"name":%q,"lane":"from-%s","git_email":"%s@example.com"}`, cases.Title(language.Und, cases.NoLower).String(n), n, n))
	}
	write(t, filepath.Join(dir, "participants.json"), "{\"participants\":["+strings.Join(ps, ",")+"]}\n")
	return dir
}

// busNote writes one note into a lane and returns its id.
func busNote(t *testing.T, bus, lane, file, id, subject, date, body string) string {
	t.Helper()
	header := fmt.Sprintf("From: %s\nTo: Rowan\nDate: %s\nId: %s\nSubject: %s\n\n", cases.Title(language.Und, cases.NoLower).String(lane), date, id, subject)
	write(t, filepath.Join(bus, "from-"+lane, file), header+body)
	return id
}

const busDate = "Fri Sep 11 20:00:00 UTC 2026"
