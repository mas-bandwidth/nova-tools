package main

import (
	"flag"
	"io"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
)

// releaseHoldWords distinguishes a card's release operand from the holds on a
// member, a reader and a friend (docs/SPEC-ISA.md, the one wait kind).
const releaseHoldWords = `release resolves a wait on a release: a sentinel or a held card. It does not release a held member, reader, friend or stream:
  unhold <name>... releases any of them (one verb for the four)
  fleet unhold <member> and friend unhold <friend> release a held member or friend alone
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
	"demo load":     "exit codes: 0 the demo is up and where read it, 1 failed (a damaged backup, a demo already up, a Redis that did not start or a line not replayed: what this load started is stopped and removed), 2 usage",
	"demo stop":     "exit codes: 0 stopped and removed, 1 failed (no demo up, or a recorded pid or directory that is not the demo's: nothing stopped or removed), 2 usage",
	"server switch": "exit codes: 0 done, 1 failed or refused (the candidate's shadow tick failed: nothing changed), 2 usage",
}

// verbEffect is a verb's effect line, the last line of its -h, where the verb
// states one (docs/STANDARD.md: `effect: inspection|local write|delivery`).
var verbEffect = map[string]string{
	"hold":              "local write: holds the named members, readers, friends or streams in the sprint's store (--return also hands back their begun work); --dry-run writes nothing",
	"unhold":            "local write: releases the named holds in the sprint's store; --dry-run writes nothing",
	"fleet hold":        "local write: holds the named fleet members in the sprint's store, as hold does (--return also hands back their begun work); --dry-run writes nothing",
	"fleet unhold":      "local write: releases the named fleet members' holds in the sprint's store, as unhold does; --dry-run writes nothing",
	"friend hold":       "local write: holds the named friends in the sprint's store, as hold does (every card she holds goes back to ready); --dry-run writes nothing",
	"friend unhold":     "local write: releases the named friends' holds in the sprint's store, as unhold does; --dry-run writes nothing",
	"check":             "inspection: reads the sprint's tables and prints each violation, writes nothing",
	"routes":            "inspection: reads the route table and prints each route, writes nothing",
	"cost reconcile":    "local write: reads each provider's usage of today through the seat's key and writes the reconciliation and its gap judgment to the sprint's store; --dry-run writes nothing",
	"cost reprice":      "local write: rewrites every priced consumer record's cost, each card's totals and each stream's landed sum from the records' tokens at the routes' current prices in the sprint's store; --dry-run writes nothing",
	"backup":            "local write: with --out, writes the epoch's keys and the shared keys as a RESTORE text dump, xz -9, split into parts under 100 MB, with SHA256SUMS and a README section, after restoring the parts into a throwaway store under this build's function library, comparing the counts, and scanning for every nova-secrets value under nova-secrets exec (counts only); --out is written only when every step passed. With --file, writes the store to a new owner-only file, restores it into a twin, compares and scans it for secrets, and removes the file when any step fails; --dry-run writes nothing; the store is only read",
	"demo load":         "local write: starts a throwaway Redis on a free 127.0.0.1 port in a directory of its own under --dir, loads this build's function library and the backup into it, and records its port, pid and directory in the state file there; the live store is never opened",
	"demo stop":         "local write: stops the Redis demo load started (the pid in its state file, only when the Redis at the recorded address is that pid) and removes the recorded directory and the state file; nothing else",
	"promote":           "delivery: promotes the landed cards toward the development branch and records the promotion in the sprint's store; --dry-run prints the branch and the landed cards and changes nothing",
	"friend clean":      "local write: removes the friends' finished job directories and listings past --days under --root; --dry-run prints every removal with the bytes it would free and removes nothing",
	"where":             "inspection: reads the sprint table, its rows and the epoch log, writes nothing; --json carries landedSeries, cards landed per 10 minutes over the last 24 hours (144 buckets), friends and fleet by the worker of the landed attempt (the last <who>:ok of the card's .wN, never the lander; a sentinel's release is not work)",
	"card":              "inspection: reads one card, its brief and its attempts, writes nothing",
	"log":               "inspection: reads the sprint's change log, writes nothing",
	"stats":             "inspection: reads the sprint's counts and rates, writes nothing",
	"stats reset":       "local write: writes one mark, the counters as they stand, into the sprint's stats record; nothing moves, and every figure counts from the mark; --dry-run and --show write nothing",
	"stats tidy":        "local write: takes the history off the done cells named and writes the tidy's archive and stats record to the sprint's store; --dry-run writes nothing",
	"queue":             "inspection: reads a worker's or a stream's cards, writes nothing",
	"handover":          "inspection: reads the store, writes nothing",
	"seat check":        "inspection: checks server, store, loop, beats, readers, dashboard, installed versions, merge queue, writes nothing",
	"seat install":      "local write: writes the push loop's unit (inbox --wait --push seat) into --dir and loads it with launchctl (macOS) or systemctl --user (Linux); --dry-run prints it and writes nothing",
	"seat uninstall":    "local write: unloads the push loop's unit and removes its file from --dir; --dry-run names the unit and unloads and removes nothing",
	"machinery":         "inspection: checks server, store, loop, beats, readers, dashboard, installed versions, merge queue, writes nothing",
	"needs":             "inspection: reads the waiting cards, writes nothing",
	"streams":           "inspection: reads the work and merge tables once and prints each stream with the repositories and bases its cards record, its release, its open and landed counts, and with --cards every card's id, state, tier, title and needs; writes nothing",
	"held":              "inspection: reads the held cards of the table, writes nothing",
	"sentinels":         "inspection: reads the sentinels and what each waits on, writes nothing",
	"sentinel set":      "local write: replaces the sentinel's needs in the sprint's store, keeping its id, stream, score and log; --dry-run writes nothing",
	"view cards":        "inspection: lists or counts (--by tier|stream|col|holder) the work table's primaries, filtered by --col, --stream, --holder; writes nothing",
	"view coordinator":  "inspection: reads what needs the seat (the tables, the inbox, the friends and the machines), writes nothing",
	"view worker":       "inspection: reads the worker's cards, their packets and its results not landed, writes nothing",
	"seat":              "inspection: reads the seat (holder, epoch, generation), writes nothing",
	"rules":             "inspection: reads the rules the tick answers by and why the fleet is idle, writes nothing",
	"remind":            "store write: writes one timer to the sprint's timer record, which the tick of a RUNNING machine raises as one judgment of kind \"timer\" addressed to its actor at its due time, once (--list reads the open timers, --cancel takes one off); --dry-run writes nothing",
	"relink":            "local write: re-points what waited on the old cards to their twin in the sprint's store and answers their blocked judgments; --dry-run writes nothing",
	"friend cards":      "inspection: reads the cards held on the friend's row, their packets and briefs, writes nothing",
	"friend take":       "local write: takes the named cards back from the friend in the sprint's store; --dry-run writes nothing",
	"friend give":       "local write: clears the friend's take-back mark on the named cards in the sprint's store; --dry-run writes nothing",
	"friend level":      "local write: moves queued cards between the friends' rows in the sprint's store; --dry-run writes nothing",
	"friend health":     "local write: records the coordinator's observation of the friend in the sprint's store, or removes it with --clear; --dry-run writes nothing",
	"reader retire":     "local write: retires the named readers in the sprint's store; a read it is reading is taken back at the next tick and asked of a reader up with no card at that attempt, and it stays when none can take it; --dry-run writes nothing",
	"promoted":          "local write: records the promotion in the sprint's store; --dry-run writes nothing",
	"preflight":         "inspection: reads the briefs, the table and the repository, writes nothing",
	"selftest":          "local write: makes a fresh directory, a bare origin and a clone whose base holds a go module, runs the card's flow of the walkthrough on a twin file in it and lands one card through the tree gate; writes only in that directory, opens no store of the caller's and no network, and removes it unless --keep",
	"dashboard":         "inspection: serves the page and the pull routes, reads the sprint as where --json --cards does, writes nothing",
	"coordinator":       "delivery: moves the seat in the sprint's store, a note to the old holder on a take; --dry-run writes nothing",
	"answer":            "delivery: sends the routine judgments' state to the decision's backend (Jev), applies the verbs chosen through the sprint's verbs, and appends to --record; --dry-run asks and writes nothing",
	"selftest land":     "inspection: lands a canned card on a scratch clone with this binary, writes nothing to the sprint",
	"server switch":     "local write: runs <binary> tick --shadow against the store first (read-only, under --tick-deadline) and refuses the swap, nothing changed, when it exits non-zero, panics or misses the deadline; then switches the server binary on disk, keeping the previous binary, the shadow's plan size and time at <target>.shadow.json, and rolling back on land failure in the window; --dry-run runs the shadow tick only and switches and writes nothing",
	"merge-window open": "local write: the merge window, the merge table's properties in the sprint's store; land pauses while it is open; --dry-run checks --for and --reason and writes nothing",
	// a store write, the collect's tip read and its directory reads said
	"friend reconcile": "store write: finishes each card she reported on and returns each she abandoned, in the sprint's store; reads her inbox/QUEUE.json and outbox, writes nothing in her directory, and reads origin's tip (one git ls-remote) for each LAND it collects; --dry-run writes nothing and reads no tip",
	"lane list":        "inspection: lists every machine's lanes and holders, writes nothing",
	"lane take":        "store write: takes one lane on the machine for the worker, or joins the queue; --dry-run checks availability and writes nothing",
	"lane give":        "store write: gives the worker's lane or queue position back; --dry-run checks whether a lane is held and writes nothing",
	// the verbs whose one write is their store step, planned and not committed by --dry-run (stepdry.go)
	"accept":   "local write: moves the eligible primaries in review into the merge queue, or records the coordinator's heavy read on each named primary, in the sprint's store; --dry-run writes nothing",
	"ack":      "local write: closes the named notifications, nothing to be done, with the reason, in the sprint's store; --dry-run writes nothing",
	"ask":      "local write: asks a reader for each named primary (one more with --another, another in place with --instead) in the sprint's store; --dry-run writes nothing",
	"ci":       "local write: records the CI run's result, red or green, on each named primary in the sprint's store; --dry-run writes nothing",
	"funded":   "local write: ends the provider's rest of its funds in the sprint's store, with the reason; --dry-run writes nothing",
	"move":     "local write: moves the named cards to the stream, in line where --before, --after or --score says, in the sprint's store; --dry-run writes nothing",
	"priority": "local write: with a level, sets it on the named cards or the stream in the sprint's store; with none, prints the levels and writes nothing; --dry-run writes nothing",
	"redo":     "local write: returns, reworks and resumes the named conflicted cards in one step, or restores a dropped card to waiting at its old score (with --reason), in the sprint's store; --dry-run writes nothing",
	"resolve":  "local write: moves the waiting primaries whose needs landed in the sprint's store; --dry-run writes nothing",
	"resume":   "local write: resumes each named stopped stream in the sprint's store, with what was done; --dry-run writes nothing",
	"set":      "local write: sets the sprint's settings named in the sprint's store; --dry-run writes nothing",
	"twin":     "local write: replaces the card by its twin in the sprint's store (a merging card is returned first, then twinned; its dry run plans the return alone); --dry-run writes nothing",

	// card base (the base's 5a9340a155) writes through one store step too; its --dry-run is stepdry.go's
	"card base": "local write: re-points the merging card's BASE to the branch in the sprint's store, after asking origin (a read) whether it holds that branch; --dry-run asks origin the same and plans the step, and --dry-run writes nothing",

	// stop-return (the base's #5499) writes through one store step too; its --dry-run is stepdry.go's
	"stop-return": "local write: returns the named stopped cards to the owner row that acknowledges the cancellation, keeping their placement and attempt, in the sprint's store; --dry-run writes nothing",

	// seat push and seat pong (the base's #5477) write the seat's push record; each --dry-run is its own
	"seat push": "store write: with --harness and --target, records the seat's push target (harness, target, session, adapter) in the sprint's store; with --sent, records the push loop's delivery of a check or its failure; with neither, prints the record and whether the seat is live and writes nothing; --dry-run checks the same and writes nothing",
	"seat pong": "store write: proves the seat live by the nonce of the last check delivered, recording the proof in the sprint's store; --dry-run checks the nonce against the record and writes nothing",

	// verbs that read and write nothing
	"goal show": "inspection: reads the goals in the sprint's store, writes nothing",
	"fsck seat": "inspection: reads the coordinator key, the seat record and the server's actor from the sprint's store and the sprint row's coordinator from nova-config's store, writes nothing",
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
	"Deadline: finish within <n> minutes; the judgment of a card that runs past it is the coordinator's, so report what you have with the verdict not-done rather than push past it.\n" +
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
	"STEP 4. Run the gate: go test -count=1 -timeout 600s ./internal/<package>/ ./internal/ci/ and read the last line of each. When a test fails, name its file and say whether that file was changed by your work (yours) or is unchanged (already red at BASE: run the same test on the unchanged base to say so), and report that line first.\n" +
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
	case "sentinel set":
		return sentinelSetWords + "\n"
	case "friend beat", "friend down", "friend up", "friend health":
		return friendVerbWords(name)
	case "friend take":
		return friendTakeWords
	case "friend give":
		return friendGiveWords
	case "friend cards":
		return friendCardsWords
	case "friend level":
		return friendLevelWords
	case "add", "brief":
		return cardHelpWords
	case "hold", "unhold":
		return holdWords()
	case "fleet hold", "fleet unhold", "friend hold", "friend unhold":
		return kindHoldWords(name)
	case "fleet down", "reader away", "reader up":
		return oldHoldWords(name)
	default:
		return ""
	}
}
