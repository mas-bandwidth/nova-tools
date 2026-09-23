package main

// `cut --kind X --from <table> --out <dir> --queue <dir>` — the typed cut's small loop
// (#2021). The pin is that "specs must enumerate their own work so cutting cards is a
// loop, not a manager", and the loop is exactly this: read one row per line, dedupe it
// against what the queue already has, and cut one card per surviving row.
//
// One row shape per kind:
//
//	fix   <repo><tab><issue><tab><title>[<tab><body-file><tab><prior>]
//	read  <repo><tab><pr><tab><head><tab><title>
//	guard <repo><tab><head>
//
// Those three are the issue's families (fix per triaged issue, read per unreviewed PR,
// guard per dev commit); the other kinds have no one-record-per-row shape to read.
//
// Two dedupes, reported apart (why=carded, why=origin):
//
//   - carded: the marker is the part of pulse.CutKind's own line 1 that does not change
//     between cuts: <repo-short> #<issue> for fix; PR<pr> at <head> for read; guard of
//     <repo-short> at <head> for guard. A row that hits the marker in any of
//     <queue>/{pending,launched,done,failed} is skipped.
//   - origin: with --dir <clone>, the clone's origin is asked once per run,
//     `git ls-remote --heads origin` (the question internal/pulse/validated.go's
//     check=branch asks), and a fix row whose issue already has a branch there -- the
//     open PR a hand-cut wave re-cuts -- is skipped. The issue matches a branch that
//     carries <repo-short>-<issue> (the harvest's fix3-<repo-short>-<issue>) or
//     issue-<issue> (the validated cut's rowan/issue-<n>-<slug>) between word
//     boundaries, so a branch for 430 never stands for 43. A read row names an open PR
//     by definition and a guard row a dev commit, so origin holds nothing new for
//     either; they dedupe on the queue alone.

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/pulse"
)

// fromKinds lists --kind <kind> --from combinations the verb accepts. The verb refuses
// any other kind: giving the kind decides the row shape, and a row of the wrong shape
// is a refusal that names what was wrong.
var fromKinds = map[string]bool{"fix": true, "read": true, "guard": true}

