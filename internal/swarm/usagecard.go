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
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// CardUsageColumns are the thirteen columns of one card's usage.tsv, in this order. The
// order is the contract between the native run that writes it and the batch that sums it.
var CardUsageColumns = []string{
	"job", "attempt", "started", "ended", "rc", "provider", "model",
	"tokens_in", "tokens_out", "cache_write", "cache_read", "reasoning", "usd",
}

// cardMessagesSQL is the one statement this reader runs against the harness's own store: the
// assistant rows of the messages table, grouped by provider and model. A column no message
// reported sums to NULL, which prints as the empty string and is read back as an absence --
// never as a zero.
const cardMessagesSQL = `SELECT provider, model, ` +
	`SUM(tokens_in), SUM(tokens_out), SUM(cache_write), SUM(cache_read), SUM(reasoning), SUM(usd) ` +
	`FROM messages WHERE role='assistant' GROUP BY provider, model`

// cardStoreLocations are the store paths OpenCode may keep inside one data home, in the
// order this reader tries them: the run's own data directory first -- <dataHome>/opencode/
// opencode.db, the XDG_DATA_HOME location this tool exports -- then the HOME/.local/share
// location a real OpenCode honours when it reads HOME instead of XDG_DATA_HOME. The native
// run points both HOME and XDG_DATA_HOME at the same data directory, so both live under the
// path native.go passes here.
func cardStoreLocations(dataHome string) []string {
	return []string{
		filepath.Join(dataHome, "opencode", "opencode.db"),
		filepath.Join(dataHome, ".local", "share", "opencode", "opencode.db"),
	}
}

// ReadCardUsage reads one card's accounting out of its harness store and returns the values,
// a note, and the store path that answered. The data home the native run chose is passed in
// explicitly, and the reader looks in its standard locations in order rather than guessing
// one path. A note names a condition the caller should carry to the person reading it --
// most importantly, sqlite3 missing from PATH, under which the token columns are dashes and
// the row still writes rather than the run failing on a number nobody can see. When no store
// exists at either location the returned path is empty and the note names what was looked for.
func ReadCardUsage(dataHome string) (ProviderUsage, string, string) {
	locations := cardStoreLocations(dataHome)
	for _, dbPath := range locations {
		st, err := os.Stat(dbPath)
		if err != nil || st.IsDir() {
			continue
		}
		if _, err := exec.LookPath(SQLiteBinary); err != nil {
			note := fmt.Sprintf("usage.tsv token columns are %q: %s is not on PATH, and the store is read with %q read-only",
				Dash, SQLiteBinary, SQLiteBinary)
			return ProviderUsage{Values: dashCardTokens()}, note, dbPath
		}
		rows, err := queryCardMessages(dbPath)
		if err != nil {
			return ProviderUsage{Values: dashCardTokens()}, "", dbPath
		}
		if len(rows) == 0 {
			return ProviderUsage{Values: dashCardTokens()}, "", dbPath
		}
		usage, _ := foldCardMessages(rows)
		if dbPath == locations[0] {
			return usage, "", dbPath
		}
		note := fmt.Sprintf("usage.tsv read the store at %s (the primary %s was absent)", dbPath, locations[0])
		return usage, note, dbPath
	}
	return ProviderUsage{Values: dashCardTokens()}, fmt.Sprintf("no harness store: looked at %s and %s", locations[0], locations[1]), ""
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
// read carries (rule 13).
func queryCardMessages(path string) ([][]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), usageTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, SQLiteBinary, "-readonly", "-tabs", path, cardMessagesSQL)
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
	return ProviderUsage{Values: values, Observed: true}, ""
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
