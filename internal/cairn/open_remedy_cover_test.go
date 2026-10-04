// Unit coverage for the remedy-command helpers open_remedy.go showed at zero
// in the per-function coverage table: octalWord, and with it the subshell
// branch of command the unit tier never reached, plus the quoting refusals of
// shellWord and command. Everything runs in-process on plain strings: no
// sleeps, no real time, no network, no subprocess, no Redis or Postgres.
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
			name: "safe values are bare words on one plain line, no subshell",
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
			want: "nova-cairn open --store 'my store'\\''s $HOME `literal`' --session 's'\\''$HOME' --publish never",
		},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, row.want, row.cmd(), "command must render the flags as one pasteable POSIX-shell line")
		})
	}
}

func TestOpenRemedyCoverShellWord(t *testing.T) {
	t.Parallel()

	rows := []struct {
		name, in, want string
	}{
		{name: "a word of bytes a shell leaves alone stands bare", in: "abc-1/x:y@%,=+", want: "abc-1/x:y@%,=+"},
		{name: "a space forces single quotes", in: "two words", want: "'two words'"},
		{name: "an embedded single quote closes and escapes", in: "s'$HOME", want: `'s'\''$HOME'`},
		{name: "refusal: an empty value quotes to the empty word", in: "", want: "''"},
		{name: "refusal: a control byte is quoted, never left raw on the line", in: "\t", want: "'\t'"},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, row.want, shellWord(row.in), "shellWord must return the word bare or single-quoted")
		})
	}
}

func TestOpenRemedyCoverOctalWord(t *testing.T) {
	t.Parallel()

	rows := []struct {
		name, in, want string
	}{
		{name: "a tab becomes one octal triple under a leading zero", in: "\t", want: `'\0011'`},
		{name: "a multi-byte rune is escaped byte by byte", in: "‮", want: `'\0342\0200\0256'`},
		{name: "refusal: an empty value is the empty quoted word, one pair of quotes", in: "", want: "''"},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, row.want, octalWord(row.in), "octalWord must quote every byte as \\0NNN inside single quotes")
		})
	}
}

func TestOpenRemedyCoverCommandOctalSubshellPath(t *testing.T) {
	t.Parallel()

	rows := []struct {
		name string
		cmd  func() string
		want string
	}{
		{
			name: "a value with control bytes is decoded from octal in a subshell with a trailing-underscore sentinel",
			cmd: func() string {
				return command("open", "--store", "store\t\n", "--session", "s‮end", "--publish", "never")
			},
			want: "(nova_cairn_store=$(printf '%b_' '\\0163\\0164\\0157\\0162\\0145\\0011\\0012'); " +
				"nova_cairn_session=$(printf '%b_' '\\0163\\0342\\0200\\0256\\0145\\0156\\0144'); " +
				`nova-cairn open --store "${nova_cairn_store%_}" --session "${nova_cairn_session%_}" --publish never)`,
		},
		{
			name: "a value needing quoting stands on the line while an unsafe sibling goes through the subshell",
			cmd:  func() string { return command("index", "--store", "two words", "--session", "s\t") },
			want: "(nova_cairn_session=$(printf '%b_' '\\0163\\0011'); " +
				`nova-cairn index --store 'two words' --session "${nova_cairn_session%_}")`,
		},
		{
			name: "refusal: a safe value never takes the subshell route",
			cmd:  func() string { return command("index", "--store", "st", "--session", "s") },
			want: "nova-cairn index --store st --session s",
		},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, row.want, row.cmd(), "command must wrap the subshell only around values the one-line rendering would escape")
		})
	}
}
