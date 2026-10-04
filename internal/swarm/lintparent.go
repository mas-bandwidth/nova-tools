package swarm

import (
	"regexp"
	"strings"
)

// CardParentPaths enforces the boundary around the job directory: commands must not walk a
// parent path that reaches the worktree, scratch files, or notes from outside the job.
//
// The check reads card text, so it reports parent paths when they are command arguments or
// destinations, including inside fenced commands. It ignores paths quoted as prose, inline
// code, markdown link targets, or command-output ellipses because those paths are not walked.

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
const CardParentPathWanted = "the wall refuses every path ABOVE THE JOB: keep the worktree, the scratch and the notes under the working directory rather than reaching through `../`. The rule is what the card WALKS -- a `cd`, a `mkdir`, a `cp`, a redirect, a `--root` -- so a `../` the card merely quotes (a fenced block, a backtick span, a markdown link target, a `go test` ellipsis) is not this drift"
