package log

// redact.go is SPEC-LOGS.md Part 2's one hard rule -- "What must never be logged: a
// secret VALUE" -- as code that runs inside the emitter, so the leak is caught before the
// line leaves the process and not in review. Part 5 names it a red test of the emitter,
// and redact_test.go is that test.
//
// The choice here is REDACTION, not refusal. An event whose message happened to carry a
// token is still the event that says what a mechanical part did; dropping it would trade
// a leak for a blind spot, and a blind spot is the thing this whole stack exists to
// close. The value is replaced with [redacted]; the line still ships, still one line,
// with every field a query selects on intact.
//
// The rules are deliberately ordered from the specific to the general, and the general
// one is deliberately narrow: a redaction that eats a git sha or a card id would make the
// log useless, so the entropy rule wants upper case, lower case AND a digit in one run,
// which a 40-character lowercase-hex sha never has.

import (
	"regexp"
	"strings"
)

// Redacted is what replaces a secret value. It is a fixed string so a query can find the
// lines where something was redacted, and a test can assert the mark is there.
const Redacted = "[redacted]"

// entropyRun is the general rule: a long token drawn from the credential alphabet that
// mixes case and digits. The floors below are what keep the ids we query with out of its
// jaws, and each one was set by a thing it wrongly ate:
//
//   - 32 bytes, not 24, because a pulse id (20260917T165603Z-pulse-f193e3, 29 bytes) is a
//     mixed-class run and losing it would blind the launch query.
//   - three of EACH class, not one, because that same id carries exactly two upper-case
//     letters (its T and its Z) while a credential of this length carries a spread.
//
// A 40-character lowercase-hex sha fails on the first class and always survives, which is
// the property the dev-moved event depends on.
const (
	entropyMin      = 32
	entropyPerClass = 3
)

var (
	// prefixed credentials: the shapes a provider stamps on its own keys. Each is
	// anchored on the prefix, so the rule never has to guess where the value starts.
	reProviderKeys = regexp.MustCompile(`(?:` +
		`sk-[A-Za-z0-9_-]{16,}` + // OpenAI-style and the several that copied it
		`|sk_[A-Za-z0-9_-]{16,}` +
		`|gh[pousr]_[A-Za-z0-9]{20,}` + // GitHub classic tokens
		`|github_pat_[A-Za-z0-9_]{20,}` + // GitHub fine-grained tokens
		`|glpat-[A-Za-z0-9_-]{16,}` + // GitLab
		`|xox[abprs]-[A-Za-z0-9-]{10,}` + // Slack
		`|A(?:KIA|SIA)[A-Z0-9]{16}` + // AWS access key ids
		`|AGE-SECRET-KEY-1[0-9A-Za-z]{20,}` + // age identities
		`|dckr_pat_[A-Za-z0-9_-]{16,}` + // Docker Hub
		`|npm_[A-Za-z0-9]{30,}` + // npm
		`)`)

	// a PEM private key block, whatever the algorithm names it.
	rePEM = regexp.MustCompile(`(?s)-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----.*?-----END [A-Z0-9 ]*PRIVATE KEY-----`)

	// an Authorization header's credential, scheme and all.
	reBearer = regexp.MustCompile(`(?i)\b(?:bearer|basic|token)\s+[A-Za-z0-9._~+/=-]{8,}`)

	// a keyed assignment: the KEY names a credential, so whatever follows is its
	// value. This over-redacts a line that names a secret without its value, which is
	// the side to err on: SPEC-LOGS allows naming the secret, and losing the name
	// costs a word, while losing the value costs the fleet.
	reKeyedValue = regexp.MustCompile(`(?i)\b([A-Za-z0-9_-]*(?:api[_-]?key|apikey|access[_-]?key|private[_-]?key|secret|token|password|passwd|credential)[A-Za-z0-9_-]*)\s*([:=])\s*("?)([^\s"',;]+)`)

	// the general rule, last: a long mixed-class run from the credential alphabet.
	reEntropyRun = regexp.MustCompile(`[A-Za-z0-9+/=_-]{` + itoa(entropyMin) + `,}`)
)

// Redact returns s with every secret-shaped value replaced by Redacted. It is safe to
// call on any string, including one already redacted: the mark itself matches no rule.
func Redact(s string) string {
	if s == "" {
		return s
	}
	s = rePEM.ReplaceAllString(s, Redacted)
	s = reProviderKeys.ReplaceAllString(s, Redacted)
	s = reBearer.ReplaceAllString(s, Redacted)
	s = reKeyedValue.ReplaceAllStringFunc(s, func(m string) string {
		g := reKeyedValue.FindStringSubmatch(m)
		if len(g) != 5 || g[4] == Redacted {
			return m
		}
		return g[1] + g[2] + g[3] + Redacted
	})
	s = reEntropyRun.ReplaceAllStringFunc(s, func(m string) string {
		if mixedClasses(m) {
			return Redacted
		}
		return m
	})
	return s
}

// mixedClasses is the entropy rule's test: at least entropyPerClass upper-case letters,
// lower-case letters and digits in one run. A lowercase-hex sha fails it on the first
// class, a timestamped id fails it on the spread, and a random credential of this length
// passes it -- and that difference is the whole reason the rule is safe to run over every
// message a mechanical part writes.
func mixedClasses(s string) bool {
	var upper, lower, digit int
	for _, r := range s {
		switch {
		case r >= 'A' && r <= 'Z':
			upper++
		case r >= 'a' && r <= 'z':
			lower++
		case r >= '0' && r <= '9':
			digit++
		}
	}
	return upper >= entropyPerClass && lower >= entropyPerClass && digit >= entropyPerClass
}

// itoa keeps the regexp literal above readable without pulling strconv into a file whose
// only number is a compile-time constant.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b strings.Builder
	var digits [8]byte
	i := len(digits)
	for n > 0 {
		i--
		digits[i] = byte('0' + n%10)
		n /= 10
	}
	b.Write(digits[i:])
	return b.String()
}
