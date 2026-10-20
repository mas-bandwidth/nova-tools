package release

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
	"golang.org/x/mod/semver"
)

// prNumber matches the `(#123)` a squash merge puts at the end of the subject.
// It is anchored to the END of the subject on purpose: this repository's own
// merges read `feat: nova-pulse hygiene, the path-safe bench cleanup verbs
// (#1142) (#1253)`, where the first number is the ISSUE the work closes and the
// last is the pull request that merged it. Taking the first would file every
// such commit under a number that never existed as a pull request.
var prNumber = regexp.MustCompile(`\(#(\d+)\)\s*$`)

// memberNumber matches a `#123` anywhere in a commit body. An integration batch
// lists its members there, and the list is the only place the individual pull
// requests appear at all -- the batch is one merge commit.
var memberNumber = regexp.MustCompile(`#(\d+)\b`)

// mergePullNumber matches the `Merge pull request #n from ...` subject a
// merge-queue merge leaves. Its number is NOT at the end of the subject as a
// squash merge's is, so the end-anchored prNumber misses it; a merge commit
// still names one pull request, and the changelog lists every one.
var mergePullNumber = regexp.MustCompile(`^Merge pull request #(\d+)\b`)

// mergePR reads a merge commit's message back to its pull request: the number
// from the `Merge pull request #n from o/b` subject, and the title from the
// body's first non-empty line (the pull request's own title, which the merge
// subject does not carry). memberBody is the body with that title line removed,
// so a `#n` the title merely mentions is not filed as a member.
func mergePR(subject, body string) (n int, title, memberBody string) {
	m := mergePullNumber.FindStringSubmatch(subject)
	if m == nil {
		return 0, "", body
	}
	n, _ = strconv.Atoi(m[1])
	title = strings.TrimSpace(subject)
	if trimmed := strings.TrimSpace(body); trimmed != "" {
		if first, rest, _ := strings.Cut(trimmed, "\n"); strings.TrimSpace(first) != "" {
			title = strings.TrimSpace(first)
			memberBody = rest
		}
	}
	return n, title, memberBody
}

// PullRequests reads a compare range as a changelog. One commit whose subject
// ends in `(#n)` is one pull request; a `Merge pull request #n from ...` subject
// is one too, with its title read from the body. The rest of the message, if it
// names other pull requests, is that entry's member list.
func PullRequests(commits []Commit) []PR {
	var prs []PR
	for _, c := range commits {
		subject, body, _ := strings.Cut(strings.TrimSpace(c.Message), "\n")
		subject = strings.TrimSpace(subject)
		n, title, memberBody := 0, "", body
		if m := prNumber.FindStringSubmatch(subject); m != nil {
			n, _ = strconv.Atoi(m[1])
			title = strings.TrimSpace(strings.TrimSuffix(subject, m[0]))
		} else {
			n, title, memberBody = mergePR(subject, body)
		}
		if n == 0 {
			continue
		}
		pr := PR{Number: n, Title: title}
		seen := map[int]bool{n: true}
		for _, mm := range memberNumber.FindAllStringSubmatch(memberBody, -1) {
			member, err := strconv.Atoi(mm[1])
			if err != nil || seen[member] {
				continue
			}
			seen[member] = true
			pr.Members = append(pr.Members, member)
		}
		prs = append(prs, pr)
	}
	return prs
}

// green is the gate. A tag is the one thing in this repository that cannot be
// quietly amended and pushed again, so the evidence is read before it is
// written: every check run on the commit must have COMPLETED, and completed
// with a conclusion that is not a failure. A commit no run has ever judged is
// not green either -- it is unjudged, which is the state that let four benches
// run six-hour-old tools while everybody believed otherwise.
//
// The way past is `--waive-ci "<who, when>"` (ciWaiver), which is the cut's own
// flag and not a hand-made tag: what it waived travels in the tag annotation
// and the release notes, per docs/SPEC-RELEASE.md section 19.
func green(runs []CheckRun) error {
	if len(runs) == 0 {
		return refuse("dispatch CI on this commit and cut again when it is green",
			"no check run has judged this commit")
	}
	failing, pending := ciNotGreen(runs)
	if len(failing) > 0 {
		return refuse("fix the red and cut again; a tag cannot be amended",
			"CI is not green on this commit: %s", strings.Join(failing, ", "))
	}
	if len(pending) > 0 {
		return refuse("wait for the run to finish and cut again",
			"CI has not finished on this commit: %s", strings.Join(pending, ", "))
	}
	return nil
}

// sourceChecks excludes only a cancelled publication of an existing older
// release tag (cmd/nova-update/README.md, release cut; docs/SPEC-RELEASE.md
// section 19), a whole cancelled suite superseded by a later green suite of the
// same workflow path, a stale revert-on-red verdict beside ci's later green
// latest attempt, and the checks of an earlier attempt of a workflow run whose
// latest attempt is green (nova-sprint tla/ReleaseTrain.tla StaleVerdict and
// CutOnlyGreen). All other checks, including unknown provenance, remain evidence
// the ordinary CI gate must judge. It returns the set-aside runs too, so the
// cut's record can name what it ignored and why.
func sourceChecks(runs []checkRun, tags []string, version string) (kept, setAside []checkRun) {
	older := map[string]bool{}
	for _, tag := range tags {
		if ValidVersion(tag) == nil && semver.IsValid(tag) && semver.IsValid(version) && semver.Compare(tag, version) < 0 {
			older[tag] = true
		}
	}
	aside := supersedingSuites(runs)
	stale := staleRevertVerdicts(runs)
	superseded := supersededAttempts(runs)
	kept = make([]checkRun, 0, len(runs))
	for i, r := range runs {
		if r.Status == "completed" && r.Conclusion == "cancelled" && r.WorkflowPath == ".github/workflows/release.yml" && r.Event == "push" && r.TagPush && older[r.HeadBranch] {
			setAside = append(setAside, r)
			continue
		}
		if by, ok := aside[i]; ok {
			r.SupersededBy = by
			setAside = append(setAside, r)
			continue
		}
		if reason, ok := stale[i]; ok {
			r.SetAsideReason = reason
			setAside = append(setAside, r)
			continue
		}
		if reason, ok := superseded[i]; ok {
			r.SetAsideReason = reason
			setAside = append(setAside, r)
			continue
		}
		kept = append(kept, r)
	}
	return kept, setAside
}

