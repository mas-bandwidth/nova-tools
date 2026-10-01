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
// The harness prints its error lines into the run's capture too (`--print-logs --log-level
// ERROR` in the providers table), so a log that holds none is followed by the capture.
func providerLogError(dataHome string, offset int64, capture string) string {
	if line := errorLineIn(filepath.Join(dataHome, filepath.FromSlash(harnessLogFile)), offset, 0, true, isProviderError); line != "" {
		return line
	}
	return errorLineIn(capture, 0, captureTailLines, true, isProviderError)
}

// captureErrorLine is the last error line the harness printed into the run's capture
// (bounded to its tail), whatever it says: the harness's own cause of a failure it reports
// only as `UnknownError`. "" when there is none.
func captureErrorLine(capture string) string {
	return errorLineIn(capture, 0, captureTailLines, false, func(string) bool { return true })
}

// captureTailLines is how far back from its end the capture is read for the harness's error
// lines: the harness prints them last, and the card's own output before them (a log it
// printed that says level=ERROR) is not the harness's.
const captureTailLines = 20

// isProviderError is whether an error line is the provider's (providerErrorRE).
func isProviderError(line string) bool { return providerErrorRE.MatchString(line) }

// errorLineIn is the first (first true) or last error line of the harness in what path holds
// after offset, bounded to its last providerLogTailBytes and, when tail is above 0, to its
// last tail lines, that match accepts, from its message on; "" when there is none. An error
// line is the log's `level=ERROR`, or a printed line that begins with `ERROR`.
func errorLineIn(path string, offset int64, tail int, first bool, match func(string) bool) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	end, err := f.Seek(0, io.SeekEnd)
	if err != nil || end <= offset {
		return ""
	}
	from := max(offset, end-providerLogTailBytes)
	raw := make([]byte, end-from)
	if _, err := f.ReadAt(raw, from); err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	if tail > 0 && len(lines) > tail {
		lines = lines[len(lines)-tail:]
	}
	found := ""
	for _, line := range lines {
		errLine := strings.Contains(line, "level=ERROR") || strings.HasPrefix(strings.TrimSpace(line), "ERROR")
		if !errLine || !match(line) {
			continue
		}
		if i := strings.Index(line, "message="); i >= 0 {
			line = line[i:] // what the error says, without its timestamp and run id
		}
		found = strings.TrimSpace(line)
		if first {
			break
		}
	}
	return found
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
func providerEnd(dataHome, capture string, offset int64, since time.Time, rc int) (swarm.ProviderCause, bool) {
	if rc < 0 {
		return swarm.ProviderCause{}, false // killed: the deadline's end, whatever the log says
	}
	var found swarm.ProviderCause
	if line := providerLogError(dataHome, offset, capture); line != "" {
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
