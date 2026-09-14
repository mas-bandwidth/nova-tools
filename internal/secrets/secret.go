// Package secrets implements the credential-layer machinery described in
// docs/SPEC-SECRETS.md. It provides four verbs -- exec, names, check, and keygen --
// as a thin wrapper over sops and age-keygen, linking no cryptography of its own.
//
// THE ONE RULE THIS PACKAGE EXISTS TO KEEP: a credential value is never printed,
// logged, put in an argument list, or written anywhere but the child process environment
// that asked for it by name.
package secrets

import (
	"fmt"
	"io"
	"strings"
)

// Redacted is the only string a Secret ever yields to a formatter. One word, in one
// place, so no caller can produce a "helpfully" partial one.
const Redacted = "[redacted]"

// DetailWithheld replaces a WHOLE detail line found to contain credential material.
// Deliberately not a surgical redaction: cutting the secret out of the middle of a
// string and printing the rest is how a truncated secret gets printed.
const DetailWithheld = "[detail withheld: it contained credential material]"

// MinLeakFragment is how many consecutive bytes of a secret count as a leak. Six, not
// "the whole value", because the leak that actually happens is a truncated one.
const MinLeakFragment = 6

// Secret carries a credential value. The zero value is "never looked up", which is
// distinct from "looked up and found empty".
//
// WHY A TYPE AND NOT A string: every formatting route is closed -- String, GoString,
// Format (which covers %v %s %q %x %#v and every other verb), MarshalText and
// MarshalJSON all yield Redacted. There is deliberately no accessor that RETURNS the
// value: Use hands it to a callback and takes it back, so an exposure is a visible,
// deliberate shape rather than an interpolation.
type Secret struct {
	v      string
	loaded bool
}

// NewSecret wraps a value. The caller should drop its own copy immediately.
func NewSecret(v string) Secret { return Secret{v: v, loaded: true} }

// Loaded reports whether a lookup actually produced a value.
func (s Secret) Loaded() bool { return s.loaded }

// Empty reports whether the value is the empty string. A present-but-empty entry is not
// a credential.
func (s Secret) Empty() bool { return s.v == "" }

// Len is the byte length of the value.
func (s Secret) Len() int { return len(s.v) }

// Use is the ONLY route from a Secret back to a string, and it does not return one.
func (s Secret) Use(fn func(string) error) error {
	if fn == nil {
		return fmt.Errorf("secrets: Use called with no function; refusing to expose a secret to nothing")
	}
	return fn(s.v)
}

func (s Secret) String() string   { return Redacted }
func (s Secret) GoString() string { return "secrets.Secret(" + Redacted + ")" }

// Format closes every fmt verb at once. String() alone is not enough: %#v goes to
// GoStringer, %x and %d bypass Stringer entirely.
func (s Secret) Format(f fmt.State, verb rune) { _, _ = io.WriteString(f, Redacted) }

func (s Secret) MarshalText() ([]byte, error) { return []byte(Redacted), nil }
func (s Secret) MarshalJSON() ([]byte, error) { return []byte(`"` + Redacted + `"`), nil }

// Leaks reports whether text contains this secret, whole or in a fragment of at least
// MinLeakFragment consecutive bytes.
func Leaks(text string, s Secret) bool {
	if !s.loaded || s.v == "" || text == "" {
		return false
	}
	if len(s.v) <= MinLeakFragment {
		return strings.Contains(text, s.v)
	}
	for i := 0; i+MinLeakFragment <= len(s.v); i++ {
		if strings.Contains(text, s.v[i:i+MinLeakFragment]) {
			return true
		}
	}
	return false
}

// SafeDetail gates a human-readable detail line against a secret. It returns the line,
// or DetailWithheld, and whether it withheld.
func SafeDetail(detail string, s Secret) (string, bool) {
	if Leaks(detail, s) {
		return DetailWithheld, true
	}
	return detail, false
}