// revertOnRedWorkflow and ciWorkflow are the two workflow paths the stale
// verdict reads. A revert-on-red failure is judged beside ci's own latest
// attempt at the same commit and no other workflow's (docs/SPEC-RELEASE.md
// section 19; nova-sprint tla/ReleaseTrain.tla StaleVerdict).
const (
	revertOnRedWorkflow = ".github/workflows/revert-on-red.yml"
	ciWorkflow          = ".github/workflows/ci.yml"
)

// staleRevertVerdicts finds every completed failed or cancelled revert-on-red
// check whose verdict ci's latest attempt already replaced: a ci workflow run at
// the same commit whose highest run_attempt concluded success and completed
// after the revert run was created. It answers the run's index mapped to
// `stale-verdict-of=<ci run id>@<attempt>` (nova-sprint tla/ReleaseTrain.tla
// StaleVerdict; the train's revert step reads the same rule; docs/SPEC-RELEASE.md
// section 19). A ci attempt that is red or pending, or green but completed before
// the revert run was created, leaves the verdict live.
func staleRevertVerdicts(runs []checkRun) map[int]string {
	type attempt struct {
		number int
		run    checkRun
	}
	latest := map[int64]attempt{}
	for _, r := range runs {
		if r.WorkflowPath != ciWorkflow || r.ID == 0 {
			continue
		}
		if a, ok := latest[r.ID]; !ok || r.RunAttempt > a.number {
			latest[r.ID] = attempt{number: r.RunAttempt, run: r}
		}
	}
	out := map[int]string{}
	for i, r := range runs {
		if r.WorkflowPath != revertOnRedWorkflow || r.Status != "completed" {
			continue
		}
		if r.Conclusion != "failure" && r.Conclusion != "cancelled" {
			continue
		}
		// Without the verdict's created time nothing proves ci came later.
		if r.CreatedAt.IsZero() {
			continue
		}
		// Several ci runs can answer for one commit; the one that completed
		// last is the verdict's replacement, and the smaller id breaks a tie
		// so the record does not depend on map order.
		best, bestAt := attempt{}, time.Time{}
		for _, a := range latest {
			if a.run.WorkflowConclusion != "success" || a.run.UpdatedAt.IsZero() || !a.run.UpdatedAt.After(r.CreatedAt) {
				continue
			}
			if best.run.ID == 0 || a.run.UpdatedAt.After(bestAt) || (a.run.UpdatedAt.Equal(bestAt) && a.run.ID < best.run.ID) {
				best, bestAt = a, a.run.UpdatedAt
			}
		}
		if best.run.ID != 0 {
			out[i] = fmt.Sprintf("stale-verdict-of=%d@%d", best.run.ID, best.number)
		}
	}
	return out
}

// supersededAttempts finds the checks of an earlier attempt of a workflow run
// whose latest attempt (the same workflow run id, a higher run_attempt) completed
// green and has every job name the earlier one had. It answers each earlier
// check's index mapped to `superseded-attempt=<run id>@<latest attempt>`
// (nova-sprint tla/ReleaseTrain.tla CutOnlyGreen; docs/SPEC-RELEASE.md section
// 19). The latest attempt must have completed with every run success, skipped or
// neutral and the workflow run concluded success; an attempt missing a job name
// supersedes nothing.
func supersededAttempts(runs []checkRun) map[int]string {
	byRun := map[int64]map[int][]int{}
	for i, r := range runs {
		if r.ID == 0 {
			continue
		}
		if byRun[r.ID] == nil {
			byRun[r.ID] = map[int][]int{}
		}
		byRun[r.ID][r.RunAttempt] = append(byRun[r.ID][r.RunAttempt], i)
	}
	out := map[int]string{}
	for runID, attempts := range byRun {
		latest := -1
		for number := range attempts {
			if number > latest {
				latest = number
			}
		}
		if len(attempts) < 2 || !greenAttempt(runs, attempts[latest]) {
			continue
		}
		newNames := jobNames(runs, attempts[latest])
		for number, indexes := range attempts {
			if number >= latest || !covers(newNames, jobNames(runs, indexes)) {
				continue
			}
			for _, i := range indexes {
				out[i] = fmt.Sprintf("superseded-attempt=%d@%d", runID, latest)
			}
		}
	}
	return out
}

// greenAttempt reports whether every run of an attempt is completed and green and
// the workflow run itself concluded success.
func greenAttempt(runs []checkRun, indexes []int) bool {
	green := true
	for _, i := range indexes {
		r := runs[i]
		if r.Status != "completed" || (r.Conclusion != "success" && r.Conclusion != "skipped" && r.Conclusion != "neutral") || r.WorkflowConclusion != "success" {
			green = false
		}
	}
	return green
}

