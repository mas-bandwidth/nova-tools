package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// A PROVIDER FAILURE IS NOT THE CARD'S (docs/SPEC-CARD-CONTRACT.md section 4;
// tla/CardContract.tla, ProviderFailure).
//
// A run whose provider failed it ends `done rc=0` with no result, and was judged as a
// card that chose to publish nothing: a pro card's child ended 7.5 minutes in, the
// harness's own log held two `stream error ... server_error ... h2 protocol error` lines,
// and the coordinator got a failed-work judgment that was not the card's fault. The
// harness keeps its own record of the run in the data home this tool handed it, and two
// things in it say the provider failed the run when no result was published:
//
//  1. the log carries a provider error (a stream error, server_error, a rate limit, an
//     HTTP 5xx) written by this run, and
//  2. the run's last message is not a final assistant message: the transcript stops on a
//     tool result, which a clean exit 0 never does (seen after 96 commands with no error
//     line at all).
//
// A run with no result and neither stays `no-result` as before. The line this writes is
// the one the member reads (cmd/nova-swarm/member.go, nativeEnd), and the finish it
// causes redeals the card (internal/sprint, providerEnded).
//
// The line names the cause (internal/swarm providercause.go): the class, the status and the
// provider's words, read from the session's own record of the failed message when it has
// one (an API error keeps the provider's status there), else from the log's error line.

// The harness's own record, in the job's data home (the harness profile's paths): its
// log, and its session database, whose `message` table holds one row per message of the
// session, the role and the finish of each in its `data` JSON.
const harnessLogFile = "opencode/log/opencode.log"

// ProviderLogTailBytes is the most of the harness's log this run reads: the tail of what
// the run appended, so a log the slot keeps for many runs is never read whole.
const providerLogTailBytes = 64 << 10

// providerEndedNoMessage is the reason of a run that ended on a tool result.
const providerEndedNoMessage = "ended without a final message"

// providerClass is the NATIVE line's class word; the member's nativeProvider matches it
// with the launch grace's PROVIDER-5XX.
const providerClass = "PROVIDER-FAIL"

// providerSQLTimeout bounds the one read of the session database, and
// providerSQLWaitDelay how long the read waits for its pipe once it is killed.
const (
	providerSQLTimeout   = 10 * time.Second
	providerSQLWaitDelay = 2 * time.Second
)

// providerErrorRE is a provider error on an ERROR line of the harness's log: a stream
// error, a server_error, a rate limit, an overload, or an HTTP 5xx status.
var providerErrorRE = regexp.MustCompile(`(?i)message="?stream error|server_error|rate.?limit|overloaded|\b(?:http|status|statuscode)\W{0,3}5\d\d\b`)

// providerLogError is the first provider error line in what the run appended to the
// harness's log after offset (bounded to its tail), trimmed to the line's own message;
// "" when there is none. The cause read from it (swarm.CauseFromText) is what is bounded.
// The harness prints its error lines on its stderr too (`--print-logs --log-level ERROR` in
// the providers table), so a log that holds none is followed by the parent's own tail of
// that stderr (harnessErrTail), and only by a line in the harness's printed shape.
func providerLogError(dataHome string, offset int64, errTail []string) string {
	f, err := os.Open(filepath.Join(dataHome, filepath.FromSlash(harnessLogFile)))
	if err != nil {
		return printedErrorLine(errTail, true, isProviderError)
	}
	defer f.Close()
	end, err := f.Seek(0, io.SeekEnd)
	if err != nil || end <= offset {
		return printedErrorLine(errTail, true, isProviderError)
	}
	from := max(offset, end-providerLogTailBytes)
	raw := make([]byte, end-from)
	if _, err := f.ReadAt(raw, from); err != nil {
		return printedErrorLine(errTail, true, isProviderError)
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.Contains(line, "level=ERROR") && isProviderError(line) {
			return errorMessage(line)
		}
	}
	return printedErrorLine(errTail, true, isProviderError)
}

