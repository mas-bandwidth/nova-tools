package main

import (
	"fmt"
	"strings"

	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
)

func liftShellWord(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func liftOctalWord(s string) string {
	encoded := "'"
	for i := 0; i < len(s); i++ {
		encoded += fmt.Sprintf(`\0%03o`, s[i])
	}
	return encoded + "'"
}

// boxRemedy renders init/status, whose only argument is the exact box path.
// verb is supplied by the caller, never by command-line input. Reuse the lift
// remedy's quoting and sentinel encoding to keep control-byte paths on one line.
func boxRemedy(verb, box string) string {
	if oneline.Escape(box) == box {
		return "nova-fuse " + verb + " --box " + liftShellWord(box)
	}
	return "(nova_fuse_box=$(printf '%b_' " + liftOctalWord(box) + "); " +
		"nova-fuse " + verb + ` --box "${nova_fuse_box%_}")`
}
