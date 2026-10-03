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
//  1. the session's record, the harness's printed output or its log carries a provider
//     error (a stream error, server_error, a rate limit, an account out of credit or
//     quota, an HTTP 402, 429 or 5xx) written by this run, or
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

// providerErrorRE is a provider error on an ERROR line of the harness's log or its printed
// output: a stream error, a server_error, a rate limit, an overload, an account out of
// credit or quota (`Insufficient credits`, `Insufficient account funds`, `out of credit`,
// a quota, a payment required), or an HTTP 402, 429 or 5xx status (nova-tools#5199: a 402
// at launch was read as a card that chose to publish nothing, 247 times for one card).
var providerErrorRE = regexp.MustCompile(`(?i)message="?stream error|server_error|rate.?limit|overloaded|insufficient|out of credit|quota|payment required|\b(?:http|status|statuscode)\W{0,3}(?:402|429|5\d\d)\b`)

// providerLogError is the first provider error line in what the run appended to the
// harness's log after offset (bounded to its tail), trimmed to the line's own message;
// "" when there is none. The cause read from it (swarm.CauseFromText) is what is bounded.
func providerLogError(dataHome string, offset int64) string {
	raw := tailSince(filepath.Join(dataHome, filepath.FromSlash(harnessLogFile)), offset)
	for _, line := range strings.Split(string(raw), "\n") {
		if !strings.Contains(line, "level=ERROR") || !providerErrorRE.MatchString(line) {
			continue
		}
		if i := strings.Index(line, "message="); i >= 0 {
			line = line[i:] // what the error says, without its timestamp and run id
		}
		return strings.TrimSpace(line)
	}
	return ""
}

// tailSince is what was appended to the file at path after offset, at most its last
// providerLogTailBytes; nil when there is nothing or the file cannot be read.
func tailSince(path string, offset int64) []byte {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	end, err := f.Seek(0, io.SeekEnd)
	if err != nil || end <= offset {
		return nil
	}
	from := max(offset, end-providerLogTailBytes)
	raw := make([]byte, end-from)
	if _, err := f.ReadAt(raw, from); err != nil {
		return nil
	}
	return raw
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
// false when it did not. The provider failed it when the harness's own record names a
// provider error, read in this order: the session's record of the failed message (it keeps
// the provider's status: a 402 is `statusCode 402` there), the harness's printed output
// (capture: what the run appended to `<job>/harness-output.log`, where OPENCODE_PRINT_LOGS
// puts its ERROR lines), then its log in the data home; else when the run exited clean
// ending on a tool result (`ended without a final message`, class other).
//
// THE SESSION AND THE PRINTED OUTPUT COME FIRST (nova-tools#5199). A non-retryable 402 at
// launch makes the harness exit 1 within two seconds, before its data-home log holds a
// line; the 402 is in the printed output and in the session alone. Reading the log first
// and giving up on an rc that was not 0 reported `no-result`, and the card was dealt again
// 247 times overnight on a provider with no credit.
//
// offset is the harness log's size when the run began, since is when it began.
func providerEnd(dataHome string, offset int64, since time.Time, rc int, capture []byte) (swarm.ProviderCause, bool) {
	if rc < 0 {
		return swarm.ProviderCause{}, false // killed: the deadline's end, whatever the log says
	}
	if c, ok := sessionProviderError(dataHome, since); ok {
		return c, true
	}
	if line := printedHarnessError(capture); line != "" {
		return swarm.CauseFromText(line), true
	}
	if line := providerLogError(dataHome, offset); line != "" {
		return swarm.CauseFromText(line), true
	}
	if rc == 0 && endedOnATool(dataHome, since) {
		return swarm.ProviderCause{Class: swarm.CauseOther, Message: providerEndedNoMessage}, true
	}
	return swarm.ProviderCause{}, false
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

// printedHarnessError is the harness's own printed ERROR line about why the launch failed,
// from its capture (OPENCODE_PRINT_LOGS): the catalog refusing the model first, else a
// provider error line, trimmed to the line's own message; "" when there is none.
func printedHarnessError(capture []byte) string {
	var provider string
	for _, line := range strings.Split(string(capture), "\n") {
		if !strings.Contains(line, "level=ERROR") {
			continue
		}
		if i := strings.Index(line, "message="); i >= 0 {
			line = line[i:]
		}
		line = strings.TrimSpace(line)
		if strings.Contains(line, "ProviderModelNotFoundError") && strings.Contains(line, "error=") {
			return line
		}
		if provider == "" && providerErrorRE.MatchString(line) {
			provider = line
		}
	}
	return provider
}