// captureErrorLine is the last error line the harness printed on its stderr (the parent's
// tail of it), whatever it says: the harness's own cause of a failure it reports only as
// `UnknownError`. "" when there is none.
func captureErrorLine(errTail []string) string {
	return printedErrorLine(errTail, false, func(string) bool { return true })
}

// isProviderError is whether an error line is the provider's (providerErrorRE).
func isProviderError(line string) bool { return providerErrorRE.MatchString(line) }

// harnessPrintedErrorRE is THE ONE PLACE the shape of an error line the harness prints with
// --print-logs is written, as a launch printed it on its stderr (opencode 1.18.20, `run
// --print-logs --log-level ERROR`, captured 2026-10-01): key=value pairs that open
// `timestamp=<RFC 3339> level=ERROR run=<8 hex> `, the same shape its log file holds. A line
// that only begins `ERROR`, or says level=ERROR somewhere inside it, is not one: a model
// quoting test output is not the harness. Its test rows are TestTheHarnessPrintedErrorShape.
var harnessPrintedErrorRE = regexp.MustCompile(`^timestamp=\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(?:\.\d+)?Z level=ERROR run=[0-9a-f]{8} `)

// printedErrorLine is the first (first true) or last line of the tail in the harness's
// printed error shape that match accepts, from its message on; "" when there is none.
func printedErrorLine(tail []string, first bool, match func(string) bool) string {
	found := ""
	for _, line := range tail {
		line = strings.TrimSpace(line)
		if !harnessPrintedErrorRE.MatchString(line) || !match(line) {
			continue
		}
		found = errorMessage(line)
		if first {
			break
		}
	}
	return found
}

// errorMessage is an error line from its message on, without its timestamp and run id.
func errorMessage(line string) string {
	if i := strings.Index(line, "message="); i >= 0 {
		line = line[i:]
	}
	return strings.TrimSpace(line)
}

// captureTailLines is how many of the harness's last non-empty stderr lines the parent keeps
// (harnessErrTail): the harness prints its error lines last.
const captureTailLines = 20

// harnessErrTail is the parent's own copy of the last captureTailLines non-empty lines the
// harness wrote on its stderr, kept in memory as the bytes arrive (the #1892 class, as the
// shell-denial reader is): `<job>/harness-output.log` is in the card's write directory and
// its cwd, so a card could write a line there that a read after the run would take for the
// harness's. Blank lines are not counted, as swarm's lastLines does not count them.
type harnessErrTail struct {
	mu      sync.Mutex
	lines   []string
	partial []byte
}

func (t *harnessErrTail) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.partial = append(t.partial, p...)
	for {
		i := bytes.IndexByte(t.partial, '\n')
		if i < 0 {
			break
		}
		t.keep(string(t.partial[:i]))
		t.partial = t.partial[i+1:]
	}
	if len(t.partial) > providerLogTailBytes {
		t.partial = t.partial[len(t.partial)-providerLogTailBytes:] // one runaway line stays bounded
	}
	return len(p), nil
}

func (t *harnessErrTail) keep(line string) {
	if strings.TrimSpace(line) == "" {
		return
	}
	t.lines = append(t.lines, line)
	if len(t.lines) > captureTailLines {
		t.lines = t.lines[len(t.lines)-captureTailLines:]
	}
}

// Lines is the tail, oldest first, with a last line that ended without a newline.
func (t *harnessErrTail) Lines() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := append([]string(nil), t.lines...)
	if strings.TrimSpace(string(t.partial)) != "" {
		out = append(out, string(t.partial))
		if len(out) > captureTailLines {
			out = out[1:]
		}
	}
	return out
}

// endedOnATool is whether the run's last message in the session database is not a final
// assistant message (an assistant message whose finish is a stop, a length or another
// end of turn, never `tool-calls`), among the messages created at or after since. false
// when the database or its reader is absent or holds no message of the run: an absence
// says nothing.
func endedOnATool(dataHome string, since time.Time) bool {
	out, ok := sessionQuery(dataHome, `SELECT json_extract(data,'$.role'), COALESCE(json_extract(data,'$.finish'),'') FROM message WHERE time_created >= `+
		strconv.FormatInt(since.UnixMilli(), 10)+` ORDER BY time_created DESC, id DESC LIMIT 1`)
	if !ok {
		return false
	}
	role, finish, ok := strings.Cut(out, "\t")
	if !ok {
		return false // no message of this run
	}
	return !(role == "assistant" && finish != "" && finish != "tool-calls")
}

