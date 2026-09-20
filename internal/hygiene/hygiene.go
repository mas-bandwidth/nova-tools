// Package hygiene is the one place that answers "is this diff clean", for the accept
// gate, for the merge lane and for a hand.
//
// SPEC-TOOLWORK.md §3 (PR #1637), issue #1647. Four mechanical questions, none of
// which reads prose and none of which needs a model:
//
//	identity     every commit in the range is the pool's own, and none is a merge
//	out-of-path  every changed path matches one of the card's declared globs
//	stray-file   nothing was added that does not belong in a repository
//	secret       no added line has the SHAPE of a key
//
// One implementation and three callers -- `nova-pulse accept` at harvest, `nova-merge
// batch` on every member, and `nova-check hygiene` for a person before they ask for a
// read -- so the lane and the harvest cannot disagree about what clean means. A second
// implementation is a second definition, and the day they drift is the day a card
// passes one and fails the other with nobody able to say which is right.
//
// What it does not do: it never opens RESULT.md or any other prose the worker wrote,
// it never prints a matched secret, and it never decides anything. It returns findings;
// the caller decides what a finding costs.
package hygiene

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// Identity is one line at a keyboard: the name and email a commit in this range must
// carry, as the pool's identity.tsv (or the lane's identities.tsv) spells it.
type Identity struct {
	Name  string
	Email string
}

// Finding is one thing wrong, in the form the gate's REJECT line prints: a token, and
// where. Why is one line for a person and never carries matched secret text.
type Finding struct {
	Token string // identity | out-of-path | stray-file | secret
	At    string // <sha12>, <path>, or <path>:<line>
	Why   string
}

func (f Finding) String() string {
	return fmt.Sprintf("%s at=%s: %s", f.Token, f.At, f.Why)
}

// Options is one call. The spec's signature is Check(repo, base, head, paths,
// identity); it is a struct here because two of the five are sets whose absence means
// something specific, and a positional nil says neither which nor why.
type Options struct {
	Repo string
	Base string
	Head string

	// Paths is the card's PATHS: globs. ABSENT (nil or empty) means the diff is not
	// bounded -- a friend's batch member has no card and no declared paths -- and
	// out-of-path is skipped and said to be skipped (`paths=-`), never silently
	// passed.
	Paths []string

	// Identities is a set: at harvest the pool's one row, at batch every friend who
	// commits to this repository. An empty set is a refusal, not a pass: a range
	// checked against nobody would admit anybody.
	Identities []Identity

	// Kind is the card's KIND:, used only to honour an allowlisted stray exception
	// that names the kind it is for. Empty means no exception applies.
	Kind string
}

// Check runs the four checks over base..head and returns everything wrong, in a stable
// order. An error is a could-not-run (a bad ref, a repo that is not a working copy) and
// is never a finding: a check that could not run has found nothing, and reporting it as
// clean is the one answer that must not be possible.
func Check(ctx context.Context, o Options) ([]Finding, error) {
	rules, shapes, err := load()
	if err != nil {
		return nil, err
	}
	repo, err := filepath.Abs(o.Repo)
	if err != nil {
		return nil, fmt.Errorf("repo %s: %v", o.Repo, err)
	}
	if _, err := os.Stat(filepath.Join(repo, ".git")); err != nil {
		return nil, fmt.Errorf("repo %s is not a git working copy", o.Repo)
	}
	// An empty identity set would admit anybody, so it is a could-not-run and never a
	// clean answer: the one thing a hygiene check must not be able to say is "nothing
	// wrong" because it had nothing to compare against.
	if len(o.Identities) == 0 {
		return nil, errors.New("no identity to check against: a range checked against nobody would admit anybody")
	}
	if len(o.Paths) > 0 {
		if err := ValidatePaths(o.Paths); err != nil {
			return nil, err
		}
	}
	base, err := gitLine(ctx, repo, "rev-parse", o.Base+"^{commit}")
	if err != nil {
		return nil, fmt.Errorf("base %s names no commit in this repo", o.Base)
	}
	head, err := gitLine(ctx, repo, "rev-parse", o.Head+"^{commit}")
	if err != nil {
		return nil, fmt.Errorf("head %s names no commit in this repo", o.Head)
	}
	if mb, err := gitLine(ctx, repo, "merge-base", base, head); err == nil && mb != "" {
		base = mb
	}

	var findings []Finding
	id, err := checkIdentity(ctx, repo, base, head, o.Identities)
	if err != nil {
		return nil, err
	}
	findings = append(findings, id...)

	raw, err := rawDiff(ctx, repo, base, head)
	if err != nil {
		return nil, err
	}
	if len(o.Paths) > 0 {
		findings = append(findings, checkPaths(raw, o.Paths)...)
	}
	stray, err := checkStray(ctx, repo, raw, rules, o.Kind)
	if err != nil {
		return nil, err
	}
	findings = append(findings, stray...)

	// The added lines answer two questions in one pass: the key shapes and the
	// conflict markers.
	added, err := checkAddedLines(ctx, repo, base, head, shapes)
	if err != nil {
		return nil, err
	}
	findings = append(findings, added...)

	sort.SliceStable(findings, func(i, j int) bool {
		if findings[i].Token != findings[j].Token {
			return findings[i].Token < findings[j].Token
		}
		return findings[i].At < findings[j].At
	})
	return findings, nil
}

