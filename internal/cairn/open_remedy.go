package cairn

import (
	"fmt"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// command is a remedy: a POSIX-shell line running `nova-cairn <verb>` with
// flags, given as name, value, name, value (a name with no value after it, a
// boolean, ends the list). Each value is one shell word, quoted only when it
// needs it. A value holding a byte the one-line rendering would escape (a
// control or bidi character in a store path) is decoded from octal inside a
// subshell instead, so the line survives that rendering and still runs: the
// trailing underscore keeps command substitution from stripping a newline,
// and only that sentinel is removed. No variable escapes to the caller.
func command(verb string, flags ...string) string {
	var lets, words []string
	for i := 0; i < len(flags); i += 2 {
		words = append(words, flags[i])
		if i+1 >= len(flags) {
			break
		}
		v := flags[i+1]
		if oneline.Escape(v) == v {
			words = append(words, shellWord(v))
			continue
		}
		name := "nova_cairn_" + strings.ReplaceAll(strings.TrimLeft(flags[i], "-"), "-", "_")
		lets = append(lets, name+"=$(printf '%b_' "+octalWord(v)+"); ")
		words = append(words, `"${`+name+`%_}"`)
	}
	line := "nova-cairn " + verb + " " + strings.Join(words, " ")
	if len(lets) == 0 {
		return line
	}
	return "(" + strings.Join(lets, "") + line + ")"
}

// shellWord is s as one POSIX-shell word: bare when every byte is one a shell
// leaves alone, else single-quoted.
func shellWord(s string) string {
	if s != "" && strings.Trim(s, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_./:@%+,=-") == "" {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func octalWord(s string) string {
	var b strings.Builder
	b.WriteString("'")
	for i := 0; i < len(s); i++ {
		fmt.Fprintf(&b, `\0%03o`, s[i])
	}
	b.WriteString("'")
	return b.String()
}
