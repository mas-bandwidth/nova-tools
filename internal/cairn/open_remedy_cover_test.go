// Unit coverage for the remedy-command helper open_remedy.go: command's plain
// line, its quoting of values through oneline.ShellWord, and its refusal of a
// value the one-line rendering cannot carry. Everything runs in-process on
// plain strings: no sleeps, no real time, no network, no subprocess, no Redis
// or Postgres.
package cairn

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestOpenRemedyCoverCommandPlainPath(t *testing.T) {
	t.Parallel()

	rows := []struct {
		name string
		cmd  func() string
		want string
	}{
		{
			name: "safe values are bare words on one plain line",
			cmd:  func() string { return command("open", "--store", "store", "--session", "s", "--publish", "never") },
			want: "nova-cairn open --store store --session s --publish never",
		},
		{
			name: "a trailing name with no value is a boolean flag and ends the list",
			cmd: func() string {
				return command("receipt", "--store", "st", "--session", "s", "--entry", "e-1", "--text")
			},
			want: "nova-cairn receipt --store st --session s --entry e-1 --text",
		},
		{
			name: "refusal: a value with spaces or shell metacharacters is single-quoted, never interpolated",
			cmd: func() string {
				return command("open", "--store", "my store's $HOME `literal`", "--session", "s'$HOME", "--publish", "never")
			},
			want: "nova-cairn open --store 'my store'\"'\"'s $HOME `literal`' --session 's'\"'\"'$HOME' --publish never",
		},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, row.want, row.cmd(), "command must render the flags as one pasteable POSIX-shell line")
		})
	}
}

func TestOpenRemedyCoverCommandRefusesAValueItCannotPrint(t *testing.T) {
	t.Parallel()

	rows := []struct {
		name string
		cmd  func() string
		want string
	}{
		{
			name: "a store path with a control byte is refused whole, never decoded in a subshell",
			cmd:  func() string { return command("open", "--store", "store\t\n", "--session", "s", "--publish", "never") },
			want: "this path cannot be printed as one line; rename it",
		},
		{
			name: "a session id a terminal would reorder is refused whole",
			cmd:  func() string { return command("index", "--store", "st", "--session", "s\u202eend") },
			want: "this path cannot be printed as one line; rename it",
		},
		{
			name: "a safe value stands on the line, never the refusal",
			cmd:  func() string { return command("index", "--store", "st", "--session", "s") },
			want: "nova-cairn index --store st --session s",
		},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, row.want, row.cmd(), "command must refuse a value the one-line rendering cannot carry")
		})
	}
}
