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

// The harness's own record, in the job's data home (the harness profile's paths): its
// log, and its session database, whose `message` table holds one row per message of the
// session, the role and the finish of each in its `data` JSON.
const harnessLogFile = "opencode/log/opencode.log"

// ProviderLogTailBytes is the most of the harness's log this run reads: the tail of what
// the run appended, so a log the slot keeps for many runs is never read whole.
const providerLogTailBytes = 64 << 10

// providerLineBytes bounds the error line the finish carries.
const providerLineBytes = 200

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
// harness's log after offset (bounded to its tail), trimmed to the line's own message and
// cut to providerLineBytes; "" when there is none.
func providerLogError(dataHome string, offset int64) string {
	f, err := os.Open(filepath.Join(dataHome, filepath.FromSlash(harnessLogFile)))
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
	for _, line := range strings.Split(string(raw), "\n") {
		if !strings.Contains(line, "level=ERROR") || !providerErrorRE.MatchString(line) {
			continue
		}
		if i := strings.Index(line, "message="); i >= 0 {
			line = line[i:] // what the error says, without its timestamp and run id
		}
		line = strings.TrimSpace(line)
		if len(line) > providerLineBytes {
			line = line[:providerLineBytes]
		}
		return line
	}
	return ""
}

// endedOnATool is whether the run's last message in the session database is not a final
// assistant message (an assistant message whose finish is a stop, a length or another
// end of turn, never `tool-calls`), among the messages created at or after since. false
// when the database or its reader is absent or holds no message of the run: an absence
// says nothing.
func endedOnATool(dataHome string, since time.Time) bool {
	if !swarm.SQLiteOnPath() {
		return false
	}
	var db string
	for _, p := range swarm.OpenCodeStoreLocations(dataHome) {
		if fi, err := os.Stat(p); err == nil && fi.Mode().IsRegular() {
			db = p
			break
		}
	}
	if db == "" {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), providerSQLTimeout)
	defer cancel()
	query := `SELECT json_extract(data,'$.role'), COALESCE(json_extract(data,'$.finish'),'') FROM message WHERE time_created >= ` +
		strconv.FormatInt(since.UnixMilli(), 10) + ` ORDER BY time_created DESC, id DESC LIMIT 1`
	var out bytes.Buffer
	cmd := exec.CommandContext(ctx, swarm.SQLiteBinary, "-readonly", "-tabs", db, query)
	cmd.Stdout = &out
	cmd.WaitDelay = providerSQLWaitDelay // a killed sqlite3 that left a grandchild on the pipe cannot hold this run
	if err := cmd.Run(); err != nil {
		return false
	}
	role, finish, ok := strings.Cut(strings.TrimRight(out.String(), "\n"), "\t")
	if !ok {
		return false // no message of this run
	}
	return !(role == "assistant" && finish != "" && finish != "tool-calls")
}

// providerEnd is why the provider failed a run that ended without a result, "" when it
// did not: the first error line of the harness's log (`provider: <line>`), else the end
// on a tool result when the run exited clean (`provider: ended without a final message`).
// offset is the harness log's size when the run began, since is when it began.
func providerEnd(dataHome string, offset int64, since time.Time, rc int) string {
	if rc < 0 {
		return "" // killed: the deadline's end, whatever the log says
	}
	if line := providerLogError(dataHome, offset); line != "" {
		return "provider: " + line
	}
	if rc == 0 && endedOnATool(dataHome, since) {
		return "provider: " + providerEndedNoMessage
	}
	return ""
}

// providerLine is the NATIVE line of a provider failure:
//
//	NATIVE PROVIDER-FAIL label=<l> wall=<s>s route=<model> reason=<provider: ...>
//
// reason is last and carries the rest of the line.
func providerLine(label string, wall float64, model, reason string) string {
	return fmt.Sprintf("NATIVE %s label=%s wall=%.2fs route=%s reason=%s",
		providerClass, oneline.Field(label), wall, oneline.Field(model), oneline.Escape(reason))
}
