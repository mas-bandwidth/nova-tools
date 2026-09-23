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

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
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

// PullRequests reads a compare range as a changelog. One commit whose subject
// ends in `(#n)` is one pull request; the rest of the message, if it names other
// pull requests, is that entry's member list.
func PullRequests(commits []Commit) []PR {
	var prs []PR
	for _, c := range commits {
		subject, body, _ := strings.Cut(strings.TrimSpace(c.Message), "\n")
		subject = strings.TrimSpace(subject)
		m := prNumber.FindStringSubmatch(subject)
		if m == nil {
			continue
		}
		n, err := strconv.Atoi(m[1])
		if err != nil {
			continue
		}
		pr := PR{Number: n, Title: strings.TrimSpace(strings.TrimSuffix(subject, m[0]))}
		seen := map[int]bool{n: true}
		for _, mm := range memberNumber.FindAllStringSubmatch(body, -1) {
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
func green(runs []CheckRun) error {
	if len(runs) == 0 {
		return refuse("dispatch CI on this commit and cut again when it is green",
			"no check run has judged this commit")
	}
	var pending, failing []string
	for _, r := range runs {
		switch {
		case r.Status != "completed":
			pending = append(pending, r.Name)
		case r.Conclusion != "success" && r.Conclusion != "skipped" && r.Conclusion != "neutral":
			failing = append(failing, r.Name+"="+r.Conclusion)
		}
	}
	sort.Strings(pending)
	sort.Strings(failing)
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

// Section renders one changelog section. It is exported and pure so that the
// shape of what a release says about itself is asserted by a test rather than
// by reading a file somebody wrote by hand afterwards.
func Section(version, sha, previous, sumsDigest, dogfoodWaiver string, when time.Time, prs []PR) string {
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
	// (Johnny, 2026-09-18). It is written in the form the check wants, so
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
// about where the digest is (Johnny's decision 2, #1337). A tag is the one
// thing in this repository that cannot be quietly amended, so what it says
// about a release is the most durable record the release has.
func Annotation(version, sha, sumsDigest string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n\nCut from %s.\n", version, sha)
	if sumsDigest != "" {
		fmt.Fprintf(&b, "%s%s\n", AnnotationSumsPrefix, sumsDigest)
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

// classify is the gate Johnny's decision 1 puts in front of the tag: which of
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
// And --security-read does not get past it. Johnny's read is a read OF A LIST,
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
		return refuse("get Johnny's read of these paths and name it: --security-read <note id or the url of his comment>",
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

// WritePathsFile records the complete list and the range it is the list for.
func WritePathsFile(path, rangeName string, files []string) error {
	var b strings.Builder
	b.WriteString(PathsHeaderPrefix + rangeName + "\n")
	for _, f := range files {
		b.WriteString(f + "\n")
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

// ReadPathsFile reads one back, and refuses anything this verb did not write.
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
	var files []string
	for _, line := range lines[1:] {
		if line = strings.TrimSpace(line); line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		files = append(files, line)
	}
	return files, nil
}

// paths answers the list `cut` classifies, and whether that list is COMPLETE.
// Three sources, in the order a person reaches for them: the checkout, a list
// this verb wrote earlier, and the forge -- which is the default and the only
// one with a ceiling.
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
	return os.WriteFile(path, []byte(head+section+rest), 0o644)
}

func cut(ctx context.Context, o options, deps Deps, out, errs io.Writer) int {
	// THE DEFINITION OF DONE, FIRST. Before the forge is asked anything: a
	// release with an open edge will not be cut whatever the forge says, and
	// finding that out after three network reads is three reads spent to
	// arrive at the same no. internal/release/dogfoodgate.go says why the gate
	// is in front of a tag at all.
	gate, err := dogfoodCheck("CUT", o, deps, filepath.Join(filepath.Dir(o.changelog), "docs", "CLI.md"), out, errs)
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
	runs, err := forge.CheckRuns(ctx, o.repo, sha)
	if err != nil {
		return refusal(errs, "CUT", fmt.Errorf("cannot read the checks on %s: %w (ask again when the forge answers)", sha, err))
	}
	if err := green(runs); err != nil {
		return refusal(errs, "CUT", err)
	}
	progress(errs, "reading the tags")
	tags, err := forge.Tags(ctx, o.repo)
	if err != nil {
		return refusal(errs, "CUT", fmt.Errorf("cannot read the tags of %s: %w (ask again when the forge answers)", o.repo, err))
	}
	for _, tag := range tags {
		if tag == o.version {
			return refusal(errs, "CUT", refuse("choose the next version, or delete the tag deliberately",
				"%s is already a tag in %s", o.version, o.repo))
		}
	}
	previous := previousTag(tags)
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
	// tag exists (Johnny's decision 1, #1337). Asked BEFORE --dry-run branches
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
	section := Section(o.version, sha, previous, sumsDigest, dogfoodWaiver(gate, o.reason), deps.Now(), prs)
	if o.dryRun {
		fmt.Fprintf(out, "RELEASE CUT version=%s sha=%s prs=%d previous=%s changelog=%s sums=%s dogfood=%s dry-run=yes\n",
			field(o.version), field(sha), len(prs), field(previous), field(o.changelog), field(sumsDigest), gate)
		fmt.Fprint(errs, section)
		return 0
	}
	if err := prependSection(o.changelog, section); err != nil {
		return refusal(errs, "CUT", fmt.Errorf("cannot write %s: %w (name a writable --changelog)", o.changelog, err))
	}
	progress(errs, "tagging %s at %s, annotated with the digest adopt will check", o.version, sha)
	if err := forge.Tag(ctx, o.repo, o.version, sha, Annotation(o.version, sha, sumsDigest)); err != nil {
		// The changelog is already written; say so, because the remedy is to
		// tag by hand or to cut again, not to wonder which half happened.
		fmt.Fprintf(errs, "CUT FAIL version=%s sha=%s: %s (the changelog section is written at %s; create the tag by hand or delete the section and cut again)\n",
			field(o.version), field(sha), oneline.Err(err), field(o.changelog))
		return 1
	}
	fmt.Fprintf(out, "RELEASE CUT version=%s sha=%s prs=%d previous=%s changelog=%s sums=%s dogfood=%s dry-run=no\n",
		field(o.version), field(sha), len(prs), field(previous), field(o.changelog), field(sumsDigest), gate)
	return 0
}
