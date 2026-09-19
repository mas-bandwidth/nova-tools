// Package goenv builds the environment a tool hands to a child `go` command.
//
// A tool that runs `go test`, `go build`, `go vet` or `go list` for its own
// purposes reads the child's output: nova-review mutate counts the `--- PASS:`
// and `--- FAIL:` lines of an inner `go test`, nova-merge simulate quotes a
// check's first line. The environment the tool was started in can reshape that
// output under the parser's feet, and then the tool reports something that
// never happened.
//
// That is not hypothetical. CI's `make test` exports GOFLAGS=-json. On
// 2026-09-18 the inner `go test` of `nova-review mutate` inherited it, came
// back as a JSON stream with no `--- PASS:` line in it, and the parser counted
// the run that stayed green as red: `MUTATE <sha> red=1 green=1 PASS` became
// `red=2 green=0`, and three legs of integration-4 failed on a tool that was
// working perfectly.
//
// Clean is the one answer for the whole class: the parent's environment minus
// everything that can change the shape of a go command's output. A tool that
// needs a variable of its own appends it AFTER Clean, where the last value
// wins.
//
//	cmd := exec.CommandContext(ctx, "go", args...)
//	cmd.Env = append(goenv.Clean(os.Environ()), "GOTMPDIR="+scratch)
//
// internal/ci enforces this: every exec.Command whose argv[0] is "go" outside
// this package must build its environment from Clean.
package goenv

import (
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/keyshape"
)

// Removed is the documented list of what Clean drops, and the only list. It is
// read by people, not by code -- the rules below are the implementation -- so
// that a reader can see the whole set without reading the matcher.
//
//	GOFLAGS      flags the go command prepends to EVERY invocation. -json turns
//	             `go test` and `go build` into a JSON stream, -count and -race
//	             change what a run means, and -mod can make a build refuse.
//	GOTEST*      GOTESTFLAGS, GOTESTSUM_FORMAT and anything else a harness
//	             exports to steer a test run: not the go command's own, but
//	             read by the wrappers CI puts around it.
//	GO*=...-json any other GO-prefixed variable carrying a -json or --json
//	             flag, which is the shape of this bug wherever it turns up next.
//
// GOTMPDIR is deliberately NOT dropped: it names a location, not an output
// shape, and a bench that sets it usually has a reason (a small /tmp). A tool
// that wants its own scratch appends GOTMPDIR= after Clean.
const Removed = "GOFLAGS, GOTEST*, and any GO* variable whose value carries -json"

// Clean returns a copy of env with the variables named in Removed taken out.
// The order of what remains is preserved, and env itself is not modified: the
// caller keeps its own os.Environ().
func Clean(env []string) []string {
	out := make([]string, 0, len(env))
	for _, entry := range env {
		name, value, ok := strings.Cut(entry, "=")
		if ok && removes(name, value) {
			continue
		}
		out = append(out, entry)
	}
	return out
}

// WithoutSecrets returns a copy of env with every variable whose NAME carries
// KEY, TOKEN or SECRET taken out. It is not the output-shape rule Clean is:
// Clean keeps the child `go` command's answers comparable, and this one keeps a
// program the gate did not write from reading the seat's credentials. The
// predicate is keyshape.SecretName, the same one the job shell's shim and the
// harvest's argv log redact by, so there is one definition of a secret name.
//
// A gate runs a card's tree -- its git filters and its tests -- and the process
// it was started in already holds the provider key, GH_TOKEN and the rest
// (#1814 was the job shell; this is the gate's own children, #1897). The value
// is never read, printed or copied: the name is what decides, and a variable
// that does not carry one is left alone.
func WithoutSecrets(env []string) []string {
	out := make([]string, 0, len(env))
	for _, entry := range env {
		name, _, ok := strings.Cut(entry, "=")
		if ok && keyshape.SecretName(name) {
			continue
		}
		out = append(out, entry)
	}
	return out
}

// removes is the matcher behind Removed. Names are folded to upper case
// because Windows environment names are case-insensitive and os.Environ there
// returns them however they were written.
func removes(name, value string) bool {
	up := strings.ToUpper(strings.TrimSpace(name))
	switch {
	case up == "GOFLAGS":
		return true
	case strings.HasPrefix(up, "GOTEST"):
		return true
	case strings.HasPrefix(up, "GO") && carriesJSONFlag(value):
		return true
	}
	return false
}

// carriesJSONFlag reports whether a value holds a -json or --json flag, in any
// of the spellings a flag list uses: on its own, or with a value attached.
func carriesJSONFlag(value string) bool {
	for _, field := range strings.Fields(value) {
		if !strings.HasPrefix(field, "-") {
			continue
		}
		flag := strings.TrimPrefix(strings.TrimPrefix(field, "--"), "-")
		if name, _, _ := strings.Cut(flag, "="); strings.EqualFold(name, "json") {
			return true
		}
	}
	return false
}