// jobNames is the set of job names an attempt ran; matrix legs are part of the
// name, so a missing leg is a missing name.
func jobNames(runs []checkRun, indexes []int) map[string]bool {
	names := map[string]bool{}
	for _, i := range indexes {
		names[runs[i].Name] = true
	}
	return names
}

// supersedingSuites finds every run that belongs to a cancelled suite S with a
// later green suite T of the same workflow path covering the same job names,
// and answers that run's index mapped to T's workflow run ID (the superseding
// run). S qualifies only when every run in it is completed and the workflow
// run's own conclusion is cancelled: an aggregate job such as certification-ok
// that reports failure only because its needs were cancelled is part of the
// cancelled suite, told apart by the workflow run's conclusion rather than the
// job's. T qualifies only when every run in it is completed with a success,
// skipped or neutral conclusion, it completed later than S, and it has a run
// for every job name S has, matrix legs included (docs/SPEC-RELEASE.md section
// 19).
func supersedingSuites(runs []checkRun) map[int]int64 {
	bySuite := map[int64][]int{}
	for i := range runs {
		if runs[i].SuiteID > 0 {
			bySuite[runs[i].SuiteID] = append(bySuite[runs[i].SuiteID], i)
		}
	}
	type suite struct {
		path       string
		updated    time.Time
		runID      int64
		names      map[string]bool
		completed  bool
		allGreen   bool
		cancelled  bool
		supersedes int64
	}
	infos := make(map[int64]*suite, len(bySuite))
	for id, indexes := range bySuite {
		s := &suite{names: map[string]bool{}, completed: true, allGreen: true}
		// Every run in a suite carries the same workflow provenance; read the
		// suite's conclusion and update time from the first run's copy.
		first := runs[indexes[0]]
		s.path, s.updated, s.runID = first.WorkflowPath, first.UpdatedAt, first.ID
		s.cancelled = first.WorkflowConclusion == "cancelled"
		for _, i := range indexes {
			r := runs[i]
			s.names[r.Name] = true
			if r.Status != "completed" {
				s.completed = false
			}
			if r.Conclusion != "success" && r.Conclusion != "skipped" && r.Conclusion != "neutral" {
				s.allGreen = false
			}
		}
		s.cancelled = s.cancelled && s.completed && s.runID != 0
		infos[id] = s
	}
	for id, s := range infos {
		if !s.cancelled {
			continue
		}
		for tid, t := range infos {
			if tid == id || !t.allGreen || t.path == "" || t.path != s.path || !t.updated.After(s.updated) || !covers(t.names, s.names) {
				continue
			}
			s.supersedes = t.runID
			break
		}
	}
	out := map[int]int64{}
	for id, s := range infos {
		if s.cancelled && s.supersedes != 0 {
			for _, i := range bySuite[id] {
				out[i] = s.supersedes
			}
		}
	}
	return out
}

// covers reports whether every job name in s appears in t. Matrix legs are part
// of the job name, so a missing matrix leg is a missing job name.
func covers(t, s map[string]bool) bool {
	for name := range s {
		if !t[name] {
			return false
		}
	}
	return true
}

// ciSetAside names the runs a cut set aside, each as its run id and the reason:
// the older tag it published, the later green suite's run id that superseded it,
// the ci attempt that made its revert-on-red verdict stale, or the later attempt
// that superseded it, for the RELEASE CUT line's ci-set-aside field
// (cmd/nova-update/README.md, release cut; docs/SPEC-RELEASE.md section 19).
func ciSetAside(runs []checkRun) string {
	if len(runs) == 0 {
		return ""
	}
	var b strings.Builder
	for i, r := range runs {
		if i > 0 {
			b.WriteByte(',')
		}
		switch {
		case r.SetAsideReason != "":
			fmt.Fprintf(&b, "%d:%s", r.ID, r.SetAsideReason)
		case r.SupersededBy != 0:
			fmt.Fprintf(&b, "%d:superseded-by=%d", r.ID, r.SupersededBy)
		default:
			fmt.Fprintf(&b, "%d@%s", r.ID, r.HeadBranch)
		}
	}
	return b.String()
}

// CIAsidePrefix opens the line that names what a cut set aside, written into
// the tag annotation and the CHANGELOG section so the record of what was
// ignored travels with the release (docs/SPEC-RELEASE.md section 19).
const CIAsidePrefix = "CI set aside: "

// ciAsideRecord is the set-aside record as the tag annotation and the CHANGELOG
// section carry it, and the empty string when nothing was set aside. It names
// each set-aside run and the reason it was set aside, in the same shape the cut
// line prints.
func ciAsideRecord(runs []checkRun) string {
	if len(runs) == 0 {
		return ""
	}
	return CIAsidePrefix + ciSetAside(runs) + "\n"
}

// readCheckRuns reads the check runs with their suite provenance where the forge
// offers it; a forge that only satisfies CheckRuns is read without suites, and
// the superseding gate then has nothing to judge (docs/SPEC-RELEASE.md section
// 19). The richer read is behind a type assertion rather than the Forge
// interface, so the public seam keeps its one shape.
func readCheckRuns(ctx context.Context, forge Forge, repo, sha string) ([]checkRun, error) {
	if f, ok := forge.(interface {
		checkRunsWithSuites(context.Context, string, string) ([]checkRun, error)
	}); ok {
		return f.checkRunsWithSuites(ctx, repo, sha)
	}
	runs, err := forge.CheckRuns(ctx, repo, sha)
	if err != nil {
		return nil, err
	}
	out := make([]checkRun, len(runs))
	for i := range runs {
		out[i] = checkRun{CheckRun: runs[i]}
	}
	return out, nil
}

