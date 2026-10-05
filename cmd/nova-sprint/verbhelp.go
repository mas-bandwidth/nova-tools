package main

import (
	"flag"
	"io"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
)

// releaseHoldWords tells release apart from the holds on a member, a reader,
// and a friend (docs/SPEC-SPRINT.md: release is the sentinel and held-card step).
const releaseHoldWords = `release acts on a sentinel or a held card. It does not release a held member, reader, friend or stream:
  unhold <name>... releases any of them (one verb for the four)
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
	"run":           "exit codes: 0 stopped (an interrupt), 2 usage or a store that did not answer, 3 its binary was replaced on disk (its supervisor starts the new one)",
	"fleet sync":    "exit codes: 0 done (--check: no drift), 1 refused, 2 usage, a store that did not answer, or (--check) there is drift, 3 the config could not be read",
	"friend sync":   "exit codes: 0 done, 1 refused (a friend row's name, or a working directory that cannot be read), 2 usage or a store that did not answer, 3 the config could not be read or holds no friend row",
	"land":          "exit codes: 0 every batch landed (--dry-run: would land), 1 a batch was refused (its line names the next step), 2 usage, a store that did not answer, or a push that landed and was not reported (run land again)",
	"check":         "exit codes: 0 no violation, 1 a violation (each on its line), 2 usage or a store that did not answer",
	"selftest":      "exit codes: 0 the selftest landed its card through the tree gate (SELFTEST OK), 1 it did not (SELFTEST FAILED names the step, the why and the kept directory), 2 usage",
	"seat check":    "exit codes: 0 done (every check OK), 1 a check is DOWN, 2 usage or a store that did not answer",
	"machinery":     "exit codes: 0 done (every check OK), 1 a check is DOWN, 2 usage or a store that did not answer",
	"answer":        "exit codes: 0 done (each routine judgment's card applied or listed; --every: the machine is STOPPED), 1 a line applied was refused or a decision's backend failed, 2 usage, an actor not the coordinator, or a sprint that did not answer",
	"dashboard":     "exit codes: 0 stopped (an interrupt), 2 usage or an address it cannot listen on, 3 its binary was replaced on disk (its supervisor starts the new one)",
	"selftest land": "exit codes: 0 done, 1 failed (lander broken or card did not land), 2 usage",
	"server switch": "exit codes: 0 done, 1 failed, 2 usage",
}

// verbEffect is a verb's effect line, the last line of its -h, where the verb
// states one (docs/STANDARD.md: `effect: inspection|local write|delivery`).
var verbEffect = map[string]string{
	"hold":             "local write: holds the named members, readers, friends or streams in the sprint's store (--return also hands back their begun work); --dry-run writes nothing",
	"unhold":           "local write: releases the named holds in the sprint's store; --dry-run writes nothing",
	"check":            "inspection: reads the sprint's tables and prints each violation, writes nothing",
	"routes":           "inspection: reads the route table and prints each route, writes nothing",
	"promote":          "delivery: promotes the landed cards toward the development branch and records the promotion in the sprint's store; --dry-run prints the branch and the landed cards and changes nothing",
	"friend clean":     "local write: removes the friends' finished job directories and listings past --days under --root; --dry-run prints every removal with the bytes it would free and removes nothing",
	"where":            "inspection: reads the sprint table and its rows, writes nothing",
	"card":             "inspection: reads one card, its brief and its attempts, writes nothing",
	"log":              "inspection: reads the sprint's change log, writes nothing",
	"stats":            "inspection: reads the sprint's counts and rates, writes nothing",
	"queue":            "inspection: reads a worker's or a stream's cards, writes nothing",
	"handover":         "inspection: reads the store, writes nothing",
	"seat check":       "inspection: checks server, store, loop, beats, readers, dashboard, installed versions, merge queue, writes nothing",
	"seat install":     "local write: writes the push loop's unit (inbox --wait --push seat) into --dir and loads it with launchctl (macOS) or systemctl --user (Linux); --dry-run prints it and writes nothing",
	"seat uninstall":   "local write: unloads the push loop's unit and removes its file from --dir",
	"machinery":        "inspection: checks server, store, loop, beats, readers, dashboard, installed versions, merge queue, writes nothing",
	"needs":            "inspection: reads the waiting cards, writes nothing",
	"held":             "inspection: reads the held cards of the table, writes nothing",
	"sentinels":        "inspection: reads the sentinels and what each waits on, writes nothing",
	"view coordinator": "inspection: reads what needs the seat (the tables, the inbox, the friends and the machines), writes nothing",
	"view worker":      "inspection: reads the worker's cards, their packets and its results not landed, writes nothing",
	"seat":             "inspection: reads the seat (holder, epoch, generation), writes nothing",
	"rules":            "inspection: reads the rules the tick answers by and why the fleet is idle, writes nothing",
	"relink":           "local write: re-points what waited on the old cards to their twin in the sprint's store and answers their blocked judgments; --dry-run writes nothing",
	"friend take":      "local write: takes the named cards back from the friend in the sprint's store; --dry-run writes nothing",
	"friend level":     "local write: moves queued cards between the friends' rows in the sprint's store; --dry-run writes nothing",
	"friend health":    "local write: records the coordinator's observation of the friend in the sprint's store; --dry-run writes nothing",
	"reader retire":    "local write: retires the named readers in the sprint's store; a read it is reading is taken back at the next tick and asked of a reader up with no card at that attempt, and it stays when none can take it; --dry-run writes nothing",
	"promoted":         "local write: records the promotion in the sprint's store; --dry-run writes nothing",
	"preflight":        "inspection: reads the briefs, the table and the repository, writes nothing",
	"selftest":         "local write: makes a fresh directory, a bare origin and a clone whose base holds a go module, runs the card's flow of the walkthrough on a twin file in it and lands one card through the tree gate; writes only in that directory, opens no store of the caller's and no network, and removes it unless --keep",
	"dashboard":        "inspection: serves the page and the pull routes, reads the sprint as where --json --cards does, writes nothing",
	"coordinator":      "delivery: moves the seat in the sprint's store, a note to the old holder on a take; --dry-run writes nothing",
	"answer":           "delivery: sends the routine judgments' state to the decision's backend (Jev), applies the verbs chosen through the sprint's verbs, and appends to --record; --dry-run asks and writes nothing",
	"selftest land":    "inspection: lands a canned card on a scratch clone with this binary, writes nothing to the sprint",
	"server switch":    "local write: switches the server binary on disk, keeping the previous binary and rolling back on land failure in the window",
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

// briefExampleCard is an example card brief that passes the card lint,
// matching `nova-swarm template --name card`.
const briefExampleCard = "RESULT: <label> sha=<sha12>\n" +
	"REPO: <owner>/<name>\n" +
	"BASE: <branch>\n" +
	"The REPO: and BASE: lines are the repository and the branch the work starts from and lands on: the member stages REPO: at BASE:, and nova-sprint land merges the card's head onto BASE: (land --base stands in for a card naming no BASE:, land --repo-dir for one naming no REPO:).\n" +
	"You are a child of the coordinator: one task, one worktree, one branch, unattended. This card is the whole of the task and it stands alone in front of a stranger; nothing outside it is owed to you.\n" +
	"Deadline: finish within <n> minutes.\n" +
	"\n" +
	"RULES.\n" +
	"Work only in the job directory this card names.\n" +
	"Never force-push or rebase a shared branch.\n" +
	"Never kill a process you did not start.\n" +
	"Never start a server on this machine.\n" +
	"No `rm -rf` outside the job directory.\n" +
	"Report what was not done.\n" +
	"\n" +
	"THE TASK. <What is wrong or wanted, in a paragraph a stranger can act on, and the file or package the work lives in: internal/<package>/<file>.go. Name the worktree path, the branch, the base branch, and every file you may touch.>\n" +
	"Libraries considered: <what the standard library and the adopted modules offer for this work, and why each is used or not; the search comes before any helper of more than about thirty lines is written>\n" +
	"\n" +
	"STEP 1. Enter your worktree with cd <worktree path> && git log --oneline -1; it is a NEW worktree on the branch this card names. Export GOFLAGS=-mod=readonly NOVA_TEST_NO_HOST=1 before any go command; GOCACHE is already set to the machine's shared build cache (JOB.md names it): keep it.\n" +
	"STEP 2. Write the red test first, named TestSomething, in <file>_test.go, opening with t.Parallel(). Run go test -count=1 -timeout 600s ./internal/<package>/ -run TestSomething and keep the failing line.\n" +
	"STEP 3. Make it pass in the files this card names, and only those. Cite the model or the design section from each function that implements a rule.\n" +
	"STEP 4. Run the gate: go test -count=1 -timeout 600s ./internal/<package>/ ./internal/ci/ and read the last line of each.\n" +
	"STEP 5. Commit on your own branch with the trailer. Nothing reaches the forge from inside the wall: in the job the git shim records a push, the pull request is the finish JOB.md names (STEP 6), and the member makes both, against <base>, from outside the wall when the card finishes. The pull request body states the diff stat, what was deleted, the tests with what each pins, and what was not done.\n" +
	"STEP 6. End as JOB.md says (docs/SPEC-CARD-CONTRACT.md): where JOB.md ends the card with its pull request, that is the end and there is nothing else to write, the gate's lines in the pull request body; where it asks for RESULT.md, write it in JOB.md's shape (head, branch, verdict, gate, output, report).\n"

// cardHelpWords is the brief example shown in help add and help brief.
const cardHelpWords = "a brief is held to the card lint (nova-swarm template --name card):\n\n" + briefExampleCard

// verbProse is the explanation a verb's -h carries past its flags. The banner
// still carries the inbox walkthrough and the friends section on their own.
func verbProse(name string) string {
	switch name {
	case "inbox":
		return inboxExample
	case "release":
		return releaseHoldWords
	case "friend beat", "friend down", "friend up", "friend health":
		return friendVerbWords(name)
	case "friend take":
		return friendTakeWords
	case "friend level":
		return friendLevelWords
	case "add", "brief":
		return cardHelpWords
	case "hold", "unhold":
		return holdWords()
	case "fleet down", "reader away", "reader up":
		return oldHoldWords(name)
	default:
		return ""
	}
}
