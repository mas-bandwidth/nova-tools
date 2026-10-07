package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/mas-bandwidth/nova-tools/internal/bus/bustest"
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
	exit := runWith(args, &out, &errb, now, testWorld)
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
		os.Exit(runWith(os.Args[1:], os.Stdout, os.Stderr, foldStamp, childWorld()))
	}
	if mode := os.Getenv(fakeSqlite3Env); mode != "" {
		os.Exit(fakeSqlite3Main(mode, os.Args[1:], os.Stdout))
	}
	os.Exit(m.Run())
}

// childWorld is the world a re-executed tool runs with: the fake bus a parent test wrote
// into the working directory where there is one, else the real one. It is the child half of
// testWorld, and it opens no socket.
func childWorld() world {
	raw, err := os.ReadFile(testBusSnapshotFile)
	if err != nil {
		return realWorld()
	}
	var s testBusSnapshot
	if err := json.Unmarshal(raw, &s); err != nil {
		return realWorld()
	}
	fake := bustest.NewFake(time.Date(2026, 9, 11, 21, 0, 0, 0, time.UTC), s.Names...)
	for _, e := range s.Log {
		// ignored: the fake's AddAll fails only when its Fail is set, which a snapshot never is
		_ = fake.AddAll(context.Background(), e.Streams, e.Fields)
	}
	return world{openBus: func(_ context.Context, addr string) (bus.Store, func(), error) {
		if addr != s.Addr {
			return nil, nil, fmt.Errorf("dial tcp %s: connect: connection refused", addr)
		}
		return fake, func() {}, nil
	}}
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

// testBuses are the fake Redis buses of the running tests, by the address a test passes
// to --bus: busDir makes one under the directory the test owns, which is unique to it.
var testBuses sync.Map // string -> *bustest.Fake

// testBusLogs records what each fake bus was sent, so a child process can be handed the
// same log through a file: runToolChild runs the tool with its own working directory, and a
// child cannot read this process's map. The mutex guards the appends, which sync.Map does not.
var (
	testBusLogsMu sync.Mutex
	testBusLogs   = map[string][]testBusLog{}
)

// testBusLog is one message a fake bus took: the streams and the fields AddAll was given.
type testBusLog struct {
	Streams []string          `json:"streams"`
	Fields  map[string]string `json:"fields"`
}

// testBusSnapshotFile is where a child looks for one fake bus, in its working directory.
const testBusSnapshotFile = ".nova-tokens-test-bus.json"

// testBusSnapshot is that file: the address the documented command names, the roster the
// fake was made with, and the messages it took, in order.
type testBusSnapshot struct {
	Addr  string       `json:"addr"`
	Names []string     `json:"names"`
	Log   []testBusLog `json:"log"`
}

// testWorld is the tool's world in a test: a --bus address names a fake in testBuses and
// nothing opens a socket.
var testWorld = world{openBus: func(_ context.Context, addr string) (bus.Store, func(), error) {
	f, ok := testBuses.Load(addr)
	if !ok {
		return nil, nil, fmt.Errorf("dial tcp %s: connect: connection refused", addr)
	}
	return f.(*bustest.Fake), func() {}, nil
}}

// busDir makes a fake Redis bus whose roster is names and returns its address (the dir).
func busDir(t *testing.T, dir string, names ...string) string {
	t.Helper()
	testBuses.Store(dir, bustest.NewFake(time.Date(2026, 9, 11, 21, 0, 0, 0, time.UTC), names...))
	testBusNames.Store(dir, names)
	testBusLogsMu.Lock()
	delete(testBusLogs, dir)
	testBusLogsMu.Unlock()
	return dir
}

// busDateLayout is how busNote's date argument is written.
const busDateLayout = "Mon Jan  2 15:04:05 UTC 2006"

// busNote puts one message from lane on the bus's log and returns its id. A date that is
// no instant is stored as it is, which the reader reads as no stamp at all.
func busNote(t *testing.T, addr, lane, file, id, subject, date, body string) string {
	t.Helper()
	f, ok := testBuses.Load(addr)
	require.True(t, ok, "no fake bus at %s", addr)
	at := date
	if tm, err := time.Parse(busDateLayout, date); err == nil {
		at = tm.UTC().Format(time.RFC3339)
	}
	fields := map[string]string{"id": id, "from": lane, "to": "rowan", "cc": "", "subject": subject, "re": "", "at": at, "body": body}
	streams := []string{bus.StreamOf("rowan"), bus.LogKey}
	require.NoError(t, f.(*bustest.Fake).AddAll(context.Background(), streams, fields))
	testBusLogsMu.Lock()
	testBusLogs[addr] = append(testBusLogs[addr], testBusLog{Streams: streams, Fields: fields})
	testBusLogsMu.Unlock()
	return id
}

// writeChildBus writes the fake bus at addr to dir as the file childWorld reads, under the
// address docAddr the documented command passes to --bus, so a transcript step can run in a
// child (for its own working directory) against the same log, without a socket.
func writeChildBus(t *testing.T, dir, docAddr, addr string) {
	t.Helper()
	names, ok := testBusNames.Load(addr)
	require.True(t, ok, "no fake bus at %s", addr)
	testBusLogsMu.Lock()
	log := append([]testBusLog(nil), testBusLogs[addr]...)
	testBusLogsMu.Unlock()
	raw, err := json.Marshal(testBusSnapshot{Addr: docAddr, Names: names.([]string), Log: log})
	require.NoError(t, err, err)
	write(t, filepath.Join(dir, testBusSnapshotFile), string(raw))
}

const busDate = "Fri Sep 11 20:00:00 UTC 2026"

// testBusNames are the roster each fake bus was made with, so busClear can start it over.
var testBusNames sync.Map // string -> []string

// busClear empties the bus's log: the next busNote starts a log of its own, which is what a
// test that rewrites a note between two folds means.
func busClear(t *testing.T, addr string) {
	t.Helper()
	names, ok := testBusNames.Load(addr)
	require.True(t, ok, "no fake bus at %s", addr)
	testBuses.Store(addr, bustest.NewFake(time.Date(2026, 9, 11, 21, 0, 0, 0, time.UTC), names.([]string)...))
	testBusLogsMu.Lock()
	delete(testBusLogs, addr)
	testBusLogsMu.Unlock()
}