// asCheckRuns is a checkRun slice as the plain CheckRun slice the green gate and
// its waiver read: the suite fields are the cut's own and do not reach green.
func asCheckRuns(runs []checkRun) []CheckRun {
	out := make([]CheckRun, len(runs))
	for i := range runs {
		out[i] = runs[i].CheckRun
	}
	return out
}

// ciNotGreen reads the runs once and answers the checks green() would refuse
// on, in green()'s own words: the failing ones as `name=conclusion`, and the
// ones that have not completed by name. It is separate so a waiver records the
// same checks the gate would have named rather than a second reading of them.
func ciNotGreen(runs []CheckRun) (failing, pending []string) {
	for _, r := range runs {
		switch {
		case r.Status != "completed":
			pending = append(pending, r.Name)
		case r.Conclusion != "success" && r.Conclusion != "skipped" && r.Conclusion != "neutral":
			failing = append(failing, r.Name+"="+r.Conclusion)
		}
	}
	sort.Strings(failing)
	sort.Strings(pending)
	return failing, pending
}

// CIWaiverPrefix is how the tag annotation and the CHANGELOG section name a
// waived CI gate, spelled once so the two records cannot drift. A red CI is
// let past only by the cut's own --waive-ci, and what it let past outlives the
// terminal: a person asking in six months why a version shipped on a red commit
// reads the answer in the tag. docs/SPEC-RELEASE.md section 19.
const CIWaiverPrefix = "CI waived: "

// CIWaiverChecksPrefix opens the line that names the checks the waiver covered.
const CIWaiverChecksPrefix = "CI waived checks: "

// ciWaivedChecks names, for a one-line terminal field, the checks a CI waiver
// covered: the failing ones, else the unfinished ones, else none.
func ciWaivedChecks(runs []CheckRun) string {
	failing, pending := ciNotGreen(runs)
	switch {
	case len(failing) > 0:
		return strings.Join(failing, ",")
	case len(pending) > 0:
		return strings.Join(pending, ",") + "(not-finished)"
	}
	return "none"
}

// ciWaiver is the waived CI record as the tag annotation and the CHANGELOG
// section carry it, and the empty string when there is nothing to waive. who is
// trimmed; the caller refuses an empty one before calling. It names the checks
// green() would have refused on, so the record says WHAT was let past and not
// only that the gate was: docs/SPEC-RELEASE.md section 19.
func ciWaiver(runs []CheckRun, who string) string {
	who = strings.TrimSpace(who)
	if who == "" {
		return ""
	}
	failing, pending := ciNotGreen(runs)
	var b strings.Builder
	fmt.Fprintf(&b, "%s%s\n", CIWaiverPrefix, who)
	switch {
	case len(failing) > 0:
		fmt.Fprintf(&b, "%s%s\n", CIWaiverChecksPrefix, strings.Join(failing, ", "))
	case len(pending) > 0:
		fmt.Fprintf(&b, "%s%s (not finished)\n", CIWaiverChecksPrefix, strings.Join(pending, ", "))
	default:
		fmt.Fprintf(&b, "%snone judged this commit\n", CIWaiverChecksPrefix)
	}
	return b.String()
}

// previousTag picks the highest version tag in the repository. It is a semantic
// comparison, not a lexical one: v0.15.10 comes after v0.15.3, which a sort by
// string puts the other way round and which would make the changelog for a
// patch release list seven releases' worth of work.
func previousTag(tags []string) string {
	best, bestParts := "", []int{}
	for _, tag := range tags {
		if ValidVersion(tag) != nil {
			continue
		}
		parts := versionParts(tag)
		if best == "" || lessVersion(bestParts, parts) {
			best, bestParts = tag, parts
		}
	}
	return best
}

func versionParts(tag string) []int {
	out := make([]int, 3)
	fields := strings.SplitN(strings.TrimPrefix(tag, "v"), ".", 3)
	for i := range out {
		if i >= len(fields) {
			break
		}
		n := fields[i]
		if c := strings.IndexAny(n, "-+"); c >= 0 {
			n = n[:c]
		}
		out[i], _ = strconv.Atoi(n)
	}
	return out
}

func lessVersion(a, b []int) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}