// sessionProviderError is the cause the session database recorded on the newest message of
// the run (created at or after since) that carries an error: the provider's status and
// words of an API error. ok is false when there is none, or no database or reader.
func sessionProviderError(dataHome string, since time.Time) (swarm.ProviderCause, bool) {
	out, ok := sessionQuery(dataHome, `SELECT json_extract(data,'$.error') FROM message WHERE time_created >= `+
		strconv.FormatInt(since.UnixMilli(), 10)+` AND json_extract(data,'$.error') IS NOT NULL ORDER BY time_created DESC, id DESC LIMIT 1`)
	if !ok {
		return swarm.ProviderCause{}, false
	}
	return swarm.CauseFromSessionError(out)
}

// sessionQuery is one read-only query of the harness's session database in the data home,
// its output without the trailing newline; ok is false when the database or its reader is
// absent or the query failed. List mode, because `-tabs` quotes a value holding a quote,
// and an empty one, as CSV does (sqlite3 3.54): an error's JSON came back unreadable and an
// unfinished message's empty finish read as a finish.
func sessionQuery(dataHome, query string) (string, bool) {
	if !swarm.SQLiteOnPath() {
		return "", false
	}
	var db string
	for _, p := range swarm.OpenCodeStoreLocations(dataHome) {
		if fi, err := os.Stat(p); err == nil && fi.Mode().IsRegular() {
			db = p
			break
		}
	}
	if db == "" {
		return "", false
	}
	ctx, cancel := context.WithTimeout(context.Background(), providerSQLTimeout)
	defer cancel()
	var out bytes.Buffer
	cmd := exec.CommandContext(ctx, swarm.SQLiteBinary, "-readonly", "-list", "-separator", "\t", db, query)
	cmd.Stdout = &out
	cmd.WaitDelay = providerSQLWaitDelay // a killed sqlite3 that left a grandchild on the pipe cannot hold this run
	if err := cmd.Run(); err != nil {
		return "", false
	}
	return strings.TrimRight(out.String(), "\n"), true
}

// providerEnd is the cause when the provider failed a run that ended without a result, ok
// false when it did not. The provider failed it when the harness's log holds a provider
// error line, or the run exited clean ending on a tool result (`ended without a final
// message`, class other); the cause is then the session's own record of the failed message
// when it has one, which keeps the provider's status, else what the log line says.
// offset is the harness log's size when the run began, since is when it began.
func providerEnd(dataHome string, errTail []string, offset int64, since time.Time, rc int) (swarm.ProviderCause, bool) {
	if rc < 0 {
		return swarm.ProviderCause{}, false // killed: the deadline's end, whatever the log says
	}
	var found swarm.ProviderCause
	if line := providerLogError(dataHome, offset, errTail); line != "" {
		found = swarm.CauseFromText(line)
	} else if rc == 0 && endedOnATool(dataHome, since) {
		found = swarm.ProviderCause{Class: swarm.CauseOther, Message: providerEndedNoMessage}
	} else {
		return swarm.ProviderCause{}, false
	}
	if c, ok := sessionProviderError(dataHome, since); ok {
		return c, true
	}
	return found, true
}

// providerLine is the NATIVE line of a provider failure:
//
//	NATIVE PROVIDER-FAIL label=<l> wall=<s>s route=<model> reason=provider: class=<c> status=<n|-> msg=<m>
//
// reason is last and carries the rest of the line.
func providerLine(label string, wall float64, model string, cause swarm.ProviderCause) string {
	return fmt.Sprintf("NATIVE %s label=%s wall=%.2fs route=%s reason=%s",
		providerClass, oneline.Field(label), wall, oneline.Field(model), oneline.Escape(cause.Reason()))
}
