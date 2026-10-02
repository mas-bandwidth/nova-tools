package main

import (
	"io"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
)

// exitLine is the banner's exit-code paragraph, every verb's codes at once:
// `nova-sprint help` prints it whole, and a verb's -h prints that verb's own
// line in its place (verbExits; docs/STANDARD.md section 2, "Every verb's -h
// quotes the table, or the verb's own"; tool ledger P5, X9).
const exitLine = "exit codes: 0 done, 1 failed or incomplete (including refused), 2 usage or a store that did not answer (fleet sync --check: there is drift), 3 fleet sync or friend sync could not read the config, or run: its binary was replaced (its supervisor starts the new one)"

// verbExit is a verb's own exit codes where they are not the common three.
var verbExit = map[string]string{
	"run":          "exit codes: 0 stopped (an interrupt), 2 usage or a store that did not answer, 3 its binary was replaced on disk (its supervisor starts the new one)",
	"fleet sync":   "exit codes: 0 done (--check: no drift), 1 refused, 2 usage, a store that did not answer, or (--check) there is drift, 3 the config could not be read",
	"friend sync":  "exit codes: 0 done, 1 refused (a friend row's name), 2 usage or a store that did not answer, 3 the config could not be read or holds no friend row",
	"friend clean": "exit codes: 0 FRIENDS-CLEAN OK, 1 a removal or a read failed (FRIENDS-CLEAN FAILED names each; the summary is FRIENDS-CLEAN INCOMPLETE) or a friend row's name refused, 2 usage, 3 the config could not be read or holds no friend row",
	"land":         "exit codes: 0 every batch landed (--dry-run: would land), 1 a batch was refused (its line names the next step), 2 usage, a store that did not answer, or a push that landed and was not reported (run land again)",
	"check":        "exit codes: 0 no violation, 1 a violation (each on its line), 2 usage or a store that did not answer",
}

// commonExit is the codes of every other verb.
const commonExit = "exit codes: 0 done, 1 failed or incomplete (including refused; the line names why), 2 usage or a store that did not answer"

// verbExits is the exit-code line a verb's -h prints.
func verbExits(name string) string {
	if e, ok := verbExit[name]; ok {
		return e
	}
	return commonExit
}

// recoverHelp is verbflag.RecoverWith with the verb's own exit codes in place of
// the banner's paragraph: a verb's -h (or help <verb>) prints its usage quoted
// from the banner, its examples, its flags and its exit codes on stdout, exit 0.
// It is deferred directly, as RecoverWith is.
func recoverHelp(out io.Writer, code *int) {
	r := recover()
	if r == nil {
		return
	}
	h, ok := r.(verbflag.Help)
	if !ok {
		panic(r)
	}
	name := verbflag.Verb(prog, h.FS)
	var b strings.Builder
	verbflag.Print(&b, prog, strings.Replace(banner(), exitLine, verbExits(name), 1), h.FS)
	*code = 0
	if _, err := io.WriteString(out, verbflag.Insert(b.String(), verbExample(name))); err != nil {
		// the help did not reach its reader (a closed stdout): the exit code says so
		*code = 1
	}
}
