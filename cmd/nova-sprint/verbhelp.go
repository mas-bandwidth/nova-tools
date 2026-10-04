package main

import (
	"flag"
	"io"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
)

// releaseHoldWords tells release apart from the holds on a member, a reader,
// and a friend (docs/SPEC-SPRINT.md: release is the sentinel and held-card step).
const releaseHoldWords = `release acts on a sentinel or a held card. It does not release a held member, reader, or friend:
  fleet up <member> releases a held member
  reader up <reader> releases a held reader
  friend up <friend> releases a held friend
`

// exitLine is the banner's exit-code paragraph, every verb's codes at once:
// `nova-sprint help` prints it whole, and a verb's -h prints that verb's own
// line in its place (verbExits; docs/STANDARD.md section 2, "Every verb's -h
// quotes the table, or the verb's own"; tool ledger P5, X9).
const exitLine = "exit codes: 0 done, 1 failed or incomplete (including refused), 2 usage or a store that did not answer (fleet sync --check: there is drift), 3 fleet sync or friend sync could not read the config, or run: its binary was replaced (its supervisor starts the new one)"

// verbExit is a verb's own exit codes where they are not the common three.
var verbExit = map[string]string{
	"run":         "exit codes: 0 stopped (an interrupt), 2 usage or a store that did not answer, 3 its binary was replaced on disk (its supervisor starts the new one)",
	"fleet sync":  "exit codes: 0 done (--check: no drift), 1 refused, 2 usage, a store that did not answer, or (--check) there is drift, 3 the config could not be read",
	"friend sync": "exit codes: 0 done, 1 refused (a friend row's name, or a working directory that cannot be read), 2 usage or a store that did not answer, 3 the config could not be read or holds no friend row",
	"land":        "exit codes: 0 every batch landed (--dry-run: would land), 1 a batch was refused (its line names the next step), 2 usage, a store that did not answer, or a push that landed and was not reported (run land again)",
	"check":       "exit codes: 0 no violation, 1 a violation (each on its line), 2 usage or a store that did not answer",
	"answer":      "exit codes: 0 done (each routine judgment's card applied or listed; --every: the machine is STOPPED), 1 a line applied was refused or a decision's backend failed, 2 usage, an actor not the coordinator, or a sprint that did not answer",
	"dashboard":   "exit codes: 0 stopped (an interrupt), 2 usage or an address it cannot listen on, 3 its binary was replaced on disk (its supervisor starts the new one)",
}

// verbEffect is a verb's effect line, the last line of its -h, where the verb
// states one (docs/STANDARD.md: `effect: inspection|local write|delivery`).
var verbEffect = map[string]string{
	"handover":    "inspection: reads the store, writes nothing",
	"dashboard":   "inspection: serves the page and the pull routes, reads the sprint as where --json --cards does, writes nothing",
	"coordinator": "delivery: moves the seat in the sprint's store, a note to the old holder on a take; --dry-run writes nothing",
	"answer":      "delivery: sends the routine judgments' state to the decision's backend (Jev), applies the verbs chosen through the sprint's verbs, and appends to --record; --dry-run asks and writes nothing",
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
	text := verbflag.Insert(concreteUsage(b.String(), name, h.FS), verbExample(name))
	if e, ok := verbEffect[name]; ok {
		text += "effect: " + e + "\n"
	}
	if extra := verbProse(name); extra != "" {
		if !strings.HasSuffix(text, "\n") {
			text += "\n"
		}
		text += "\n" + extra
		if !strings.HasSuffix(text, "\n") {
			text += "\n"
		}
	}
	if _, err := io.WriteString(out, text); err != nil {
		// the help did not reach its reader (a closed stdout): the exit code says so
		*code = 1
	}
}

// concreteUsage replaces Print's placeholder usage line with the verb table's
// synopsis, or with the flags the set registers when that synopsis is empty.
func concreteUsage(help, name string, fs *flag.FlagSet) string {
	line, ok := sprintUsageLine(name, fs)
	if !ok {
		return help
	}
	_, rest, found := strings.Cut(help, "\n")
	if !found {
		return line + "\n"
	}
	return line + "\n" + rest
}

// sprintUsageLine is the verb's usage line. The second result is false when
// name is none of the verb table.
func sprintUsageLine(name string, fs *flag.FlagSet) (string, bool) {
	for _, v := range verbs {
		if v.name != name {
			continue
		}
		if syn := strings.TrimSpace(v.syntax); syn != "" {
			return verbflag.UsageLineSynopsis(prog, name, nil, syn), true
		}
		if syn := verbflag.FlagSynopsis(fs); syn != "" {
			return verbflag.UsageLineSynopsis(prog, name, nil, syn), true
		}
		return "usage: " + strings.TrimSpace(prog+" "+name), true
	}
	return "", false
}

// verbProse is the explanation a verb's -h carries past its flags. The banner
// still carries the inbox walkthrough and the friends section on their own.
func verbProse(name string) string {
	switch name {
	case "inbox":
		return inboxExample
	case "release":
		return releaseHoldWords
	case "friend beat", "friend down", "friend up", "friend take":
		return friendVerbWords(name)
	default:
		return ""
	}
}
