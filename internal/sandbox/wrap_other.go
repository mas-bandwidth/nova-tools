//go:build !darwin && !linux

// Every platform whose body is not built yet REFUSES, and this file is that refusal.
// Rule 1 is "OS-enforced or refused": a stub that proceeded would be the silent sandbox
// the whole tool exists to prevent, so the windows (AppContainer) body named in
// docs/SPEC-SANDBOX.md is not stubbed as a pass-through — it is stubbed as NO, and the
// refusal names the platform so that a reader knows which body is missing rather than
// that "the sandbox failed". darwin (sandbox-exec) and linux (landlock) are built and
// have bodies of their own; this file is what is left.
package sandbox

import (
	"io"
	"runtime"
)

// Backend is what the SANDBOX OK line would name here — and no line here says OK.
const Backend = "none"

// ABI is the abi= field; there is no backend to have one.
func ABI() string { return "-" }

// ClampedABI is linux's alone: only Landlock has a numbered table this tool can be newer
// or older than. Here there is no number, so there is nothing to clamp and no used= field.
func ClampedABI() (int, bool) { return 0, false }

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
	case "windows":
		return "appcontainer"
	}
	return "no backend this spec names"
}
