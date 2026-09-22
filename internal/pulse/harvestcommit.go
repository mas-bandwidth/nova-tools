package pulse

// THE HARVEST COMMIT STEP'S DECISION HALF (nova-tools #2549).
//
// `~/rowan-working/bin/harvest-priority` carried this as 5.8 KB of bash inside a
// single-quoted string inside an `ssh <bench> bash -s`, run on every UP bench every pass.
// In twelve hours it broke three times and stranded work silently each time:
//
//   - 2026-09-21: the Macs' login shell is zsh, which aborts on an unmatched glob. 24 DONE
//     cells on superman and batman were never committed and not one log line said so.
//   - 2026-09-22 02:05Z: a `case ... DONE\(*` inside the single-quoted string did not parse
//     on the remote bash. NO bench committed for seven hours, nothing landed overnight, the
//     canary failed, and the only trace was one `syntax error: unexpected end of file` per
//     bench per pass.
//   - 09:50 Eastern: the patch for that broke the file itself.
//
// Each fix was a bash edit inside a bash string inside an ssh, and there was no test.
//
// THE SPLIT IS THE POINT. Everything that is a JUDGEMENT about a job directory lives here,
// as one pure function over a value: no `os`, no `exec`, no git, no filesystem. Everything
// that TOUCHES git lives in harvestcommiteffect.go and never re-decides. A rule you can
// state is a rule you can put a table row against, which is what the bash could not have.
//
// EVERY VERDICT IS PRINTED (the second fact on #2549). The bash's driver greps only
// `^HARVEST (JOB|NO-COMMIT|REFUSED|BENCH)` and drops every `HARVEST SKIP` line: the log
// holds 0 SKIP lines against 12,784 REFUSED while the studio pass reports `skipped=96`
// every time, so ~96 verdicts per bench per pass went in the bin. Here a plan ALWAYS names
// an action and a reason, the verb prints one line per plan, and the summary line carries
// the counts, so a narrow grep downstream loses a line and never a fact.