// sectionWith renders one changelog section, with what the journey gate found, which
// is already in the section's words (JourneyRecord.section; "" when it found nothing to
// say). It is pure so that the shape of what a release says about itself is asserted by
// a test rather than by reading a file somebody wrote by hand afterwards.
func sectionWith(version, sha, previous, sumsDigest, dogfoodWaiver, journeys string, when time.Time, prs []PR, ciWaiver ...string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## %s — %s\n\n", version, when.UTC().Format("2006-01-02"))
	since := "this repository's first commit"
	if previous != "" {
		since = previous
	}
	fmt.Fprintf(&b, "Cut from %s. %s since %s.\n\n", sha, plural(len(prs), "pull request"), since)
	// THE DIGEST GOES IN THE CHANGELOG, WHICH TRAVELS BY GIT. `adopt` fetching
	// a release from another machine cannot verify it with the checksum file
	// that came with it -- anybody who could change one could change the other
	// -- so it is given this digest instead, which reached the adopting host
	// through the repository rather than through the machine being read
	// (a person, 2026-09-18). It is written in the form the check wants, so
	// nobody has to transcribe it.
	if sumsDigest != "" {
		fmt.Fprintf(&b, "%s%s\n\nAdopt this release with `--expect-sums %s`.\n\n", SumsDigestPrefix, sumsDigest, sumsDigest)
	}
	// AND THE WAIVER, WHEN THERE WAS ONE. The dogfood gate is the definition
	// of done in front of the tag, and a release that went round it says so
	// HERE, in the file that travels by git, rather than only in the terminal
	// that cut it. A person asking in six months why v0.17.0 shipped with an
	// open edge reads the answer in the same place they read what shipped.
	if dogfoodWaiver != "" {
		fmt.Fprintf(&b, "%s%s\n\n", DogfoodWaiverPrefix, dogfoodWaiver)
	}
	// AND THE CI WAIVER, WHEN A RED OR MISSING CI WAS LET PAST. It sits beside
	// the dogfood waiver for the same reason: a version cut past the green gate
	// says so where the release is read, in the file that travels by git, and
	// names the checks it let past (docs/SPEC-RELEASE.md section 19).
	if len(ciWaiver) > 0 && ciWaiver[0] != "" {
		b.WriteString(ciWaiver[0])
		b.WriteString("\n")
	}
	// AND WHAT THE PROMISED JOURNEYS WERE PROVEN AT, or which of them were
	// not, under the waiver's reason.
	b.WriteString(journeys)
	for _, pr := range prs {
		fmt.Fprintf(&b, "- #%d %s\n", pr.Number, pr.Title)
		if len(pr.Members) > 0 {
			members := make([]string, len(pr.Members))
			for i, m := range pr.Members {
				members[i] = "#" + strconv.Itoa(m)
			}
			fmt.Fprintf(&b, "  - members: %s\n", strings.Join(members, ", "))
		}
	}
	if len(prs) == 0 {
		b.WriteString("- no pull request merged since the previous tag\n")
	}
	b.WriteString("\n")
	return b.String()
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// SumsDigestPrefix is how the changelog names the digest of a release's
// SHA256SUMS, in one place so that what `cut` writes and what a person copies
// into `adopt --expect-sums` are the same string.
const SumsDigestPrefix = "SHA256SUMS digest: "

// AnnotationSumsPrefix is how the TAG names the same digest, and it is written
// on a line of its own so that reading it back is an anchored match rather than
// a search through prose.
const AnnotationSumsPrefix = "sums="

// annotationSums reads the digest line and only the digest line. A sha
// mentioned inside a release note is not the digest this release was cut with,
// and a reader that took the first 64 hex characters it found would sometimes
// be right, which is the worst way for a check like this to be wrong.
var annotationSums = regexp.MustCompile(`(?m)^` + AnnotationSumsPrefix + `([0-9a-f]{64})$`)

// Annotation is the message the TAG OBJECT carries, composed in one place
// because it is written by `cut` and read by `adopt` and the two have to agree
// about where the digest is (the repository owner's decision 2, #1337). A tag is the one
// thing in this repository that cannot be quietly amended, so what it says
// about a release is the most durable record the release has. It also carries a
// waived CI gate, when there was one: the tag is exactly where a person asks
// why a version shipped red, and the extra lines are appended after the digest
// so the `sums=` line stays put (docs/SPEC-RELEASE.md section 19).
func Annotation(version, sha, sumsDigest string, ciWaiver ...string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n\nCut from %s.\n", version, sha)
	if sumsDigest != "" {
		fmt.Fprintf(&b, "%s%s\n", AnnotationSumsPrefix, sumsDigest)
	}
	if len(ciWaiver) > 0 {
		b.WriteString(ciWaiver[0])
	}
	return b.String()
}

// SumsInAnnotation reads the digest back out of a tag's message, or "" when
// the tag carries none -- which is what a release cut before decision 2, or one
// cut without --sums, looks like from here.
func SumsInAnnotation(message string) string {
	m := annotationSums.FindStringSubmatch(message)
	if m == nil {
		return ""
	}
	return m[1]
}

// errTruncated says that the compare could not be classified at all and that
// the refusal has ALREADY been printed, in the shape the remedy needs. It is a
// sentinel rather than an ordinary refusal because this one refusal does not
// read `CUT REFUSED: <prose>`: it is a field line, so that a person or a script
// meeting it in a log can tell `reason=compare-truncated` from every other
// reason a cut can refuse.
var errTruncated = errors.New("compare-truncated")

// classify is the gate the repository owner's decision 1 puts in front of the tag: which of
// the paths this range touched are on SensitivePaths, and may this cut proceed.
// It is its own function, and pure apart from the writers, because the decision
// is the thing worth reading -- the cut around it is bookkeeping.
//
// THE ORDER IS THE WHOLE LESSON OF THE FOURTH DOGFOOD (2026-09-18). That cut's
// compare answered with exactly 300 files -- the forge's ceiling -- and the
// verb refused naming 24 sensitive paths out of the 58 the range really
// touched. It looked like the gate working. It was the gate being lucky: the
// hits it named were the ones that happened to fall inside the prefix it could
// see, and a range whose only sensitive file sat past file 300 would have been
// cut clean. So the truncation is decided FIRST and named FIRST, before
// anything is said about what was found inside a list that may be short.
//
// And --security-read does not get past it. A person's read is a read OF A LIST,
// and a read of a prefix of the truth vouches for a prefix of the truth. The
// way past a truncated compare is a complete list, which is what --local-diff
// and --paths-from are for.
func classify(files []string, complete bool, rangeName, securityRead string, out, errs io.Writer) error {
	if securityRead != "" {
		if err := ValidSecurityRead(securityRead); err != nil {
			return err
		}
	}
	if !complete && len(files) >= CompareFileCap {
		fmt.Fprintf(errs, "RELEASE CUT REFUSED reason=compare-truncated files=%d range=%s remedy=%q\n",
			len(files), field(rangeName),
			fmt.Sprintf("classify from a local `git diff --name-only %s` with --paths-from <file>, produced by `release cut --local-diff <checkout>`", rangeName))
		return errTruncated
	}
	hits := Sensitive(files)
	if len(hits) == 0 {
		// The line exists to mark the exception. Printed every time, it is a
		// line nobody reads, and then it is not a mark at all.
		return nil
	}
	if securityRead == "" {
		return refuse("get a person's read of these paths and name it: --security-read <note id or the url of his comment>",
			"this range touches %s on the sensitive list: %s", plural(len(hits), "path"), namedPaths(hits, 10))
	}
	// ON STDOUT, above the cut line: it is a receipt, not progress. A release
	// that crossed the sensitive list is a fact somebody reads off the
	// terminal today and out of a log in six months, and `read=` is how they
	// find what was actually said.
	fmt.Fprintf(out, "RELEASE CUT SENSITIVE paths=%d read=%s\n", len(hits), field(securityRead))
	return nil
}

// PathsHeaderPrefix is the first line of a path list `release cut --local-diff`
// wrote, and the whole reason --paths-from can be trusted: the rest of the file
// is a classification gate's INPUT, and a gate reading a hand-written input is
// a gate whose answer is whatever somebody remembered. The line also names the
// RANGE, so a list left over from a different pair of commits is refused rather
// than quietly classifying a release that is not this one.
const PathsHeaderPrefix = "# nova-update release cut --local-diff "

// pathsDigestPrefix opens the SECOND header line, the sha256 of the list body
// (the files joined by newlines, in order) as this verb wrote it. The first
// line pins WHICH range the list is for; the digest pins THAT THE LIST IS
// STILL THE ONE PRODUCED FOR IT (docs/SPEC-RELEASE.md section 5, security#72
// finding 10): an unpinned body is a hand-editable one, and whoever deleted
// the sensitive lines between the --local-diff run and the cut walked past
// the gate with no --security-read, silently.
const pathsDigestPrefix = "# sha256 "

// WritePathsFile records the complete list, the range it is the list for, and
// the digest of the list itself. The digest is computed with the package's own
// sumOf (incremental.go).
func WritePathsFile(path, rangeName string, files []string) error {
	body := strings.Join(files, "\n")
	var b strings.Builder
	b.WriteString(PathsHeaderPrefix + rangeName + "\n")
	b.WriteString(pathsDigestPrefix + sumOf([]byte(body)) + "\n")
	if body != "" {
		b.WriteString(body + "\n")
	}
	return writeNoFollow("write paths", path, []byte(b.String()), 0o644)
}

// ReadPathsFile reads one back, and refuses anything this verb did not write:
// the wrong first line, the wrong range, a list without the digest line the
// verb pins its body with, and a body that no longer matches that digest.
func ReadPathsFile(path, rangeName string) ([]string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("cannot read --paths-from %s: %w (produce it with `release cut --local-diff <checkout> --paths-from %s`)", path, err, path)
	}
	lines := strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n")
	header := ""
	if len(lines) > 0 {
		header = strings.TrimSpace(lines[0])
	}
	if !strings.HasPrefix(header, PathsHeaderPrefix) {
		return nil, refuse(fmt.Sprintf("produce it with `release cut --local-diff <checkout> --paths-from %s`", path),
			"%s was not written by `release cut --local-diff`: its first line is not %q", path, PathsHeaderPrefix)
	}
	if got := strings.TrimSpace(strings.TrimPrefix(header, PathsHeaderPrefix)); got != rangeName {
		return nil, refuse(fmt.Sprintf("produce the list for this range: `release cut --local-diff <checkout> --paths-from %s`", path),
			"%s is the path list for %s, and this cut is %s", path, got, rangeName)
	}
	recorded := ""
	var files []string
	for _, line := range lines[1:] {
		if line = strings.TrimSpace(line); line == "" {
			continue
		}
		if strings.HasPrefix(line, pathsDigestPrefix) {
			if recorded == "" {
				recorded = strings.TrimSpace(strings.TrimPrefix(line, pathsDigestPrefix))
			}
			continue
		}
		if strings.HasPrefix(line, "#") {
			continue
		}
		files = append(files, line)
	}
	remedy := fmt.Sprintf("regenerate the list: `release cut --local-diff <checkout> --paths-from %s`", path)
	if recorded == "" {
		return nil, refuse(remedy,
			"%s carries no sha256 digest line, so nothing pins its body to what `release cut --local-diff` wrote", path)
	}
	if got := sumOf([]byte(strings.Join(files, "\n"))); got != recorded {
		return nil, refuse(remedy,
			"%s changed after `release cut --local-diff` wrote it: the sha256 of its list is %s and its header records %s", path, field(got), field(recorded))
	}
	return files, nil
}