// checkIdentity: every commit in the range carries one of the given names and emails as
// BOTH author and committer, and none is a merge.
//
// Both halves matter. An author is what a commit claims about who wrote it and a
// committer is what the machine that made it recorded; a card whose author is right and
// whose committer is a bench's leftover git config has told the truth about one and
// nothing about the other. A merge commit is refused because its diff against the base
// carries work nobody in this range did, and "the diff stays inside the card's named
// files" stops meaning anything the moment the range can absorb somebody else's branch.
func checkIdentity(ctx context.Context, repo, base, head string, ids []Identity) ([]Finding, error) {
	const sep = "\x1f"
	// The plain form lists merges, which is what this check wants: a merge is a
	// finding here, not something to hide. (`--no-merges=false` is not a git option --
	// git 2.55 exits fatal on it -- so the form that took it was never the one that
	// ran.)
	out, err := gitOut(ctx, repo, "log", "--format=%H"+sep+"%an"+sep+"%ae"+sep+"%cn"+sep+"%ce"+sep+"%P", base+".."+head)
	if err != nil {
		return nil, fmt.Errorf("could not read the commits between %s and %s: %v", short(base), short(head), err)
	}
	return identityFindings(out, ids)
}

// identityFindings reads the rows checkIdentity asked for. It is a function of its own
// so the row grammar can be tested directly, on a row no git would be asked to write.
func identityFindings(out string, ids []Identity) ([]Finding, error) {
	const sep = "\x1f"
	allowed := map[string]bool{}
	for _, id := range ids {
		allowed[id.Name+sep+id.Email] = true
	}
	var findings []Finding
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		f := strings.Split(line, sep)
		if len(f) < 6 {
			// A row this cannot read is a COMMIT, and skipping it lets that commit
			// through the identity check unchecked. There is no safe way to read
			// half a row, so the whole Check stops instead.
			return nil, fmt.Errorf("could not read a commit row of %d fields, want 6: the range holds a commit this check could not read", len(f))
		}
		sha, an, ae, cn, ce, parents := f[0], f[1], f[2], f[3], f[4], f[5]
		if len(strings.Fields(parents)) > 1 {
			findings = append(findings, Finding{Token: "identity", At: short(sha),
				Why: "this is a merge commit; a card's range carries only its own work"})
			continue
		}
		switch {
		case !allowed[an+sep+ae] && !allowed[cn+sep+ce]:
			findings = append(findings, Finding{Token: "identity", At: short(sha),
				Why: fmt.Sprintf("author %s and committer %s are not the pool's identity", oneline.Field(ae), oneline.Field(ce))})
		case !allowed[an+sep+ae]:
			findings = append(findings, Finding{Token: "identity", At: short(sha),
				Why: fmt.Sprintf("author %s is not the pool's identity", oneline.Field(ae))})
		case !allowed[cn+sep+ce]:
			findings = append(findings, Finding{Token: "identity", At: short(sha),
				Why: fmt.Sprintf("committer %s is not the pool's identity", oneline.Field(ce))})
		}
	}
	return findings, nil
}

// entry is one row of `git diff --raw`: the two modes, the new blob, the status and the
// path.
type entry struct {
	oldMode string
	newMode string
	newBlob string
	status  string // A, D, M, T
	path    string
}

