package swarm

// SLICE 10: USAGE ROWS PER CARD (lesson 10).
//
// A native run writes one usage.tsv beside its RESULT.md -- one header line and one row --
// so a batch can fold every card into token and dollar totals without re-reading the
// harness. The token numbers come from the harness's own sqlite store, the messages table's
// assistant rows grouped by provider and model, read through the one program this package
// runs (sqlite3, read-only). The row keeps the same dash-versus-zero law as the pool's
// usage file: a field the provider did not report is the literal "-", never 0.

import (
	"bytes"
	"context"
	"fmt"
	"math/big"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/cardcost"
)

// CardUsageColumns are the fourteen columns of one card's usage.tsv, in this order. The
// order is the contract between the native run that writes it and the batch that sums it.
//
// `end` JOINED THEM WITH SPEC-SWARM RULE 13d (issue #1545). The word was being computed by
// every native launch and thrown away: `writeNativeUsage` has set `row["end"]` since issue
// #644's follow-up, and there was no column to put it in, so `end=wall` for a card the wall
// stopped -- and now `end=budget` for one a budget stopped -- reached no reader at all.
// Rule 13d requires it by name: "The launch a budget ended has `end=budget` in its row",
// and demanded test 13d reads it there.
//
// IT IS INSERTED WHERE RULE 12 PUTS IT, after `ended` and before `rc`, so that a person who
// knows the pool's sixteen-column file reads this one without relearning it. THAT IS SAFE
// FOR OLD READS, and it was checked before it was done: every reader of this file in this
// repo maps its columns BY HEADER NAME and says so in terms -- `cmd/nova-tokens`'s
// `readCardFile` ("mapping its columns by the header so the reader never depends on a fixed
// index"), the pulse package's `parseProgressUsage` and `status`'s own index. A file written
// before this change has thirteen columns and no `end`, and every one of those readers
// answers the empty string for it, which is what an absence is.
var CardUsageColumns = []string{
	"job", "attempt", "started", "ended", "end", "rc", "provider", "model",
	"tokens_in", "tokens_out", "cache_write", "cache_read", "reasoning", "usd",
}

// cardMessagesSQL is the one statement this reader runs against the harness's own store: the
// assistant rows of the `message` table, grouped by provider and model. The numbers live in
// the `data` JSON -- the provider, the model, the role, and the five token types plus cost --
// and the row's own `time_created` column is MILLISECONDS since the epoch, so the window is
// two ms bounds, widened five seconds each side. A column no message reported sums to NULL,
// which prints as the empty string and is read back as an absence -- never as a zero.
func cardMessagesSQL(startedMs, endedMs int64) string {
	return `SELECT ` +
		`json_extract(data, '$.providerID'), ` +
		`json_extract(data, '$.modelID'), ` +
		`SUM(json_extract(data, '$.tokens.input')), ` +
		`SUM(json_extract(data, '$.tokens.output')), ` +
		`SUM(json_extract(data, '$.tokens.cache.write')), ` +
		`SUM(json_extract(data, '$.tokens.cache.read')), ` +
		`SUM(json_extract(data, '$.tokens.reasoning')), ` +
		`SUM(json_extract(data, '$.cost')), ` +
		// the card's requests (one assistant message is one model call) and its largest
		// prompt, what a long-context price is decided on (pkg/cardcost, Predict)
		`COUNT(*), ` +
		`MAX(COALESCE(json_extract(data, '$.tokens.input'), 0) + COALESCE(json_extract(data, '$.tokens.cache.read'), 0) + COALESCE(json_extract(data, '$.tokens.cache.write'), 0)) ` +
		`FROM message WHERE json_extract(data, '$.role') = 'assistant' ` +
		`AND time_created >= ` + strconv.FormatInt(startedMs, 10) +
		` AND time_created <= ` + strconv.FormatInt(endedMs, 10) +
		` GROUP BY json_extract(data, '$.providerID'), json_extract(data, '$.modelID')`
}

// cardStoreLocations are the store paths OpenCode may keep inside one data home, in the
// order this reader tries them: the run's own data directory first -- <dataHome>/opencode/
// opencode.db, the XDG_DATA_HOME location this tool exports -- then the HOME/.local/share
// location a real OpenCode honours when it reads HOME instead of XDG_DATA_HOME. The native
// run points both HOME and XDG_DATA_HOME at the same data directory, so both live under the
// path native.go passes here.
func cardStoreLocations(dataHome string) []string {
	return OpenCodeStoreLocations(dataHome)
}