// paths answers the list `cut` classifies, and whether that list is COMPLETE.
// Three sources, in the order a person reaches for them: the checkout, a list
// this verb wrote earlier, and the forge, which is the default. Local output
// is byte-bounded; forge output also has a file-count ceiling.
func paths(ctx context.Context, o options, deps Deps, previous, sha string, out, errs io.Writer) ([]string, bool, error) {
	rangeName := previous + "..." + sha
	if previous == "" {
		// No previous tag is no range: the first release of a repository
		// classifies nothing, and asking git or the forge about `...sha`
		// would be asking about every commit that has ever existed.
		if o.localDiff != "" || o.pathsFrom != "" {
			return nil, true, refuse("cut this one without them; there is no range to diff",
				"there is no previous tag, so --local-diff and --paths-from have nothing to classify")
		}
		return nil, true, nil
	}
	switch {
	case o.localDiff != "":
		git := deps.Git
		if git == nil {
			git = ExecGit{}
		}
		progress(errs, "asking git in %s which paths %s touched", o.localDiff, rangeName)
		files, err := git.DiffNames(ctx, o.localDiff, previous, sha)
		if err != nil {
			var limit *diffOutputLimitError
			if errors.As(err, &limit) {
				remedy := "report this range and observed byte count to the release tool maintainer for review of the path-list limit; retry with the reviewed tool"
				if limit.stream == "diagnostics" {
					remedy = fmt.Sprintf("inspect Git's diagnostics for this range in %s, resolve their cause, and retry", o.localDiff)
				}
				return nil, false, fmt.Errorf("cannot classify %s: %w; no path list, changelog or tag written (%s)", rangeName, err, remedy)
			}
			return nil, false, fmt.Errorf("cannot read %s in %s: %w (name a checkout holding both %s and %s; `git fetch --tags` first)", rangeName, o.localDiff, err, previous, sha)
		}
		if o.pathsFrom != "" {
			if err := WritePathsFile(o.pathsFrom, rangeName, files); err != nil {
				return nil, false, fmt.Errorf("cannot write --paths-from %s: %w (name a writable path)", o.pathsFrom, err)
			}
			progress(errs, "wrote the %d-path list to %s", len(files), o.pathsFrom)
		}
		fmt.Fprintf(out, "RELEASE CUT PATHS source=local-diff files=%d range=%s checkout=%s\n", len(files), field(rangeName), field(o.localDiff))
		return files, true, nil
	case o.pathsFrom != "":
		files, err := ReadPathsFile(o.pathsFrom, rangeName)
		if err != nil {
			return nil, false, err
		}
		fmt.Fprintf(out, "RELEASE CUT PATHS source=paths-from files=%d range=%s file=%s\n", len(files), field(rangeName), field(o.pathsFrom))
		return files, true, nil
	}
	forge := deps.Forge
	if forge == nil {
		forge = NewGH(o.timeout)
	}
	progress(errs, "reading which paths %s touched", rangeName)
	files, err := forge.Files(ctx, o.repo, previous, sha)
	if err != nil {
		return nil, false, fmt.Errorf("cannot read the files in %s: %w (ask again when the forge answers)", rangeName, err)
	}
	return files, false, nil
}

