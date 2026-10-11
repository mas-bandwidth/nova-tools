package cairn

import (
	"strings"

	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
)

// command is a remedy: a POSIX-shell line running `nova-cairn <verb>` with
// flags, given as name, value, name, value (a name with no value after it, a
// boolean, ends the list). Each value is one shell word (oneline.ShellWord).
// A value the one-line rendering would escape cannot stand in a remedy the
// reader can paste, so the store path it came from is refused whole: the line
// says the path cannot be printed as one line and to rename it.
func command(verb string, flags ...string) string {
	words := []string{"nova-cairn", verb}
	for i := 0; i < len(flags); i += 2 {
		words = append(words, flags[i])
		if i+1 >= len(flags) {
			break
		}
		v := flags[i+1]
		if oneline.Escape(v) != v {
			return "this path cannot be printed as one line; rename it"
		}
		words = append(words, oneline.ShellWord(v))
	}
	return strings.Join(words, " ")
}
