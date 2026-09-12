package swarm

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

// THE USAGE SOURCE IS THE DATABASE THE HARNESS WRITES (SPEC-SWARM rule 12, rule 13).
//
// A SOURCE IS NAMED FOR WHAT IT IS, and on 2026-09-11 this one was not: the enum said
// `opencode` while the reader read a tab-separated file no OpenCode writes, and two jobs
// that burned 61,875 and 85,308 tokens against `--tokens 20000` both reported
// `budget=-/20000`. The name is honest here: `usage: opencode` reads the job's OWN
// `opencode.db`, in the data home this tool exported for it, which is the whole reason
// slots exist -- one job, one database.
//
// THE ONE SUBPROCESS. This repo is standard library only (CONTRIBUTING.md), and the
// standard library cannot read SQLite; a driver would be this repo's first module
// dependency. So the source is read through `sqlite3`, which SPEC-TOKENS picks deliberately
// for the same reason and whose reader this one follows. It is the only program this
// package runs, it is run READ-ONLY, and no value ever reaches a shell: the binary is
// executed with arguments.

// SQLiteBinary is the one program a usage source needs on PATH.
const SQLiteBinary = "sqlite3"

// OpenCodeDB is the database's path inside the job's data home (XDG_DATA_HOME).
const OpenCodeDB = "opencode/opencode.db"

// usageTimeout is how long this tool waits for one query before saying so. It is a property
// of the tool, like a deadline, and never a fact about anybody's data: the supervisor
// samples every few seconds and a query that has not answered in this long has failed.
const usageTimeout = 20 * time.Second

// usageWaitDelay is how long Run waits for the streams after the context is done. Without
// it a killed sqlite3 that left a grandchild holding the pipe would make the timeout above
// a deadline on the process and not on this tool.
const usageWaitDelay = 2 * time.Second

// messagesSQL is the one statement this tool runs. The numbers live in the `data` JSON of
// the message table -- the provider, the model, and the five token types nova-tokens counts
// (SPEC-TOKENS rule 14) -- and a type the provider did not report is NULL, which prints as
// the empty string and is read back as an absence, never as a zero.
const messagesSQL = `SELECT ` +
	`json_extract(data, '$.providerID'), ` +
	`json_extract(data, '$.modelID'), ` +
	`json_extract(data, '$.tokens.input'), ` +
	`json_extract(data, '$.tokens.output'), ` +
	`json_extract(data, '$.tokens.cache.write'), ` +
	`json_extract(data, '$.tokens.cache.read'), ` +
	`json_extract(data, '$.tokens.reasoning') ` +
	`FROM message WHERE json_extract(data, '$.tokens') IS NOT NULL`

// readOpenCodeUsage reads one job's accounting out of its own database.
//
// A DATABASE THAT IS NOT THERE IS NOT AN ERROR: it is the harness having reported nothing
// yet. A database that is there and cannot be read IS one, and rule 13 ends the job
// BUDGET-UNVERIFIABLE on the third such sample, because a numeric budget the tool has
// stopped being able to see is a budget the caller believes is enforced and is not.
func readOpenCodeUsage(dataHome string) (ProviderUsage, error) {
	path := filepath.Join(dataHome, filepath.FromSlash(OpenCodeDB))
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return ProviderUsage{Values: map[string]string{}}, nil
		}
		return ProviderUsage{}, fmt.Errorf("the usage source %s could not be read: %s", path, redactedReason(err))
	}
	if _, err := exec.LookPath(SQLiteBinary); err != nil {
		return ProviderUsage{}, fmt.Errorf("the usage source %s could not be read: %s is not on PATH, and `usage: opencode` reads that database with `%s -readonly`",
			path, SQLiteBinary, SQLiteBinary)
	}
	rows, err := queryOpenCode(path)
	if err != nil {
		return ProviderUsage{}, err
	}
	if len(rows) == 0 {
		// The database exists and holds no message with tokens: the harness has started and
		// reported nothing yet, which is an absence and not a failure.
		return ProviderUsage{Values: map[string]string{}}, nil
	}
	return foldOpenCodeRows(rows), nil
}

// queryOpenCode runs the one statement, read-only, under the timeout. The database is the
// job's own and this tool never writes it: `-readonly` is that promise kept by the program
// that opens it, and `-tabs` is the shape the rows come back in.
func queryOpenCode(path string) ([][]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), usageTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, SQLiteBinary, "-readonly", "-tabs", path, messagesSQL)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	cmd.WaitDelay = usageWaitDelay
	err := cmd.Run()
	if ctx.Err() != nil {
		return nil, fmt.Errorf("the usage source %s could not be read: %s did not answer within %ds",
			path, SQLiteBinary, int(usageTimeout/time.Second))
	}
	if err != nil {
		return nil, fmt.Errorf("the usage source %s could not be read: %s: %v: %s",
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

// foldOpenCodeRows sums the message rows into the columns the usage file carries.
//
// A FIELD THE PROVIDER DID NOT REPORT IS THE LITERAL DASH, NEVER 0 (rule 12): a zero is a
// measurement and a dash is an absence, and nova-tokens reads this file and reads a dash as
// unknown. So a token type no message reported stays a dash, and one that some message
// reported is the sum of the messages that did.
func foldOpenCodeRows(rows [][]string) ProviderUsage {
	values := map[string]string{}
	sums := make([]int64, len(TokenColumns))
	reported := make([]bool, len(TokenColumns))
	provider, model := "", ""
	for _, row := range rows {
		if len(row) < 2+len(TokenColumns) {
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
	}
	for i, c := range TokenColumns {
		if reported[i] {
			values[c] = strconv.FormatInt(sums[i], 10)
			continue
		}
		values[c] = Dash
	}
	values["provider"] = dashOr(provider)
	values["model"] = dashOr(model)
	// The message table holds no repository, and this reader never invents one: the usage
	// row's `repo` is what the caller knew, or a dash.
	values["repo"] = Dash
	return ProviderUsage{Values: values, Observed: true}
}

// oneLine keeps a subprocess's complaint on the one line a RUN BUDGET-UNVERIFIABLE carries.
func oneLine(s string) string {
	return strings.TrimSpace(strings.NewReplacer("\n", " ", "\r", " ", "\t", " ").Replace(s))
}
