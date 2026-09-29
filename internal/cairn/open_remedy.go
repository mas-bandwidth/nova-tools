package cairn

import (
	"fmt"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// openRemedy is a POSIX-shell command over the caller's exact store and
// session. Its bytes must also survive the CLI's one-line error rendering.
func openRemedy(store, session, publish string) string {
	return shellLine("nova-cairn open --store %s --session %s --publish "+publish, shellWord{"store", store}, shellWord{"session", session})
}

// shellWord is one caller-supplied word of a printed command, and the name of the
// variable that carries it when it has bytes a one-line message cannot hold.
type shellWord struct{ name, val string }

// shellLine is a POSIX-shell command: format with one %s for each word, in
// order. A word that is plain is single-quoted. Otherwise control bytes are
// decoded inside a subshell; the extra underscore keeps shell command
// substitution from stripping trailing newlines, and only that sentinel is
// removed when supplying the argument. No variables escape to the caller.
func shellLine(format string, words ...shellWord) string {
	plain := true
	for _, w := range words {
		plain = plain && oneline.Escape(w.val) == w.val
	}
	args := make([]any, len(words))
	if plain {
		for i, w := range words {
			args[i] = openShellWord(w.val)
		}
		return fmt.Sprintf(format, args...)
	}
	var pre strings.Builder
	for i, w := range words {
		fmt.Fprintf(&pre, "nova_cairn_%s=$(printf '%%b_' %s); ", w.name, openOctalWord(w.val))
		args[i] = fmt.Sprintf(`"${nova_cairn_%s%%_}"`, w.name)
	}
	return "(" + pre.String() + fmt.Sprintf(format, args...) + ")"
}

func openShellWord(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func openOctalWord(s string) string {
	encoded := "'"
	for i := 0; i < len(s); i++ {
		encoded += fmt.Sprintf(`\0%03o`, s[i])
	}
	return encoded + "'"
}
