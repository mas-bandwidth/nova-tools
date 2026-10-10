package release

import (
	"context"
	"encoding/json"
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

// certificationRuns keeps the certification.yml evidence out of a list of runs
// that also carries ordinary CI. The workflow-runs read returns certification
// runs only, but the check-runs fallback (readCertRuns) does not, so the filter
// lives here where both paths share it.
func certificationRuns(runs []CertRun) []CertRun {
	var out []CertRun
	for _, r := range runs {
		if r.Name == "certification" || r.Name == "certification-ok" {
			out = append(out, r)
		}
	}
	return out
}

// CertRun is one certification.yml workflow run on a commit, the evidence
// `cut` reads before a tag. It is the same evidence release.yml reads through
// `go run ./tools/ghrelease certified` (tools/ghrelease/certified.go): the
// run's status and conclusion, and the UpdatedAt stamp that orders it. The
// stamp, not a run id or an array position, decides which evidence is newest,
// because a rerun of an older run is newer evidence than a later run never
// rerun.
type CertRun struct {
	Name       string
	Status     string // queued, waiting, in_progress, completed
	Conclusion string // success, failure, cancelled, timed_out, skipped, neutral
	UpdatedAt  string // RFC3339 UTC; the newest run decides
}

// CertificationRuns reads certification.yml's runs on sha. It asks the
// workflow-runs endpoint rather than the check-runs endpoint because the run's
// own name and its updated_at are what the gate orders evidence by, and the
// answer is one snapshot: every run on the commit is fetched once, so a run
// that finishes red between two asks cannot slip between them.
func (g *GH) CertificationRuns(ctx context.Context, repo, sha string) ([]CertRun, error) {
	out, err := g.api(ctx, "api", "--paginate",
		"repos/"+repo+"/actions/workflows/certification.yml/runs?head_sha="+sha+"&per_page=100",
		"--jq", ".workflow_runs[] | {Name:.name, Status:.status, Conclusion:.conclusion, UpdatedAt:.updated_at}")
	if err != nil {
		return nil, err
	}
	var runs []CertRun
	dec := json.NewDecoder(strings.NewReader(out))
	for {
		var r CertRun
		if err := dec.Decode(&r); err == io.EOF {
			break
		} else if err != nil {
			return nil, fmt.Errorf("cannot read gh's certification runs: %w", err)
		}
		runs = append(runs, r)
	}
	return runs, nil
}

// DispatchWorkflow requests that GitHub run the named workflow on ref. It is
// the one mutation the certification gate needs: `cut` offers to dispatch
// certification.yml on the exact sha being cut and wait for it, rather than
// telling the reader to run the command by hand.
func (g *GH) DispatchWorkflow(ctx context.Context, repo, workflow, ref string) error {
	_, err := g.api(ctx, "workflow", "run", workflow, "-R", repo, "--ref", ref)
	return err
}

// certReader is the forge question the certification gate needs: every
// certification.yml run on a commit, with the updated_at stamp that orders it.
// It is a separate interface rather than a Forge method so a forge that cannot
// answer it still builds; readCertRuns falls back to CheckRuns then.
type certReader interface {
	CertificationRuns(ctx context.Context, repo, sha string) ([]CertRun, error)
}

// readCertRuns is certification.yml's runs on sha, from the workflow-runs
// endpoint when the forge answers it and from the ordinary check runs when it
// does not. The fallback cannot order evidence by updated_at (CheckRun carries
// no stamp), so every certification run it finds decides together.
func readCertRuns(ctx context.Context, forge Forge, repo, sha string) ([]CertRun, error) {
	if r, ok := forge.(certReader); ok {
		return r.CertificationRuns(ctx, repo, sha)
	}
	runs, err := forge.CheckRuns(ctx, repo, sha)
	if err != nil {
		return nil, err
	}
	var out []CertRun
	for _, r := range runs {
		if r.Name == "certification" || r.Name == "certification-ok" {
			out = append(out, CertRun{Name: r.Name, Status: r.Status, Conclusion: r.Conclusion})
		}
	}
	return out, nil
}

// newestCertification returns every certification run sharing the latest
// UpdatedAt: the group that decides. The stamp, not a run id or an array
// position, orders certification evidence, because a rerun of an older run id
// carries a newer stamp than a later run that was never rerun. Two runs can
// share the maximal stamp to the second, and the stamp supplies no order
// between them, so the whole group is returned and must be uniformly green.
func newestCertification(certs []CertRun) []CertRun {
	max := ""
	for _, r := range certs {
		if r.UpdatedAt > max {
			max = r.UpdatedAt
		}
	}
	var group []CertRun
	for _, r := range certs {
		if r.UpdatedAt == max {
			group = append(group, r)
		}
	}
	return group
}

// certified is the release gate. A commit must be vouched for by certification.yml
// before a release can be cut, and it is the same check release.yml makes
// through `go run ./tools/ghrelease certified`: every certification run on the
// commit must have completed, and the run carrying the latest update must be
// green. An older green does not vouch for a newer red or a still-running run,
// because a tag release.yml then refuses is exactly the release cut by hand this
// gate exists to prevent. The waivers (dogfood, journey, spend) never cover
// certification.
func certified(runs []CertRun, sha string) error {
	certs := certificationRuns(runs)
	if len(certs) == 0 {
		cmd := fmt.Sprintf("gh workflow run certification.yml --ref %s", sha)
		return refuse(fmt.Sprintf("certify it first: %s", cmd),
			"no certification run has vouched for this commit: run %s", cmd)
	}
	for _, r := range certs {
		if r.Status != "completed" {
			return refuse("wait for certification to finish and cut again",
				"certification.yml is still running on this commit")
		}
	}
	latest := newestCertification(certs)
	for _, r := range latest {
		if r.Conclusion != "success" {
			return refuse("fix the certification failure and cut again; a tag cannot be amended",
				"certification.yml failed on this commit: %s", r.Conclusion)
		}
	}
	return nil
}

// isCertificationFailure reports whether certification has finished on this
// commit and its newest evidence is red -- the one outcome the dispatch loop
// must not keep polling for. A run still in flight, or no run at all, is not a
// failure.
func isCertificationFailure(runs []CertRun) bool {
	certs := certificationRuns(runs)
	if len(certs) == 0 {
		return false
	}
	for _, r := range certs {
		if r.Status != "completed" {
			return false
		}
	}
	for _, r := range newestCertification(certs) {
		if r.Conclusion != "success" {
			return true
		}
	}
	return false
}

func restStep(seam func(time.Duration), d time.Duration) {
	if seam != nil {
		seam(d)
		return
	}
	time.Sleep(d)
}

// workflowDispatcher is the one forge mutation the certification gate needs:
// ask GitHub to run a workflow on a ref. Like certReader it is a separate
// interface so a forge that cannot dispatch still builds.
type workflowDispatcher interface {
	DispatchWorkflow(ctx context.Context, repo, workflow, ref string) error
}

// dispatchRequested reports whether `cut` should dispatch certification.yml on
// an uncertified commit and wait, rather than refuse naming the manual command.
// An automated cutter opts in once in its environment; an interactive cutter
// gets the refusal and runs the named command itself.
func dispatchRequested() bool {
	return os.Getenv("NOVA_RELEASE_DISPATCH_CERTIFICATION") == "1"
}

// dispatchCertification is the dispatch-and-wait loop: dispatch
// certification.yml on the exact sha, then poll the commit's certification
// runs until the newest evidence is uniformly green, giving up at once when
// that evidence turns red. It is a separate function so its single dispatch and
// its polling are asserted by a test rather than by reading cut's body.
func dispatchCertification(ctx context.Context, forge Forge, repo, sha string, sleep func(time.Duration)) error {
	d, ok := forge.(workflowDispatcher)
	if !ok {
		return refuse("dispatch certification.yml by hand and cut again",
			"this forge cannot dispatch a workflow")
	}
	if err := d.DispatchWorkflow(ctx, repo, "certification.yml", sha); err != nil {
		return fmt.Errorf("cannot dispatch certification.yml: %w", err)
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		restStep(sleep, 100*time.Millisecond)
		certs, err := readCertRuns(ctx, forge, repo, sha)
		if err != nil {
			return err
		}
		if err := certified(certs, sha); err == nil {
			return nil
		} else if isCertificationFailure(certs) {
			return err
		}
	}
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

// sectionWith renders one changelog section, with what the journey gate found, which
// is already in the section's words (JourneyRecord.section; "" when it found nothing to
// say). It is pure so that the shape of what a release says about itself is asserted by
// a test rather than by reading a file somebody wrote by hand afterwards.
func sectionWith(version, sha, previous, sumsDigest, dogfoodWaiver, journeys string, when time.Time, prs []PR) string {
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
	// arrive at the same no. internal/release/dogfoodgate.go says why the gate
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
	runs, err := forge.CheckRuns(ctx, o.repo, sha)
	if err != nil {
		return refusal(errs, "CUT", fmt.Errorf("cannot read the checks on %s: %w (ask again when the forge answers)", sha, err))
	}
	// EVERYTHING EXCEPT CERTIFICATION FIRST. The certification runs are
	// certification.yml's to judge, specifically; green() judges the rest.
	var nonCertRuns []CheckRun
	for _, r := range runs {
		if r.Name != "certification" && r.Name != "certification-ok" {
			nonCertRuns = append(nonCertRuns, r)
		}
	}
	if err := green(nonCertRuns); err != nil {
		return refusal(errs, "CUT", err)
	}

	// CERTIFICATION LAST, AND THE WAIVERS DO NOT COVER IT. The commit must be
	// vouched for by certification.yml -- the same check release.yml makes
	// through `go run ./tools/ghrelease certified` -- before a tag exists,
	// because a tag is the one thing here that cannot be amended.
	certs, err := readCertRuns(ctx, forge, o.repo, sha)
	if err != nil {
		return refusal(errs, "CUT", fmt.Errorf("cannot read the certification runs on %s: %w (ask again when the forge answers)", sha, err))
	}
	if err := certified(certs, sha); err != nil {
		if !dispatchRequested() {
			return refusal(errs, "CUT", err)
		}
		progress(errs, "dispatching certification.yml on %s", sha)
		if derr := dispatchCertification(ctx, forge, o.repo, sha, nil); derr != nil {
			return refusal(errs, "CUT", derr)
		}
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
	section := sectionWith(o.version, sha, previous, sumsDigest, dogfoodWaiver(gate, o.reason), journeys.section()+spend.Section, deps.Now(), prs)
	if o.dryRun {
		fmt.Fprintf(out, "RELEASE CUT version=%s sha=%s prs=%d previous=%s changelog=%s sums=%s dogfood=%s journeys=%s spend=%s dry-run=yes publish=workflow\n",
			field(o.version), field(sha), len(prs), field(previous), field(o.changelog), field(sumsDigest), gate, journeys.State, spend.State)
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
		fmt.Fprintf(errs, "CUT FAILED version=%s sha=%s: %s (the changelog section is written at %s; create the tag by hand or delete the section and cut again)\n",
			field(o.version), field(sha), oneline.Err(err), field(o.changelog))
		return 1
	}
	fmt.Fprintf(out, "RELEASE CUT version=%s sha=%s prs=%d previous=%s changelog=%s sums=%s dogfood=%s journeys=%s spend=%s dry-run=no publish=workflow\n",
		field(o.version), field(sha), len(prs), field(previous), field(o.changelog), field(sumsDigest), gate, journeys.State, spend.State)
	return 0
}
