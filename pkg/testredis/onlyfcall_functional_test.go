//go:build functional

package testredis

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The functional tier of OnlyFCALL: the list it judges by, held to the command
// table of a real redis-server.

// The fields of one command's entry in the reply to COMMAND that the test
// reads (https://redis.io/docs/latest/commands/command/).
const (
	fieldName        = 0
	fieldFlags       = 2
	fieldSubcommands = 9
	writeFlag        = "write"
)

// commandTable is what COMMAND says of every command the server has, by name in
// capitals (a subcommand is "XGROUP CREATE"): true when its flags carry write.
type commandTable map[string]bool

// readCommandTable asks the server for its whole command table over RESP2,
// where the flags of a command are a list of words.
func readCommandTable(t *testing.T, addr string) commandTable {
	t.Helper()
	client := redis.NewClient(&redis.Options{Addr: addr, Protocol: 2})
	defer client.Close()
	rows, err := client.Do(context.Background(), "COMMAND").Slice()
	if err != nil {
		require.NoError(t, err, "COMMAND: %v", err)
	}
	table := commandTable{}
	for _, row := range rows {
		if err := table.add(row); err != nil {
			require.NoError(t, err, "COMMAND: %v", err)
		}
	}
	return table
}

// add reads one command's entry, and the entries of its subcommands.
func (table commandTable) add(row any) error {
	fields, ok := row.([]any)
	if !ok || len(fields) <= fieldSubcommands {
		return fmt.Errorf("an entry is %v; want a list of at least %d fields", row, fieldSubcommands+1)
	}
	name, ok := fields[fieldName].(string)
	if !ok {
		return fmt.Errorf("an entry names itself %v; want text", fields[fieldName])
	}
	flags, ok := fields[fieldFlags].([]any)
	if !ok {
		return fmt.Errorf("%s has the flags %v; want a list", name, fields[fieldFlags])
	}
	write := false
	for _, flag := range flags {
		if text, _ := flag.(string); strings.EqualFold(text, writeFlag) {
			write = true
		}
	}
	// A subcommand is named container|subcommand.
	table[strings.ToUpper(strings.ReplaceAll(name, "|", " "))] = write
	subs, ok := fields[fieldSubcommands].([]any)
	if !ok {
		return fmt.Errorf("%s has the subcommands %v; want a list", name, fields[fieldSubcommands])
	}
	for _, sub := range subs {
		if err := table.add(sub); err != nil {
			return err
		}
	}
	return nil
}

// The fixed list is the write flag of the server, read back: a write the server
// has and the list lacks is a hole in OnlyFCALL, and a name the list holds that
// the server does not flag is a command that fails a test without writing.
func TestWriteListIsTheWriteFlagOfTheServer(t *testing.T) {
	t.Parallel()

	table := readCommandTable(t, Start(t))

	// The table is not empty, and it says what everyone knows: SET writes and
	// GET does not.
	if write, present := table["SET"]; !present || !write {
		require.Failf(t, "", "the server's table has SET as present %v and write %v; want a write", present, write)
	}
	if write, present := table["GET"]; !present || write {
		require.Failf(t, "", "the server's table has GET as present %v and write %v; want a command that is not a write", present, write)
	}

	var holes, wrongFlagged, wrongScripted, wrongBundled []string
	for name, write := range table {
		if write && !writes[name] {
			holes = append(holes, name)
		}
	}
	for _, g := range writeGroups {
		for _, name := range g.commands {
			write, present := table[name]
			switch g.origin {
			case flagged:
				if !write {
					wrongFlagged = append(wrongFlagged, fmt.Sprintf("%s (%s; present %v)", name, g.name, present))
				}
			case scripted:
				if !present || write {
					wrongScripted = append(wrongScripted, fmt.Sprintf("%s (%s; present %v, write %v)", name, g.name, present, write))
				}
			case bundled:
				if present && !write {
					wrongBundled = append(wrongBundled, fmt.Sprintf("%s (%s)", name, g.name))
				}
			}
		}
	}
	for _, problem := range []struct {
		names []string
		what  string
	}{
		{holes, "the server flags these as writes and no group of the list names them: add each to the group of its type"},
		{wrongFlagged, "a group of flagged commands names these and the server does not flag them: they fail a test without writing; remove them, or move them to the scripted group if they run a script"},
		{wrongScripted, "the scripted group names these and the server lacks them or flags them: a flagged command belongs to the group of its type"},
		{wrongBundled, "the server has these module commands and does not flag them as writes: remove them from the bundled groups"},
	} {
		if len(problem.names) > 0 {
			slices.Sort(problem.names)
			assert.Failf(t, "", "%s:\n  %s", problem.what, strings.Join(problem.names, "\n  "))
		}
	}
}
