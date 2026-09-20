package swarm

import (
	"regexp"
	"strings"
)

// THE RULE IS WHAT THE CARD WALKS, NOT WHAT THE CARD SAYS (issues #1494, #1527).
//
// Practice 25 exists because the wall refuses a path above the job: a card that tells its
// worker to put the worktree, the scratch or the notes outside the working directory buys
// a job that dies on its first write. `no-parent-path` is that rule made mechanical.
//
// It was written as `strings.Contains(line, "../")` over every line of the card, and that
// is a check on TEXT where the rule is about a PATH. Measured on the 2026-09-19 shift,
// across tools10, tools11, tools12, tools13 and work-swarm, every `no-parent-path` finding
// on every card was false, and all of them were one of four shapes:
//
//  1. A relative path a card QUOTES from the repository's own source, so the worker copies
//     the convention: `in the form `"../../AGENTS.md"` (that file, line 34)`. The path is
//     inside the repo and is never walked by the job.
//  2. A markdown link TARGET, quoted out of the document the card repairs:
//     `[pit-stop ledger item 20](../reports/pitstop-tests-2026-09-17.md)`. A link target is
//     resolved by a reader against the document, never by a worker against the job root.
//  3. Prose about the token itself: `a target ... gains a `../` prefix`, and
//     `one `../` dropped again in 08-rate-and-convergence.md:41`. The card is teaching the
//     two characters, not walking them.
//  4. An ELLIPSIS. `ok .../internal/pulse 1.813s` is what `go test` prints and what a card
//     pastes; its last two dots and the slash read as a parent path to a substring search.
//
// A lint that is wrong on every card is a lint a manager learns to launch past, and then
// the true finding on the next card is read the same way. Five managers wrote exactly that
// sentence in their PROGRESS file on one day.
//
// SO THE RULE READS TWO THINGS. First, whatever else is on the line, a parent path given to
// a command that WALKS it -- enters, creates, copies, moves, removes, or writes at that path
// -- is a finding, inside a fenced block as much as outside one, because a card's commands
// live in fences and that is precisely where the hurt was written. Second, everything else
// is a finding only where the card is instructing rather than quoting: not inside a fenced
// block, not inside a backtick span, not a markdown link target, and never an ellipsis.
//
// What this gives up, said plainly: a card that spells a bare `../out` as an argument to a
// command not in the list below, inside a fence, is not caught. That is the price of not
// crying on every card, and it is the cheaper half.

// cardWalkParentRE is a parent path handed to a command that walks it. The command word is
// anchored so `cp` in `cpu` and `rm` in `confirm` are not commands.
var cardWalkParentRE = regexp.MustCompile(
	`(?:^|[^A-Za-z0-9_./-])(?:cd|pushd|mkdir|rmdir|cp|mv|rm|ln|touch|tar|unzip|rsync|git[ \t]+-C)[ \t]+(?:-[A-Za-z-]+[ \t]+)*["']?\.\./`)

// cardWriteParentRE is a parent path named as a destination: a shell redirect, or the value
// of a long flag such as `--root ../queue` or `--out=../notes`.
var cardWriteParentRE = regexp.MustCompile(`(?:>>?[ \t]*|--[a-z][a-z-]*[= \t])["']?\.\./`)

// cardLinkTargetRE is a markdown link or image target, `](...)`.
var cardLinkTargetRE = regexp.MustCompile(`\]\([^)]*\)`)

// cardEllipsisRE is a run of three or more dots. `../..` never reaches three.
var cardEllipsisRE = regexp.MustCompile(`\.{3,}`)

// cardFenceRE is a fenced-code-block delimiter: three or more backticks or tildes at the
// start of the line, with an optional info string after them.
var cardFenceRE = regexp.MustCompile("^[ \t]*(?:```+|~~~+)")

// CardParentPaths returns the 1-based numbers of the card lines that reach above the job,
// in line order, at most one per line. It reads the lines it is handed and nothing else.
func CardParentPaths(lines []string) []int {
	var out []int
	inFence := false
	for i, l := range lines {
		if cardFenceRE.MatchString(l) {
			inFence = !inFence
			continue
		}
		// A walk is a walk wherever it is written, fence or no fence, backticks or none.
		if cardWalkParentRE.MatchString(l) || cardWriteParentRE.MatchString(l) {
			out = append(out, i+1)
			continue
		}
		if inFence {
			continue
		}
		if strings.Contains(cardInstructionText(l), "../") {
			out = append(out, i+1)
		}
	}
	return out
}

// cardInstructionText is the part of one card line that instructs the worker: the line with
// its quotations blanked out. Blanking rather than deleting keeps every byte offset, so a
// caller that wants a column later still has one.
func cardInstructionText(l string) string {
	l = cardEllipsisRE.ReplaceAllStringFunc(l, blank)
	l = cardLinkTargetRE.ReplaceAllStringFunc(l, blank)
	return blankCodeSpans(l)
}

func blank(s string) string { return strings.Repeat(" ", len(s)) }

// blankCodeSpans blanks the inside of every inline backtick span. An unclosed backtick
// opens a span that runs to the end of the line, which is how a reader reads it too.
func blankCodeSpans(l string) string {
	b := []byte(l)
	open := -1
	for i := 0; i < len(b); i++ {
		if b[i] != '`' {
			continue
		}
		if open < 0 {
			open = i
			continue
		}
		for j := open + 1; j < i; j++ {
			b[j] = ' '
		}
		open = -1
	}
	if open >= 0 {
		for j := open + 1; j < len(b); j++ {
			b[j] = ' '
		}
	}
	return string(b)
}

// CardParentPathWanted is what `no-parent-path` wants, in one line, for the remedy and for
// any tool that prints the rule rather than applies it.
const CardParentPathWanted = "the wall refuses every path ABOVE THE JOB: keep the worktree, the scratch and the notes under the working directory rather than reaching through `../`. The rule is what the card WALKS -- a `cd`, a `mkdir`, a `cp`, a redirect, a `--root` -- so a `../` the card merely quotes (a fenced block, a backtick span, a markdown link target, a `go test` ellipsis) is not this drift (practice 25)"