// ReadCardUsageAfter is the same read with a FLOOR under the window, and the floor is what
// keeps a retried card's rows DISJOINT (SPEC-SWARM rule 13d, issue #1545).
//
// THE DEFECT IT CLOSES, measured: the window is widened five seconds each side, because the
// launch's own clock and the store's need not agree to the millisecond. On a retried card
// the SECOND launch begins within those five seconds of the first one's end -- the launch
// grace's retry is seconds, and a test pins it shorter still -- so the second launch's
// window reached back over the first launch's rows and counted them again. Rule 13d's own
// worked example is exactly this: two launches finally reported at 40 and 70 "print
// `budget=110/100`, and their rows hold 40 and 70, never 40 and 110". Before this floor the
// rows held 40 and 110 and added to 150, which is what a downstream `cost` would have
// charged.
//
// `notBefore` is the EARLIER launch's end. The zero time is no floor at all, which is what
// a first launch has and what every caller outside the retry loop wants.
func ReadCardUsageAfter(dataHome string, started, ended, notBefore time.Time) (ProviderUsage, string, string, string) {
	windowStart := started.Add(-5 * time.Second)
	if !notBefore.IsZero() && windowStart.Before(notBefore) {
		windowStart = notBefore
	}
	startedMs := windowStart.UnixMilli()
	endedMs := ended.Add(5 * time.Second).UnixMilli()
	locations := cardStoreLocations(dataHome)
	for _, dbPath := range locations {
		st, err := os.Stat(dbPath)
		if err != nil || st.IsDir() {
			continue
		}
		if _, err := exec.LookPath(SQLiteBinary); err != nil {
			note := fmt.Sprintf("usage.tsv token columns are %q: %s is not on PATH, and the store is read with %q read-only",
				Dash, SQLiteBinary, SQLiteBinary)
			return ProviderUsage{Values: dashCardTokens()}, note, dbPath, "no-sqlite3"
		}
		rows, err := queryCardMessages(dbPath, startedMs, endedMs)
		if err != nil {
			// A QUERY THAT FAILED IS NOT AN EMPTY TABLE. These two shared the `no-rows`
			// token and they are not the same fact: this one is a database that is locked,
			// corrupt, or did not answer inside the timeout -- a READER THAT STOPPED, and
			// a caller that acts on the number is acting on a number nobody could see. The
			// one below is a read that worked and found nothing. A caller that must tell
			// them apart could not, and one that reported both as a fault cried wolf on
			// every card killed before its first answer.
			return ProviderUsage{Values: dashCardTokens()}, "", dbPath, "query-failed"
		}
		if len(rows) == 0 {
			// The database exists, the query ran, and the window holds no message with
			// tokens: the harness started and reported nothing yet. An ABSENCE.
			return ProviderUsage{Values: dashCardTokens()}, "", dbPath, "no-rows"
		}
		usage, _ := foldCardMessages(rows)
		if dbPath == locations[0] {
			return usage, "", dbPath, ""
		}
		note := fmt.Sprintf("usage.tsv read the store at %s (the primary %s was absent)", dbPath, locations[0])
		return usage, note, dbPath, ""
	}
	return ProviderUsage{Values: dashCardTokens()}, fmt.Sprintf("no harness store: looked at %s and %s", locations[0], locations[1]), "", "no-store"
}

// dashCardTokens is the row of a store that reported nothing: every numeric column a dash.
func dashCardTokens() map[string]string {
	values := map[string]string{"provider": Dash, "model": Dash, "usd": Dash}
	for _, c := range TokenColumns {
		values[c] = Dash
	}
	return values
}

// queryCardMessages runs the one statement, read-only, under the same timeout every usage
// read carries (rule 13). The statement's window is the caller's two ms bounds.
func queryCardMessages(path string, startedMs, endedMs int64) ([][]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), usageTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, SQLiteBinary, "-readonly", "-tabs", path, cardMessagesSQL(startedMs, endedMs))
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	cmd.WaitDelay = usageWaitDelay
	err := cmd.Run()
	if ctx.Err() != nil {
		return nil, fmt.Errorf("the store %s could not be read: %s did not answer within %ds",
			path, SQLiteBinary, int(usageTimeout/time.Second))
	}
	if err != nil {
		return nil, fmt.Errorf("the store %s could not be read: %s: %v: %s",
			path, SQLiteBinary, err, oneLine(errb.String()))
	}
	var rows [][]string
	for _, line := range strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		rows = append(rows, strings.Split(line, "\t"))
	}
	return rows, nil
}

