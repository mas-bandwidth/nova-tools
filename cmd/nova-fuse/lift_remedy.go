package main

import (
	"fmt"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// liftRemedy is a POSIX-shell command over the exact box and normalized surface.
// A separator keeps a leading dash in the surface from becoming a flag.
func liftRemedy(box, surface string) string {
	if oneline.Escape(box) == box && oneline.Escape(surface) == surface {
		return "nova-fuse lift quarantine --box " + liftShellWord(box) + " -- " + liftShellWord(surface)
	}
	// Literal control characters would break the one-line receipt. Decode octal
	// bytes instead, with a sentinel so command substitution cannot discard a
	// trailing newline. The subshell keeps the two variables out of the caller.
	return "(nova_fuse_box=$(printf '%b_' " + liftOctalWord(box) + "); " +
		"nova_fuse_surface=$(printf '%b_' " + liftOctalWord(surface) + "); " +
		`nova-fuse lift quarantine --box "${nova_fuse_box%_}" -- "${nova_fuse_surface%_}")`
}

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