// rawDiff reads the range with renames OFF, which is what makes a rename count on BOTH
// sides: with `--find-renames` git reports one `R` row naming the destination and the
// source is never listed, so a card that moved a file OUT of its declared paths would
// be judged only on where it landed. Off, the same move is a `D` and an `A`, and both
// are checked.
//
// `--no-abbrev` because the blob id on a `--raw` row is what `git cat-file -s` is then
// asked to size, and an abbreviation is ambiguous sooner or later -- at which point the
// whole Check fails over a range that was fine.
func rawDiff(ctx context.Context, repo, base, head string) ([]entry, error) {
	out, err := gitOut(ctx, repo, "diff", "--no-ext-diff", "--no-renames", "--no-abbrev", "--raw", "-z", base, head)
	if err != nil {
		return nil, fmt.Errorf("could not read the change between %s and %s: %v", short(base), short(head), err)
	}
	var entries []entry
	fields := strings.Split(out, "\x00")
	for i := 0; i < len(fields); i++ {
		meta := fields[i]
		if !strings.HasPrefix(meta, ":") {
			continue
		}
		parts := strings.Fields(meta[1:])
		if len(parts) < 5 || i+1 >= len(fields) {
			continue
		}
		i++
		entries = append(entries, entry{
			oldMode: parts[0], newMode: parts[1], newBlob: parts[3],
			status: parts[4], path: fields[i],
		})
	}
	return entries, nil
}

// checkPaths: every changed path matches one of the card's globs, both sides of a
// rename included.
func checkPaths(entries []entry, globs []string) []Finding {
	var findings []Finding
	for _, e := range entries {
		matched := false
		for _, g := range globs {
			if matchGlob(g, e.path) {
				matched = true
				break
			}
		}
		if !matched {
			findings = append(findings, Finding{Token: "out-of-path", At: e.path,
				Why: "this path matches none of the card's declared PATHS:"})
		}
	}
	return findings
}

// oneMiB is the size a single added file may not exceed. A card's work is source; a
// megabyte of anything in a diff is something that was generated, downloaded or
// recorded, and each of those wants a reader who knows what it is.
const oneMiB = 1024 * 1024

// checkStray: nothing was added that does not belong in a repository, nothing carries a
// mode or a type git should not be asked to keep, and no changed file holds a conflict
// marker.
func checkStray(ctx context.Context, repo string, entries []entry, rules []strayRule, kind string) ([]Finding, error) {
	var findings []Finding
	for _, e := range entries {
		if e.status == "D" {
			continue
		}
		if e.status == "A" {
			if pat, ok := matchesStray(rules, kind, e.path); ok {
				findings = append(findings, Finding{Token: "stray-file", At: e.path,
					Why: fmt.Sprintf("an added file matching the stray list's %s", oneline.Field(pat))})
				continue
			}
		}
		// A type or a mode the CARD made: a file that was already odd at the base and
		// that this card merely edited is not this card's finding.
		if e.newMode != e.oldMode || e.status == "A" {
			if f, bad := modeFinding(e); bad {
				findings = append(findings, f)
				continue
			}
		}
		// §3 rule 5 is about what a card ADDED. A file that was already over the
		// limit at the base and that this card merely edited is not this card's
		// finding, and charging it would make every later card in that repository
		// unfixable by anybody.
		if e.status == "A" && (e.newMode == "100644" || e.newMode == "100755") {
			size, err := blobSize(ctx, repo, e.newBlob)
			if err != nil {
				return nil, err
			}
			if size > oneMiB {
				findings = append(findings, Finding{Token: "stray-file", At: e.path,
					Why: fmt.Sprintf("%d bytes, over the 1 MiB a single added file may carry", size)})
			}
		}
	}
	return findings, nil
}

func blobSize(ctx context.Context, repo, blob string) (int64, error) {
	if blob == "" || strings.Trim(blob, "0") == "" {
		return 0, nil
	}
	out, err := gitLine(ctx, repo, "cat-file", "-s", blob)
	if err != nil {
		return 0, fmt.Errorf("could not size the blob %s: %v", short(blob), err)
	}
	n, err := strconv.ParseInt(strings.TrimSpace(out), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("could not read the size of blob %s", short(blob))
	}
	return n, nil
}

var hunkHeader = regexp.MustCompile(`^@@ -\d+(?:,\d+)? \+(\d+)(?:,\d+)? @@`)

// conflictMarker is git's own spelling of one: exactly seven of the character, then a
// space or the end of the line. Eight is not a marker and neither is `"<<<<<<<"` inside
// a string, which is why this is anchored rather than a substring search.
var conflictMarker = regexp.MustCompile(`^(<{7}|={7}|>{7})( |$)`)