// prependSection puts the new section above every other section and below the
// file's title, and creates the file with a title when there is none. The new
// section goes at the TOP because the question a changelog is opened with is
// what changed most recently.
func prependSection(path, section string) error {
	old, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	text := string(old)
	head, rest := "", text
	if strings.HasPrefix(text, "# ") {
		line, after, found := strings.Cut(text, "\n")
		if found {
			head, rest = line+"\n", strings.TrimLeft(after, "\n")
			head += "\n"
		}
	}
	if head == "" {
		head = "# nova-tools changelog\n\n"
		rest = strings.TrimLeft(text, "\n")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return writeNoFollow("write changelog", path, []byte(head+section+rest), 0o644)
}

func cut(ctx context.Context, o options, deps Deps, out, errs io.Writer) int {
	// THE DEFINITION OF DONE, FIRST. Before the forge is asked anything: a
	// release with an open edge will not be cut whatever the forge says, and
	// finding that out after three network reads is three reads spent to
	// arrive at the same no. pkg/release/dogfoodgate.go says why the gate
	// is in front of a tag at all.
	gate, err := dogfoodCheck("CUT", o, deps, filepath.Dir(o.changelog), out, errs)
	if err != nil {
		// The field-line refusal has already been printed, in the shape the
		// remedy needs.
		if errors.Is(err, errDogfood) {
			return 2
		}
		return refusal(errs, "CUT", err)
	}
	forge := deps.Forge
	if forge == nil {
		forge = NewGH(o.timeout)
	}
	progress(errs, "asking %s where %s is", o.repo, o.from)
	sha, err := forge.HeadSHA(ctx, o.repo, o.from)
	if err != nil {
		return refusal(errs, "CUT", fmt.Errorf("cannot read %s of %s: %w (check --repo and --from, and that gh is authenticated)", o.from, o.repo, err))
	}
	progress(errs, "reading the check runs on %s", sha)
	runs, err := readCheckRuns(ctx, forge, o.repo, sha)
	if err != nil {
		return refusal(errs, "CUT", fmt.Errorf("cannot read the checks on %s: %w (ask again when the forge answers)", sha, err))
	}
	progress(errs, "reading the tags")
	tags, err := forge.Tags(ctx, o.repo)
	if err != nil {
		return refusal(errs, "CUT", fmt.Errorf("cannot read the tags of %s: %w (ask again when the forge answers)", o.repo, err))
	}
	var setAside []checkRun
	runs, setAside = sourceChecks(runs, tags, o.version)
	// THE GREEN GATE, AND THE ONE WAY PAST IT. A waiver with an empty value is
	// not a waiver, and a waiver of a green commit is a lie in the tag's
	// permanent record, so both refuse before anything is written
	// (docs/SPEC-RELEASE.md section 19).
	waive := strings.TrimSpace(o.waiveCI)
	if o.waiveCISet && waive == "" {
		return refusal(errs, "CUT", refuse(`name who waives the CI and when: --waive-ci "<who, when>"`,
			"--waive-ci wants a person and a date, and was given an empty value"))
	}
	plain := asCheckRuns(runs)
	ciWaive := ""
	if err := green(plain); err != nil {
		if waive == "" {
			return refusal(errs, "CUT", err)
		}
		ciWaive = ciWaiver(plain, waive)
		fmt.Fprintf(out, "RELEASE CUT CI WAIVED waived=%s checks=%s\n", field(waive), field(ciWaivedChecks(plain)))
	} else if waive != "" {
		if len(setAside) > 0 {
			return refusal(errs, "CUT", refuse("cut without it; nothing here needs waiving",
				"the previous tag's release run is set aside; no waiver is needed"))
		}
		return refusal(errs, "CUT", refuse("cut without it; nothing here needs waiving",
			"--waive-ci waives a red or missing CI, and every check on %s is green", sha))
	}
	// The set-aside record travels with the release beside the waiver: a person
	// asking in six months why a cancelled suite did not block the cut reads the
	// answer in the tag and the changelog (docs/SPEC-RELEASE.md section 19).
	ciRecord := ciWaive
	if aside := ciAsideRecord(setAside); aside != "" {
		ciRecord += aside
	}
	// THE PROMISED RECOVERY JOURNEYS, once the revision is known: evidence is
	// proof about one revision, and this is the one the tag will name.
	// Before --dry-run branches, because what a cut would do is this refusal.
	journeys, err := journeyCheck(o, deps, filepath.Dir(o.changelog), sha, out, errs)
	if err != nil {
		if errors.Is(err, errJourney) {
			return 2
		}
		return refusal(errs, "CUT", err)
	}
	for _, tag := range tags {
		if tag == o.version {
			return refusal(errs, "CUT", refuse("choose the next version, or delete the tag deliberately",
				"%s is already a tag in %s", o.version, o.repo))
		}
	}
	previous := previousTag(tags)
	// THE SPEND THE STORE RECORDED, against each provider's own over the window since the
	// previous tag (spendcheck.go). Before --dry-run branches: what a cut would do is this
	// refusal.
	spend, err := spendCheck(ctx, o, deps, forge, previous, errs)
	if err != nil {
		if errors.Is(err, errSpend) {
			return 2
		}
		return refusal(errs, "CUT", err)
	}
	var commits []Commit
	if previous != "" {
		progress(errs, "reading what merged since %s", previous)
		commits, err = forge.Compare(ctx, o.repo, previous, sha)
		if err != nil {
			return refusal(errs, "CUT", fmt.Errorf("cannot compare %s...%s: %w (ask again when the forge answers)", previous, sha, err))
		}
	}
	prs := PullRequests(commits)
	// WHICH PATHS THE RANGE TOUCHED, and whether that needs a read before a
	// tag exists (the repository owner's decision 1, #1337). Asked BEFORE --dry-run branches
	// and before anything is written: a dry run exists to find out what would
	// happen, and what would happen is this refusal.
	files, complete, err := paths(ctx, o, deps, previous, sha, out, errs)
	if err != nil {
		return refusal(errs, "CUT", err)
	}
	if err := classify(files, complete, previous+"..."+sha, o.securityRead, out, errs); err != nil {
		// A truncated compare has already said so, in its own field line.
		if errors.Is(err, errTruncated) {
			return 2
		}
		return refusal(errs, "CUT", err)
	}
	// --sums names a SHA256SUMS this release's build already wrote; its digest
	// is recorded in the section so that an adopt on another host can check a
	// fetched release against something that did not travel with the bits.
	sumsDigest := ""
	if o.sums != "" {
		if sumsDigest, err = fileSum(o.sums); err != nil {
			return refusal(errs, "CUT", fmt.Errorf("cannot read %s: %w (name the SHA256SUMS that `release build` wrote, or leave --sums out)", o.sums, err))
		}
	}
	section := sectionWith(o.version, sha, previous, sumsDigest, dogfoodWaiver(gate, o.reason), journeys.section()+spend.Section, deps.Now(), prs, ciRecord)
	aside := ""
	if len(setAside) > 0 {
		aside = " ci-set-aside=" + field(ciSetAside(setAside))
	}
	if o.dryRun {
		fmt.Fprintf(out, "RELEASE CUT version=%s sha=%s prs=%d previous=%s changelog=%s sums=%s dogfood=%s journeys=%s spend=%s dry-run=yes%s\n",
			field(o.version), field(sha), len(prs), field(previous), field(o.changelog), field(sumsDigest), gate, journeys.State, spend.State, aside)
		fmt.Fprint(errs, section)
		return 0
	}
	if err := prependSection(o.changelog, section); err != nil {
		return refusal(errs, "CUT", fmt.Errorf("cannot write %s: %w (name a writable --changelog)", o.changelog, err))
	}
	progress(errs, "tagging %s at %s, annotated with the digest adopt will check", o.version, sha)
	if err := forge.Tag(ctx, o.repo, o.version, sha, Annotation(o.version, sha, sumsDigest, ciRecord)); err != nil {
		// The changelog is already written; say so, because the remedy is to
		// tag by hand or to cut again, not to wonder which half happened.
		fmt.Fprintf(errs, "CUT FAILED version=%s sha=%s: %s (the changelog section is written at %s; create the tag by hand or delete the section and cut again)\n",
			field(o.version), field(sha), oneline.Err(err), field(o.changelog))
		return 1
	}
	fmt.Fprintf(out, "RELEASE CUT version=%s sha=%s prs=%d previous=%s changelog=%s sums=%s dogfood=%s journeys=%s spend=%s dry-run=no%s\n",
		field(o.version), field(sha), len(prs), field(previous), field(o.changelog), field(sumsDigest), gate, journeys.State, spend.State, aside)
	return 0
}