// cutKindFrom reads <fromPath> as a kind-specific TSV, dedupes each row against the queue
// dirs, and calls pulse.CutKind for the surviving rows. One summary line is printed:
// CUT FROM kind=<kind> rows=<n> cut=<k> skipped=<m> carded=<a> origin=<b>. A row's CUT CARD
// line is reproduced on stdout; a skipped row prints CUT FROM SKIPPED kind=<kind>
// why=carded|origin <key> mark=<mark>|branch=<branch> before the summary, so a coordinator
// reading the log can see what was skipped and why. With --dir, the origin is read once
// before any row; an origin that does not answer is a refusal, never a quiet queue-only run.
func cutKindFrom(kind, fromPath, outDir, queueDir, dir string, stdout, stderr io.Writer) int {
	if !fromKinds[kind] {
		fmt.Fprintf(stderr,
			"CUT FROM REFUSED: --kind %s is not a generator kind (--from accepts fix|read|guard; every row's shape is the kind's own)\n",
			oneline.Field(kind))
		return 2
	}
	raw, err := os.ReadFile(fromPath)
	if err != nil {
		fmt.Fprintf(stderr,
			"CUT FROM REFUSED: --from %s: %s (pass a readable kind-specific table; fix: repo<tab>issue<tab>title, read: repo<tab>pr<tab>head<tab>title, guard: repo<tab>head)\n",
			oneline.Field(fromPath), oneline.Err(err))
		return 2
	}
	if strings.TrimSpace(outDir) == "" {
		fmt.Fprintf(stderr, "CUT FROM REFUSED: --out is required (pass the directory the cards go into, usually <queue>/pending)\n")
		return 2
	}
	if strings.TrimSpace(queueDir) == "" {
		fmt.Fprintf(stderr, "CUT FROM REFUSED: --queue is required (pass the queue directory holding pending/launched/done/failed)\n")
		return 2
	}
	var branches []string
	if strings.TrimSpace(dir) != "" {
		heads, err := originBranches(dir)
		if err != nil {
			fmt.Fprintf(stderr, "CUT FROM REFUSED: --dir %s: %s (pass a clone whose origin answers, or drop --dir to dedupe on the queue alone)\n",
				oneline.Field(dir), oneline.Err(err))
			return 2
		}
		branches = heads
	}
	rows := parseFromRows(string(raw))
	cut, carded, onOrigin := 0, 0, 0
	for _, r := range rows {
		line := r.line
		mark, in, ok := parseKindRow(kind, r.fields)
		if !ok {
			fmt.Fprintf(stderr, "CUT FROM REFUSED kind=%s row=%s\n", oneline.Field(kind), oneline.Field(line))
			return 2
		}
		if isCarded(queueDir, mark) {
			carded++
			fmt.Fprintf(stdout, "CUT FROM SKIPPED kind=%s why=carded %s mark=%s\n", oneline.Field(kind), rowKey(in), oneline.Field(mark))
			continue
		}
		if b := branchForRow(branches, in); b != "" {
			onOrigin++
			fmt.Fprintf(stdout, "CUT FROM SKIPPED kind=%s why=origin %s branch=%s\n", oneline.Field(kind), rowKey(in), oneline.Field(b))
			continue
		}
		in.Out = outDir
		in.Queue = queueDir
		var cardOut, cardErr bytes.Buffer
		in.Stdout, in.Stderr = &cardOut, &cardErr
		if code := pulse.CutKind(in); code != 0 {
			fmt.Fprintf(stderr, "CUT FROM REFUSED kind=%s row=%s: %s",
				oneline.Field(kind), oneline.Field(line), cardErr.String())
			return 2
		}
		cut++
		// pulse.CutKind ends its one line with \n; reproduce it verbatim so the
		// coordinator's tools see one line per cut, the same as a hand-run cut.
		fmt.Fprintf(stdout, "%s", cardOut.String())
	}
	skipped := carded + onOrigin
	fmt.Fprintf(stdout, "CUT FROM kind=%s rows=%d cut=%d skipped=%d carded=%d origin=%d\n",
		oneline.Field(kind), len(rows), cut, skipped, carded, onOrigin)
	if skipped > 0 {
		return 1
	}
	return 0
}

// fromRow holds one parsed line of --from: its raw text and its tab-split fields, both
// carried so a refusal can echo the line the caller wrote, not a normalised version.
type fromRow struct {
	line   string
	fields []string
}

// parseFromRows splits the file on \n and drops blank lines. A header row a caller
// chooses to put at the top of their table is fine if every column lower-cases to
// (repo, pr, issue, head, title, body-file, prior) or is empty: the same allowlist the
// validated cut uses for its header row (validated.go isHeaderRow).
func parseFromRows(body string) []fromRow {
	var out []fromRow
	for _, ln := range strings.Split(body, "\n") {
		if strings.TrimSpace(ln) == "" {
			continue
		}
		fields := strings.Split(ln, "\t")
		if isHeaderFields(fields) {
			continue
		}
		out = append(out, fromRow{line: ln, fields: fields})
	}
	return out
}

func isHeaderFields(fields []string) bool {
	for _, f := range fields {
		switch strings.ToLower(strings.TrimSpace(f)) {
		case "", "repo", "pr", "issue", "head", "title", "body-file", "body_file", "prior", "kind":
		default:
			return false
		}
	}
	return true
}