// checkAddedLines is the one pass over what this range ADDED, and it answers two of the
// four questions: does any added line have the SHAPE of a key, and did anybody leave a
// conflict marker behind.
//
// A range is judged by what it added: a shape or a marker already in the base is
// somebody else's finding, and charging it here would make every card in a repository
// that once carried one unfixable by anybody.
//
// The matched text is not printed, ever, and that is not politeness. A finding travels:
// into a gate's stdout, into a PR body, into a harvest log, into whatever a coordinator
// pastes into a chat. A finding that quotes the key has copied the key into every one
// of those places, and the check meant to contain a leak has published it.
//
// The markers used to be `git diff --check`'s answer, and that was a check the SUBJECT
// could switch off. `--check` honours a `-diff` attribute whatever `--text` says, and
// the attribute reaches git from three places: a committed `.gitattributes`,
// `.git/info/attributes`, and a local `core.attributesFile`. `--attr-source` covers the
// first and is git 2.42 and later; it covers neither of the others on any version, and
// the gate's worktree shares the job clone's `.git` (§1 rule 3), so both are the
// worker's to write. Reading the lines this function already holds has no version to
// depend on, no skip, and nothing for the subject to turn off: `--text` is what makes
// the lines appear, and `--text` is not optional here.
//
// card-16 left `<<<<<<< HEAD` inside a fenced block and it reached a pull request,
// because nothing between the worker and the forge ever looked at the bytes.
func checkAddedLines(ctx context.Context, repo, base, head string, shapes []keyShape) ([]Finding, error) {
	out, err := gitOut(ctx, repo, append(append([]string(nil), diffOpts...), "--unified=0", base, head)...)
	if err != nil {
		return nil, fmt.Errorf("could not read the added lines between %s and %s: %v", short(base), short(head), err)
	}
	var findings []Finding
	file, lineNo := "", 0
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		switch {
		case strings.HasPrefix(line, "+++ "):
			file = diffPath(strings.TrimPrefix(line, "+++ "))
			continue
		case strings.HasPrefix(line, "--- "), strings.HasPrefix(line, "diff "), strings.HasPrefix(line, "index "):
			continue
		}
		if m := hunkHeader.FindStringSubmatch(line); m != nil {
			n, _ := strconv.Atoi(m[1])
			lineNo = n
			continue
		}
		if !strings.HasPrefix(line, "+") || file == "" {
			continue
		}
		added := line[1:]
		at := fmt.Sprintf("%s:%d", file, lineNo)
		if conflictMarker.MatchString(added) {
			findings = append(findings, Finding{Token: "stray-file", At: at,
				Why: "a leftover conflict marker in a changed file"})
		}
		for _, s := range shapes {
			if s.re.MatchString(added) {
				findings = append(findings, Finding{Token: "secret", At: at,
					Why: fmt.Sprintf("an added line with the shape of a %s; the text is not printed", oneline.Field(s.name))})
				break
			}
		}
		lineNo++
	}
	return findings, nil
}

