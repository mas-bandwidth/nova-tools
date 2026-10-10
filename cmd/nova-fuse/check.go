// check.go holds the check verb: its flags, its run and the helpers only it uses.

package main

import (
	"fmt"
	"io"

	"github.com/mas-bandwidth/nova-tools/internal/fuse"
	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
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

// cmdCheck gates. Every ingestion path calls it, and only exit 0 is permission: 1 means
// a fuse is positively blown, 2 means it could not be proven clear.
func cmdCheck(rest []string, stdout, stderr io.Writer, inv invocation) int {
	box, positional, ok, parsed := parseBox("check", rest, stderr, inv.getenv)
	if !parsed {
		return 2
	}
	if len(positional) > 1 {
		refuse(stderr, " check", fmt.Sprintf("takes at most one surface, got %q too", positional[1]))
		ok = false
	}
	surface := ""
	if len(positional) == 1 {
		if fuse.Surface(positional[0]) == "" {
			refuse(stderr, " check", "surface must not be blank; omit it to check lockdown only")
			ok = false
		}
		surface = positional[0]
	}
	if !ok {
		return 2
	}

	b, err := fuse.ReadBox(boxFile(inv.wd, box))
	if err != nil {
		// Fail closed, and say which fact this is: "could not be read" is not "a fuse is
		// blown", and a claim must never outrun the measurement. Both refuse.
		fmt.Fprintf(stderr, "nova-fuse check REFUSED: %s -- cannot prove no fuse is blown, so treating every fuse as BLOWN, never as clear; %s; run: nova-fuse help\n", oneline.Err(err), oneline.Escape(remedy(err, box)))
		return 2
	}

	if b.Lockdown != nil {
		fmt.Fprintf(stderr, "FUSE FAILED lockdown since=%s: %s (hard: all untrusted reads and surface-driven acts stop, authored outbound continues; replaced only in a live conversation with the person you work with)\n",
			since(*b.Lockdown), why(*b.Lockdown))
		return 1
	}

	if name, f, ok := b.Quarantined(surface); ok {
		fmt.Fprintf(stderr, "FUSE FAILED quarantine=%s since=%s: %s (soft: yours to lift when the surface is safe again: %s)\n",
			oneline.Field(name), since(f), why(f), oneline.Escape(liftRemedy(box, fuse.Surface(name))))
		return 1
	}
	// Besides the typed surface, fail closed against the spelling obtained by decoding
	// oneline.Field escapes (\xNN and \uNNNN) in it (docs/SECURITY.md; security#74 finding 2).
	// If a surface "spaced name" is quarantined, status displays quarantine=spaced\x20name;
	// typing back that displayed token must refuse with FUSE FAILED rather than failing open.
	if decoded := unescapeField(surface); decoded != surface {
		if name, f, ok := b.Quarantined(decoded); ok {
			fmt.Fprintf(stderr, "FUSE FAILED quarantine=%s since=%s: %s (soft: yours to lift when the surface is safe again: %s)\n",
				oneline.Field(name), since(f), why(f), oneline.Escape(liftRemedy(box, fuse.Surface(name))))
			return 1
		}
	}

	if surface == "" {
		// Name what was verified and what was not. A bare check has proven only that there
		// is no lockdown; it has checked no quarantine at all, and a caller that reads
		// "clear" as "this surface is clear" leaves reads reaching the wire ungated.
		fmt.Fprintln(stdout, "FUSE OK lockdown=clear (no surface named; no quarantine checked)")
		return 0
	}
	fmt.Fprintf(stdout, "FUSE OK lockdown=clear quarantine=clear surface=%s\n", oneline.Field(fuse.Surface(surface)))
	return 0
}

func unhex(c byte) (byte, bool) {
	switch {
	case '0' <= c && c <= '9':
		return c - '0', true
	case 'a' <= c && c <= 'f':
		return c - 'a' + 10, true
	case 'A' <= c && c <= 'F':
		return c - 'A' + 10, true
	default:
		return 0, false
	}
}

// unescapeField decodes oneline.Field escapes (\xNN and \uNNNN) in s, acting as
// the inverse of oneline.Field (docs/SECURITY.md; security#74 finding 2).
// oneline.Field emits \xNN for each byte of invalid UTF-8, so \xNN decodes to the
// raw byte, never to a rune; \uNNNN decodes to that rune's UTF-8 encoding.
// Incomplete or invalid escape sequences are left as literal text.
func unescapeField(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); {
		if i+4 <= len(s) && s[i] == '\\' && s[i+1] == 'x' {
			h1, ok1 := unhex(s[i+2])
			h2, ok2 := unhex(s[i+3])
			if ok1 && ok2 {
				out = append(out, h1<<4|h2)
				i += 4
				continue
			}
		}
		if i+6 <= len(s) && s[i] == '\\' && s[i+1] == 'u' {
			h1, ok1 := unhex(s[i+2])
			h2, ok2 := unhex(s[i+3])
			h3, ok3 := unhex(s[i+4])
			h4, ok4 := unhex(s[i+5])
			if ok1 && ok2 && ok3 && ok4 {
				out = append(out, string(rune(h1)<<12|rune(h2)<<8|rune(h3)<<4|rune(h4))...)
				i += 6
				continue
			}
		}
		out = append(out, s[i])
		i++
	}
	return string(out)
}