// parseKindRow turns one row's fields into a dedupe marker and a pulse.CutKindInput. A
// row whose mark or fields cannot be derived is rejected: the verb must not guess; a
// "missing part" is the row's fault, not the verb's.
func parseKindRow(kind string, f []string) (string, pulse.CutKindInput, bool) {
	at := func(i int) string {
		if i < len(f) {
			return strings.TrimSpace(f[i])
		}
		return ""
	}
	repo := at(0)
	short := repoShortName(repo)
	if short == "" {
		return "", pulse.CutKindInput{}, false
	}
	in := pulse.CutKindInput{Kind: kind, Repo: repo}
	switch kind {
	case "fix":
		n, err := strconv.Atoi(at(1))
		if err != nil || n < 1 {
			return "", pulse.CutKindInput{}, false
		}
		title := at(2)
		if title == "" {
			return "", pulse.CutKindInput{}, false
		}
		in.Issue = n
		in.Title = title
		in.BodyFile = at(3)
		in.Prior = at(4)
		return fmt.Sprintf("%s #%d ", short, n), in, true
	case "read":
		n, err := strconv.Atoi(at(1))
		if err != nil || n < 1 {
			return "", pulse.CutKindInput{}, false
		}
		head := at(2)
		title := at(3)
		if head == "" || title == "" {
			return "", pulse.CutKindInput{}, false
		}
		in.PR = n
		in.Head = head
		in.Title = title
		return fmt.Sprintf("PR%d at %s", n, head), in, true
	case "guard":
		head := at(1)
		if head == "" {
			return "", pulse.CutKindInput{}, false
		}
		in.Head = head
		return fmt.Sprintf("guard of %s at %s", short, head), in, true
	}
	return "", pulse.CutKindInput{}, false
}

// repoShortName returns owner/name as line 1 says it: the name alone. A repo without a
// slash, or a slash followed by nothing, is rejected: the verb refuses to guess what
// line 1 will say.
func repoShortName(repo string) string {
	if idx := strings.Index(repo, "/"); idx > 0 && idx < len(repo)-1 {
		return repo[idx+1:]
	}
	return ""
}

// isCarded says whether any card in <queue>/{pending,launched,done,failed} carries `mark`
// as a substring. The compare is by TEXT, never by sha12: a queue's most-recent cut
// carries the sha12, and the older ones do not, and the marker the cut table owns is
// independent of either.
func isCarded(queue, mark string) bool {
	for _, sub := range []string{"pending", "launched", "done", "failed"} {
		matches, err := filepath.Glob(filepath.Join(queue, sub, "card-*.md"))
		if err != nil {
			continue
		}
		for _, p := range matches {
			raw, err := os.ReadFile(p)
			if err != nil {
				continue
			}
			if strings.Contains(string(raw), mark) {
				return true
			}
		}
	}
	return false
}

// originBranches is the clone's origin's branch list, read once per run: one
// `git -C <dir> ls-remote --heads origin`, each refs/heads/<name> line reduced to <name>.
func originBranches(dir string) ([]string, error) {
	out, err := exec.Command("git", "-C", dir, "ls-remote", "--heads", "origin").Output()
	if err != nil {
		return nil, fmt.Errorf("git ls-remote --heads origin: %w", err)
	}
	var names []string
	for _, ln := range strings.Split(string(out), "\n") {
		f := strings.Fields(ln)
		if len(f) < 2 {
			continue
		}
		if name, ok := strings.CutPrefix(f[1], "refs/heads/"); ok {
			names = append(names, name)
		}
	}
	return names, nil
}

// branchForRow names the first origin branch that already carries this row's work, or ""
// when none does. Only a fix row is matched (see the header: read and guard rows have no
// branch of their own to find).
func branchForRow(branches []string, in pulse.CutKindInput) string {
	if in.Kind != "fix" || in.Issue < 1 || len(branches) == 0 {
		return ""
	}
	re := regexp.MustCompile(`(^|[/_-])(` + regexp.QuoteMeta(repoShortName(in.Repo)) + `|issue)-` +
		strconv.Itoa(in.Issue) + `($|[/_-])`)
	for _, b := range branches {
		if re.MatchString(b) {
			return b
		}
	}
	return ""
}

// rowKey is the row's own identity on a SKIPPED line: the field a reader greps for.
func rowKey(in pulse.CutKindInput) string {
	switch in.Kind {
	case "fix":
		return fmt.Sprintf("issue=%d", in.Issue)
	case "read":
		return fmt.Sprintf("pr=%d head=%s", in.PR, oneline.Field(in.Head))
	}
	return "head=" + oneline.Field(in.Head)
}