func short(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

// configOpts is what this package imposes on every git it runs, over and above the
// blanked global and system config in gitCmd.
//
// `--no-replace-objects`: a `git replace <head> <base>` writes `refs/replace/<head>`
// into the job clone's own `.git` -- a ref, not a commit, so `out-of-path` and
// `stray-file` never see it -- and every later object read follows it, so the whole
// range is shown as the base and the four checks report clean over work that still
// carries a key. The replacement is the subject's vote on what git shows this check,
// which SPEC.md says the subject does not get.
//
// `core.quotePath=false`: with it on -- git's default -- one non-ASCII byte in a name
// makes git quote the whole header, `+++ "b/sign/k\303\251y.go"`, and the parser read
// that as a file it could not name and skipped every added line in it. diffPath
// unquotes what is left, because a name holding a quote or a newline is quoted
// whatever this setting says.
var configOpts = []string{"--no-replace-objects", "-c", "core.quotePath=false"}

// diffOpts is the shape of every textual diff this package reads, and every one of
// them is here because the repository being CHECKED controls what git prints.
//
// gitCmd already blanks the global and system config -- a bench's configuration is not
// evidence -- but a job clone's own `.git/config`, its own `.git/info/attributes` and
// its own committed `.gitattributes` all belong to the worker, and every one of them
// changes the output:
//
//	--src-prefix/--dst-prefix  `diff.noprefix` in the subject repo drops the `a/` and
//	                           `b/` and the parser never learns a file name;
//	                           `diff.mnemonicPrefix` and `diff.dstPrefix` rename them.
//	--text                     a `-diff` attribute -- from any of the three places one
//	                           can be written, and a NUL byte needs no attribute at
//	                           all -- makes git print "Binary files differ" instead of
//	                           the lines, and nothing then reaches the shapes or the
//	                           marker scan. This is the option the whole of
//	                           checkAddedLines rests on.
//	--no-textconv              a `diff` driver in the same file would run a program of
//	                           the worker's choosing and diff ITS output.
//
// A check whose subject can choose what it is shown is not a check. There is
// deliberately no `--attr-source` here: it reads only the committed `.gitattributes`,
// it is git 2.42 and later, and the one check that needed it -- `git diff --check` --
// is gone, because `--check` honours the attribute whatever `--text` says and the
// markers are now read off these same lines.
var diffOpts = []string{"diff", "--no-ext-diff", "--no-textconv", "--no-renames", "--text", "--src-prefix=a/", "--dst-prefix=b/"}

// diffPath reads the file name off a `+++ ` header: the unquoting first, because git
// wraps the PREFIX inside the quotes, then the `b/` off the result. A `/dev/null`
// destination and anything else unrecognised give "", which skips the hunk rather than
// charging its lines to the wrong file.
func diffPath(field string) string {
	if i := strings.IndexByte(field, '\t'); i >= 0 {
		field = field[:i]
	}
	field = strings.TrimRight(field, " ")
	if strings.HasPrefix(field, `"`) {
		if unq, err := strconv.Unquote(field); err == nil {
			field = unq
		}
	}
	if !strings.HasPrefix(field, "b/") {
		return ""
	}
	return strings.TrimPrefix(field, "b/")
}

func gitCmd(ctx context.Context, dir string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "git", append(append([]string(nil), configOpts...), args...)...)
	cmd.Dir = dir
	// A bench's own git config cannot be allowed to change what this reads: the whole
	// point of the identity check is that the bench's configuration is not evidence.
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	return cmd
}

func gitLine(ctx context.Context, dir string, args ...string) (string, error) {
	out, err := gitOut(ctx, dir, args...)
	return strings.TrimSpace(out), err
}

// verb is the subcommand inside an argument list that may start with git-level
// options, so an error names `git diff` rather than `git --attr-source=…`.
func verb(args []string) string {
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			return a
		}
	}
	return "git"
}

func gitOut(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := gitCmd(ctx, dir, args...)
	out, err := cmd.Output()
	// A cancelled or expired context is a could-not-run whatever the process did on
	// its way out, and this package has exactly one rule about could-not-run: it is
	// never an empty answer, because an empty answer reads as a clean range.
	if ctx.Err() != nil {
		return "", fmt.Errorf("git %s: %v", verb(args), ctx.Err())
	}
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && len(ee.Stderr) > 0 {
			return "", fmt.Errorf("git %s: %v: %s", verb(args), err, strings.TrimSpace(string(ee.Stderr)))
		}
		return "", err
	}
	return string(out), nil
}

// modeFinding judges one entry's TYPE and MODE, and it is a function of its own so it
// can be tested on a mode git itself will not write.
//
// git records exactly four modes -- 100644, 100755, 120000 (symlink) and 160000
// (gitlink) -- and NORMALISES anything else to 100644 in `update-index`, `mktree` and
// every other path that writes a tree. So the spec's `hygiene-rejects-mode-100600`
// cannot be built as a repository fixture: no git command will produce one. That is
// not a reason to drop the rule. A tree can be written by something that is not git,
// and a check whose green rests on "git would never do that" has assumed away the only
// case it exists for. So the rule is kept, and it is proved here, directly, on the
// entry a non-git writer would produce.
func modeFinding(e entry) (Finding, bool) {
	switch e.newMode {
	case "120000":
		return Finding{Token: "stray-file", At: e.path,
			Why: "a symlink: a staged tree holds no way out of itself"}, true
	case "160000":
		return Finding{Token: "stray-file", At: e.path,
			Why: "a submodule: this range would carry a commit from a repository nobody here read"}, true
	case "100644", "100755":
		return Finding{}, false
	default:
		return Finding{Token: "stray-file", At: e.path,
			Why: fmt.Sprintf("mode %s; a file in a diff is 100644 or 100755", e.newMode)}, true
	}
}
