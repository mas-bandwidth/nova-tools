//go:build !darwin

// Every platform whose body is not built yet REFUSES, and this file is that refusal.
// Rule 1 is "OS-enforced or refused": a stub that proceeded would be the silent sandbox
// the whole tool exists to prevent, so the linux (Landlock) and windows (AppContainer)
// bodies named in docs/SPEC-SANDBOX.md are not stubbed as pass-throughs — they are
// stubbed as NO, and the refusal names the platform so that a reader knows which body
// is missing rather than that "the sandbox failed".
package sandbox

import (
	"io"
	"runtime"
)

// Backend is what the SANDBOX OK line would name here — and no line here says OK.
const Backend = "none"

// ABI is the abi= field; there is no backend to have one.
const ABI = "-"

// Available is rule 1's question, and on a platform with no body the answer is no.
func Available() (string, bool) { return "", false }

// NetEnforceable: an enforced network denial needs a backend first.
func NetEnforceable() bool { return false }

// Note is the one clause the check verb prints about this machine.
func Note() string {
	return "the " + runtime.GOOS + " body of docs/SPEC-SANDBOX.md is not built yet; this build wraps nothing on " + runtime.GOOS
}

// Run refuses, and the command does not run. It never returns a zero status.
func Run(p *Policy, env []string, stdin io.Reader, stdout, stderr io.Writer, okLine func()) (int, error) {
	return ExitRefused, refuse("no_sandbox",
		"this build has no %s body: the darwin body is built and the %s one (%s) is not, so there is nothing to contain this command with. Run it on darwin, or build the %s body first — there is no fallback and no degraded mode",
		runtime.GOOS, runtime.GOOS, backendNameFor(runtime.GOOS), runtime.GOOS)
}

// backendNameFor names the backend the spec assigns each platform, so that the refusal
// says which thing is missing rather than only that something is.
func backendNameFor(goos string) string {
	switch goos {
	case "linux":
		return "landlock"
	case "windows":
		return "appcontainer"
	}
	return "no backend this spec names"
}