// foldCardMessages sums the grouped assistant rows into the columns the usage row carries.
// A field some message reported is the sum of the messages that did; one no message reported
// stays a dash, never a zero. Beside the row's columns it keeps what a card's cost record
// reads (pkg/cardcost): the harness's cost as the decimal the store printed for its
// float sum of opencode's per-message float costs, uncut ("cost", where the row's usd is
// cut to four places): the harness's own computation, not an invoice; the requests and
// the largest prompt; none of the three reaches the usage file.
func foldCardMessages(rows [][]string) (ProviderUsage, string) {
	values := map[string]string{}
	sums := make([]int64, len(TokenColumns))
	reported := make([]bool, len(TokenColumns))
	var usdSum float64
	usdReported := false
	cost := new(big.Rat)
	var requests, maxPrompt int64
	provider, model := "", ""
	for _, row := range rows {
		if len(row) != 2+len(TokenColumns)+3 {
			continue
		}
		if v := strings.TrimSpace(row[0]); v != "" {
			provider = v
		}
		if v := strings.TrimSpace(row[1]); v != "" {
			model = v
		}
		for i := range TokenColumns {
			cell := strings.TrimSpace(row[2+i])
			if cell == "" || cell == Dash {
				continue
			}
			n, err := strconv.ParseInt(cell, 10, 64)
			if err != nil {
				continue
			}
			sums[i] += n
			reported[i] = true
		}
		if cell := strings.TrimSpace(row[2+len(TokenColumns)]); cell != "" && cell != Dash {
			if f, err := strconv.ParseFloat(cell, 64); err == nil {
				usdSum += f
				usdReported = true
			}
			if r, ok := new(big.Rat).SetString(cell); ok && r.Sign() >= 0 {
				cost.Add(cost, r)
			}
		}
		if n, err := strconv.ParseInt(strings.TrimSpace(row[3+len(TokenColumns)]), 10, 64); err == nil {
			requests += n
		}
		if n, err := strconv.ParseInt(strings.TrimSpace(row[4+len(TokenColumns)]), 10, 64); err == nil {
			maxPrompt = max(maxPrompt, n)
		}
	}
	for i, c := range TokenColumns {
		if reported[i] {
			values[c] = strconv.FormatInt(sums[i], 10)
			continue
		}
		values[c] = Dash
	}
	if usdReported {
		values["usd"] = strconv.FormatFloat(usdSum, 'f', 4, 64)
		values["cost"] = cardcost.Text(cost)
	} else {
		values["usd"] = Dash
	}
	values["requests"] = strconv.FormatInt(requests, 10)
	values["max_prompt"] = strconv.FormatInt(maxPrompt, 10)
	values["provider"] = dashOr(provider)
	values["model"] = dashOr(model)
	return ProviderUsage{Values: values, Observed: true, Turns: len(rows)}, ""
}

// AppendCardUsage appends one attempt's usage row to a card's usage.tsv, writing the header
// first when the file is new (issue #900). A native run that retried a launch writes one row
// per attempt -- attempt=1,2,3 for one card -- so the file holds the header and one row per
// launch, and a reader folds them. A field the provider did not report stays a dash, never a
// zero, and a tab or a newline in a value is scrubbed so the row is always one row.
func AppendCardUsage(path string, row UsageRow) error {
	_, statErr := os.Stat(path)
	var b strings.Builder
	if statErr != nil {
		b.WriteString(strings.Join(CardUsageColumns, "\t"))
		b.WriteByte('\n')
	}
	values := make([]string, 0, len(CardUsageColumns))
	for _, c := range CardUsageColumns {
		v := strings.TrimSpace(row[c])
		if v == "" {
			v = Dash
		}
		v = strings.NewReplacer("\t", " ", "\n", " ", "\r", " ").Replace(v)
		values = append(values, v)
	}
	b.WriteString(strings.Join(values, "\t"))
	b.WriteByte('\n')
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(b.String()); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