import (
	"fmt"
	"path"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// CommitAction is what the step will do with one job directory. There are four, and they
// are the four verdicts the verb prints: a skip and a refusal are both DECISIONS with a
// reason, never silence.
type CommitAction int

const (
	// CommitSkip: this job is not this step's to commit, and nothing is touched.
	CommitSkip CommitAction = iota
	// CommitRefuse: this job WOULD be committed and must not be. The plan names the
	// offending path, and for the exfiltration fence the shape it matched.
	CommitRefuse
	// CommitBranchOnly: the work is already committed; only the branch name is wrong.
	CommitBranchOnly
	// CommitCommit: stage the plan's paths and commit them on the plan's branch.
	CommitCommit
)

func (a CommitAction) String() string {
	switch a {
	case CommitSkip:
		return "SKIP"
	case CommitRefuse:
		return "REFUSED"
	case CommitBranchOnly:
		return "BRANCH-ONLY"
	case CommitCommit:
		return "COMMIT"
	}
	return "UNKNOWN"
}

// The reasons, one per rule. They are the `reason=` token on the printed line and the name
// of the table row that pins the rule, so a line in a bench log leads to a test by name.
const (
	CommitWhyNotDone      = "not-done"      // R1
	CommitWhyReadCard     = "read-card"     // R2
	CommitWhyDraftOnly    = "draft-only"    // R3
	CommitWhyNoRepo       = "no-repo"       // R4
	CommitWhyNoResult     = "no-result"     // R5
	CommitWhyOutsideTree  = "outside-tree"  // R6, #2609
	CommitWhySecretPath   = "secret-path"   // R7, #2609
	CommitWhyTooBig       = "too-big"       // R8
	CommitWhyNothingToDo  = "nothing-to-do" // R9
	CommitWhyBadBranch    = "bad-branch"    // R10
	CommitWhyWorkIsCommit = "work"          // the ordinary commit
)

// commitMaxFileBytes is the size a single changed file may not exceed. The bash's bound,
// kept: a card that changed a file bigger than this is a card that did something other
// than the work, and harvest leaves it uncommitted for a person.
const commitMaxFileBytes = 1 << 20 // 1 MiB

// commitMessageMax is the commit subject's cap, in bytes. The bash used `cut -c1-200`.
const commitMessageMax = 200

// commitReportFold is how much of a card's REPORT.md is folded into its RESULT.md.
const commitReportFold = 1500

// cardScratchNames are the card's OWN files, written beside the work by the harness. When
// one is in the repository's working tree it is the worker having written in the wrong
// directory, and it is SET ASIDE inside the job directory rather than committed or deleted:
// on 2026-09-21 refusing the whole job over one of these left 88 DONE cells unharvested.
var cardScratchNames = []string{
	"RESULT.md", "notes.txt", "REPORT.md", "usage.tsv", "harness-output.log", "repo.bundle",
}

// cardAddedScratch matches a path the CARD ADDED that is scratch rather than work: it is
// dropped in a follow-up commit after the card's own commit, so the branch carries the work
// and nothing else. It is deliberately the bash's list and no wider.
var cardAddedScratch = regexp.MustCompile(`(^|/)notes\.txt$|^notes/|^\.nova-sandbox-tmp/|\.(class|o|obj|pyc|exe)$|(^|/)a\.out$`)

// baseSHAInLine1 reads the `sha=<hex>` a card's RESULT line 1 carries: the commit the card
// was cut at, which is what the scratch drop measures `--diff-filter=A` from.
var baseSHAInLine1 = regexp.MustCompile(`\bsha=([0-9a-f]{7,40})\b`)

// recutLabel reads `recut-<repo>-<number>-...`: a recut card carries the PR it recuts, and
// the `prior:` line says which one at which sha.
var recutLabel = regexp.MustCompile(`^recut-(nova-tools|schema)-([0-9]+)(?:-|$)`)

// ChangedFile is one entry of `git status --porcelain` in the card's repository: the
// repo-relative path as git spells it, and its size on disk (0 for a deletion).
type ChangedFile struct {
	Path string
	Size int64
}

// CommitJob is everything the decision is allowed to see. It is a VALUE: the caller reads
// the job directory once (harvestcommiteffect.go) and hands the facts over, so this
// function can be table-tested with no filesystem, no git and no bench.
type CommitJob struct {
	// Label is the job directory's name. `card-00-` is the cutter's prefix and is not
	// part of the branch name; it is stripped here, once.
	Label string

	// Line1 and Line2 are the RESULT.md's first two lines as written. Line1 is the
	// commit message; Line2 is the verdict.
	Line1 string
	Line2 string
	// HasResult is false when the job wrote no RESULT.md at all -- a crash, not a skip.
	HasResult bool
	// HasRepo is whether `repo/.git` is there. A job with no repository committed
	// nothing and is not this step's business.
	HasRepo bool

	// Changed is `git status --porcelain` in repo/, with each path's size.
	Changed []ChangedFile

	// ResultBranch, ResultBase, ResultRepo are the RESULT.md's BRANCH, BASE and REPO
	// lines, empty when the line is absent. HasBaseLine and HasBranchLine say whether
	// the LINE existed at all, which is the difference the bash could not see: its
	// `sed s#^BRANCH[: ].*#...#` substitutes nothing when the line is absent and exits
	// 0, so `BRANCH-LINE report-nova-tools-2537-r1 -> ...` was printed 94 times, once
	// per pass, and never once took.
	ResultBranch    string
	ResultBase      string
	ResultRepo      string
	HasBaseLine     bool
	HasBranchLine   bool
	HasPriorLine    bool
	HasClaimLine    bool
	HasReportToFold bool

	// CardBase is the BASE the CARD names, which beats the RESULT.md's: the card is
	// what the pulse cut, the RESULT.md is what a worker wrote, and only one of the two
	// is evidence (docs/SPEC-SWARM.md: everything a worker writes is data).
	CardBase string

	// CardPaths is the card's own PATHS declaration and CardFound whether a card was
	// found for this label at all. With a card, ONLY its declared paths are staged
	// (cards v2 A4, #2522). Without one the whole tree is staged and the plan carries
	// a WARN saying so, because refusing here is how DONE work goes unharvested.
	CardPaths []string
	CardFound bool

	// DraftOnly is the label-prefix list read from a file (--draft-only). The bash
	// carried nine `fix-nova-tools-<n>-*` prefixes inline, which is a rule nobody could
	// change without editing a shell string inside an ssh.
	DraftOnly []string

	// FallbackBase is the target a card that names none is rebased onto.
	FallbackBase string
}

// CommitPlan is the decision: one action, one reason, and everything the effect half needs
// to carry it out without deciding anything again.
type CommitPlan struct {
	Label  string // the label with `card-00-` stripped: the branch's and the log's name
	Action CommitAction
	Why    string
	Detail string

	// Path and Shape are set on a refusal: the offending file, and for the
	// exfiltration fence (#2609) the shape its PATH matched. No content is ever
	// carried here, and none is ever printed.
	Path  string
	Shape string

	Branch  string // rowan/<label>, always under the prefix
	Base    string // the target the card names, or the fallback
	Repo    string // owner/name off the RESULT.md's REPO line, when it named one
	Message string // RESULT.md line 1, one line, capped

	// SetAside are the card's own files found in the working tree: moved into the job
	// directory before staging, never committed and never deleted.
	SetAside []string
	// Stage is what `git add` is given. StageAll is the no-card fallback, and Warn is
	// the line that says so out loud.
	Stage    []string
	StageAll bool
	Warn     string

	// BaseSHA is the `sha=` on RESULT line 1: where the card was cut. The scratch drop
	// measures added paths from it.
	BaseSHA string
	// PriorPR is the PR a `recut-*` card recuts, 0 for anything else.
	PriorPR   int
	PriorRepo string

	// WriteBase, WriteBranch and WritePrior say which RESULT.md lines the effect must
	// WRITE (append when absent, rewrite in place when present and different). The bash
	// could only substitute, which is why the report-* cards never got a BRANCH line.
	WriteBase   bool
	WriteBranch bool
	WritePrior  bool
	FoldReport  bool
}

// CommitDecision is the whole judgement, from a value to a plan. It reaches nothing.
//
// The order is not arbitrary. The two fences come before the work rules, because a job
// that must not be committed must not be committed for the FIRST reason it fails, and a
// path that carries a key is a worse fact about a card than a path that is large.
func CommitDecision(job CommitJob) CommitPlan {
	label := strings.TrimPrefix(strings.TrimSpace(job.Label), "card-00-")
	p := CommitPlan{Label: label, Repo: strings.TrimSpace(job.ResultRepo)}

	skip := func(why, detail string) CommitPlan {
		p.Action, p.Why, p.Detail = CommitSkip, why, detail
		return p
	}
	refuse := func(why, detail, offending, shape string) CommitPlan {
		p.Action, p.Why, p.Detail = CommitRefuse, why, detail
		p.Path, p.Shape = offending, shape
		return p
	}

	// R2. A read card reviews somebody else's work and has nothing of its own to commit.
	if strings.HasPrefix(label, "read-") {
		return skip(CommitWhyReadCard, "a read card reviews and commits nothing")
	}
	// R3. DRAFT-ONLY, from a file rather than from nine prefixes buried in a shell
	// string (Glenn 2026-09-21: friends build the tooling; the swarm card is a draft).
	if pre := matchDraftOnly(job.DraftOnly, label); pre != "" {
		return skip(CommitWhyDraftOnly, fmt.Sprintf("prefix=%s (friends build the tooling; the swarm card is a draft, not committed or harvested)", field(pre)))
	}
	// R5. No RESULT.md at all is a crash, not a verdict, and it is named as one.
	if !job.HasResult {
		return skip(CommitWhyNoResult, "the job wrote no RESULT.md; nothing here says the work finished")
	}
	// R1. THE DONE PREFIX, which is the rule the bash got wrong twice. Its test was
	// `[ "$(sed -n 2p RESULT.md | tr -d '[:space:]')" = DONE ]`, which strips the spaces
	// out of `DONE (both runs green)` and then fails to match it; the `case ... DONE\(*`
	// written to fix that did not parse on the remote bash and stopped every bench for
	// seven hours. `DONE` is a WORD here: the line begins with it and what follows is
	// not another word character. `DONE (both green)` is DONE. `DONEish` is not.
	if !commitLineIsDone(job.Line2) {
		return skip(CommitWhyNotDone, fmt.Sprintf("line2=%s (only a DONE prefix counts; `DONE (both runs green)` is DONE, `DONEish` is not)", field(oneShort(job.Line2, 40))))
	}
	// R4.
	if !job.HasRepo {
		return skip(CommitWhyNoRepo, "repo/.git is not there; this job committed nothing")
	}

	// R10. The branch, before anything is planned against it.
	branch, err := commitBranchName(label, job.ResultBranch)
	if err != nil {
		return refuse(CommitWhyBadBranch, oneShort(err.Error(), 160), "", "")
	}
	p.Branch = branch

	// R6 and R7: THE EXFILTRATION FENCE (#2609). A PR is a connected destination, and a
	// card runs with the launcher's environment on the same host as the seat's secrets.
	// A commit that ADDS a path from outside the repository tree, or a path whose SHAPE
	// is a credential's, is refused with the path and the shape named -- never dropped
	// silently, because a silent drop is a fence nobody can audit. The CONTENT scan is
	// the effect half's, against the staged bytes (harvestcommiteffect.go); this is the
	// half that can be decided from a name.
	for _, f := range job.Changed {
		if outsideRepoTree(f.Path) {
			return refuse(CommitWhyOutsideTree, "a changed path leaves the repository tree; nothing was committed",
				f.Path, "outside-tree")
		}
		if shape := secretPathShape(f.Path); shape != "" {
			return refuse(CommitWhySecretPath, "a changed path has the shape of a credential; nothing was committed",
				f.Path, shape)
		}
	}
	// R8. One file over 1 MB refuses the whole job, and names the file.
	for _, f := range job.Changed {
		if f.Size > commitMaxFileBytes {
			return refuse(CommitWhyTooBig,
				fmt.Sprintf("size=%dB over %dB; left uncommitted", f.Size, commitMaxFileBytes), f.Path, "")
		}
	}

	// The card's own files in the working tree: set aside, not committed, not deleted.
	var work []ChangedFile
	for _, f := range job.Changed {
		if isCardScratch(f.Path) {
			p.SetAside = append(p.SetAside, f.Path)
			continue
		}
		work = append(work, f)
	}

	p.Message = commitMessage(job.Line1)
	p.BaseSHA = commitBaseSHA(job.Line1)
	p.Base = commitBase(job, label)
	if m := recutLabel.FindStringSubmatch(label); m != nil {
		p.PriorRepo, p.PriorPR = m[1], atoiSafe(m[2])
		p.WritePrior = !job.HasPriorLine && p.PriorPR > 0
	}
	// The RESULT.md lines the effect must WRITE. `WriteBranch` is true when the line is
	// absent OR names something else: the bash could only do the second, so a card
	// without the line kept "succeeding" at writing it 94 times over.
	p.WriteBranch = !job.HasBranchLine || strings.TrimSpace(job.ResultBranch) != branch
	p.WriteBase = p.Base != "" && (!job.HasBaseLine || strings.TrimSpace(job.ResultBase) != p.Base)
	p.FoldReport = job.HasReportToFold && !job.HasClaimLine

	// R9. Nothing in the tree: the work is already committed and at most the branch
	// name is wrong. The bash's `elif` arm, named.
	if len(work) == 0 {
		p.Action, p.Why = CommitBranchOnly, CommitWhyNothingToDo
		p.Detail = "the working tree carries no card work; the branch is set and the RESULT lines are written"
		return p
	}

	p.Action, p.Why = CommitCommit, CommitWhyWorkIsCommit
	// A4 (#2522): with a card, stage the card's DECLARED PATHS and nothing else. The
	// bash staged `-A` always, which is the rule this whole family exists to enforce.
	// Without a card there is nothing to stage BY, and refusing would strand DONE work,
	// so the whole tree is staged and the plan says WARN out loud.
	if job.CardFound && len(job.CardPaths) > 0 {
		p.Stage = append([]string(nil), job.CardPaths...)
	} else {
		p.StageAll = true
		p.Warn = "no card found for this label, so the whole tree is staged (cards v2 A4 wants the card's PATHS; a card that names its own paths is the fix)"
	}
	return p
}

// commitLineIsDone is R1, on its own so the table row points at one function. `DONE` must
// be the first WORD of the line: `DONE`, `DONE (both runs green)`, `DONE -- green` are all
// DONE, and `DONEish`, `DONE_LATER`, `NEARLY DONE` are not.
func commitLineIsDone(line string) bool {
	s := strings.TrimSpace(line)
	rest, ok := strings.CutPrefix(s, "DONE")
	if !ok {
		return false
	}
	if rest == "" {
		return true
	}
	r := []rune(rest)[0]
	return !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_')
}

// commitBranchName is R10: `rowan/<label>`, or the RESULT.md's BRANCH line put under the
// prefix when it names one. It is held against the same mustBranchPrefix every push in
// this package is held against, so a card cannot name a trunk or escape the prefix.
func commitBranchName(label, resultBranch string) (string, error) {
	b := strings.TrimSpace(resultBranch)
	// A RESULT.md that names a trunk is refused rather than quietly prefixed into
	// `rowan/main`: everything a worker writes is data (docs/SPEC-SWARM.md), and a card
	// that believes it is working on main is a card whose branch nobody should guess at.
	if b == "main" || b == "master" || b == "dev" {
		return "", fmt.Errorf("the RESULT.md names branch %s; harvest commits no trunk and guesses no replacement for one", oneline.Quote(b))
	}
	switch {
	case b == "":
		b = DefaultBranchPrefix + label
	case !strings.HasPrefix(b, DefaultBranchPrefix):
		b = DefaultBranchPrefix + b
	}
	if strings.TrimSpace(label) == "" && strings.TrimSpace(resultBranch) == "" {
		return "", fmt.Errorf("the job directory has no label, so there is no branch to commit on")
	}
	if strings.ContainsAny(b, " \t\n:~^?*[\\") || strings.Contains(b, "..") {
		return "", fmt.Errorf("branch %s is not a git branch name", oneline.Quote(b))
	}
	if err := mustBranchPrefix(b); err != nil {
		return "", err
	}
	return b, nil
}

// commitMessage is the commit subject: RESULT.md line 1, forced onto one line and capped.
func commitMessage(line1 string) string {
	s := strings.TrimSpace(strings.Join(strings.Fields(line1), " "))
	if s == "" {
		return "harvest: a card's work, committed by nova-pulse commit"
	}
	if len(s) <= commitMessageMax {
		return s
	}
	cut := s[:commitMessageMax]
	for len(cut) > 0 && !utf8.ValidString(cut) {
		cut = cut[:len(cut)-1]
	}
	return cut
}

func commitBaseSHA(line1 string) string {
	if m := baseSHAInLine1.FindStringSubmatch(line1); m != nil {
		return m[1]
	}
	return ""
}

// commitBase is the target the branch is measured and rebased against: the card's BASE
// when it named one, else the RESULT.md's, else the caller's fallback. A `report-*` card
// writes no code and is rebased onto nothing, which is the bash's own `case` arm.
func commitBase(job CommitJob, label string) string {
	if strings.HasPrefix(label, "report-") {
		return ""
	}
	for _, cand := range []string{job.CardBase, job.ResultBase, job.FallbackBase} {
		if b := harvestTargetName(cand); b != "" {
			return b
		}
	}
	return DefaultBase
}

func matchDraftOnly(list []string, label string) string {
	for _, pre := range list {
		pre = strings.TrimSpace(pre)
		if pre == "" || strings.HasPrefix(pre, "#") {
			continue
		}
		p := strings.TrimSuffix(pre, "*")
		if p != "" && strings.HasPrefix(label, p) {
			return pre
		}
	}
	return ""
}

func isCardScratch(p string) bool {
	for _, n := range cardScratchNames {
		if p == n {
			return true
		}
	}
	return false
}

// outsideRepoTree is the first half of the fence (#2609): a path git reports that does not
// stay inside the repository. An absolute path, a `..` segment, or a leading `~` are each
// a way for a card to name a file the repository does not own.
func outsideRepoTree(p string) bool {
	q := strings.ReplaceAll(strings.TrimSpace(p), `\`, "/")
	if q == "" {
		return false
	}
	if strings.HasPrefix(q, "/") || strings.HasPrefix(q, "~") {
		return true
	}
	if len(q) > 1 && q[1] == ':' { // a Windows drive letter
		return true
	}
	for _, seg := range strings.Split(q, "/") {
		if seg == ".." {
			return true
		}
	}
	return false
}

// secretPathShape is the second half of the fence (#2609): the SHAPE of a credential's
// path, from the issue's own list. It returns the shape's name, which the refusal prints
// beside the path. It never opens the file -- the content scan is the effect half's, and
// neither ever prints a byte of what it read.
func secretPathShape(p string) string {
	q := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(p), `\`, "/"))
	if q == "" {
		return ""
	}
	base := path.Base(q)
	segs := strings.Split(q, "/")
	for _, s := range segs {
		switch s {
		case ".ssh":
			return "ssh-dir"
		case "nova-secrets", ".gnupg", ".aws", ".netrc":
			return "secrets-dir"
		}
	}
	if strings.Contains(q, "nova-bench/secrets") {
		return "secrets-dir"
	}
	switch {
	case strings.HasSuffix(base, ".key"), strings.HasSuffix(base, ".keyfile"):
		return "key-file"
	case strings.HasSuffix(base, ".pem"):
		return "pem-file"
	case strings.HasSuffix(base, ".p12"), strings.HasSuffix(base, ".pfx"):
		return "pkcs12-file"
	case base == "auth.json", base == ".netrc", base == "credentials":
		return "auth-file"
	case base == ".env" || strings.HasPrefix(base, ".env."):
		return "dotenv"
	case strings.HasPrefix(base, "id_") && !strings.HasSuffix(base, ".pub"):
		return "ssh-private-key"
	}
	return ""
}

func atoiSafe(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0
		}
		n = n*10 + int(r-'0')
	}
	return n
}

func oneShort(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > n {
		s = s[:n] + "..."
	}
	return s
}
