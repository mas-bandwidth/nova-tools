package cairn

import (
	"fmt"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// openRemedy is a POSIX-shell command over the caller's exact store and
// session. Its bytes must also survive the CLI's one-line error rendering.
func openRemedy(store, session, publish string) string {
	if oneline.Escape(store) == store && oneline.Escape(session) == session {
		return "nova-cairn open --store " + openShellWord(store) + " --session " + openShellWord(session) + " --publish " + publish
	}
	// Decode control bytes inside a subshell. The extra underscore keeps shell
	// command substitution from stripping trailing newlines; remove only that
	// sentinel when supplying the argument. No variables escape to the caller.
	return "(nova_cairn_store=$(printf '%b_' " + openOctalWord(store) + "); " +
		"nova_cairn_session=$(printf '%b_' " + openOctalWord(session) + "); " +
		`nova-cairn open --store "${nova_cairn_store%_}" --session "${nova_cairn_session%_}" --publish ` + publish + ")"
}

func openShellWord(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func openOctalWord(s string) string {
	encoded := "'"
	for i := 0; i < len(s); i++ {
		encoded += fmt.Sprintf(`\0%03o`, s[i])
	}
	return encoded + "'"
}
