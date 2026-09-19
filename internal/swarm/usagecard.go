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
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
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
// index"), `internal/pulse`'s `parseProgressUsage` and `status`'s own index. A file written
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
		`SUM(json_extract(data, '$.cost')) ` +
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

// ReadCardUsage reads one card's accounting out of its harness store and returns the values,
// a note, the store path that answered, and the reason the row keeps its dashes. The data
// home the native run chose is passed in explicitly, and the reader looks in its standard
// locations in order rather than guessing one path. The window is the run's own timestamps,
// widened five seconds each side, read against the store's `time_created` column in
// milliseconds. The reason is one of the three the NATIVE OK line carries -- no-sqlite3,
// no-rows, or no-store -- or the empty string when the store answered. A note names a
// condition the caller should carry to the person reading it, most importantly sqlite3
// missing from PATH, under which the token columns are dashes and the row still writes
// rather than the run failing on a number nobody can see.
func ReadCardUsage(dataHome string, started, ended time.Time) (ProviderUsage, string, string, string) {
	return ReadCardUsageAfter(dataHome, started, ended, time.Time{})
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
			return ProviderUsage{Values: dashCardTokens()}, "", dbPath, "no-rows"
		}
		if len(rows) == 0 {
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
// stays a dash, never a zero.
func foldCardMessages(rows [][]string) (ProviderUsage, string) {
	values := map[string]string{}
	sums := make([]int64, len(TokenColumns))
	reported := make([]bool, len(TokenColumns))
	var usdSum float64
	usdReported := false
	provider, model := "", ""
	for _, row := range rows {
		if len(row) != 2+len(TokenColumns)+1 {
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
	} else {
		values["usd"] = Dash
	}
	values["provider"] = dashOr(provider)
	values["model"] = dashOr(model)
	return ProviderUsage{Values: values, Observed: true, Turns: len(rows)}, ""
}

// WriteCardUsage writes one card's usage.tsv, header line and one row, atomically. The
// fields follow the same tab- and newline-scrubbing law as the pool's usage file, so the row
// is always one row.
func WriteCardUsage(path string, row UsageRow) error {
	var head, values []string
	for _, c := range CardUsageColumns {
		v := strings.TrimSpace(row[c])
		if v == "" {
			v = Dash
		}
		v = strings.NewReplacer("\t", " ", "\n", " ", "\r", " ").Replace(v)
		head = append(head, c)
		values = append(values, v)
	}
	body := strings.Join(head, "\t") + "\n" + strings.Join(values, "\t") + "\n"
	return writeAtomic(path, []byte(body), 0o644)
}

// AppendCardUsage appends one attempt's usage row to a card's usage.tsv, writing the header
// first when the file is new (issue #900). A native run that retried a launch writes one row
// per attempt -- attempt=1,2,3 for one card -- so the file holds the header and one row per
// launch, and a reader folds them. A field the provider did not report stays a dash, never a
// zero, exactly as in the single-row writer.
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
