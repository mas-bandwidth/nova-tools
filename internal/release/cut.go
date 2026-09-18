package release

import (
	"context"
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
func Section(version, sha, previous string, when time.Time, prs []PR) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## %s — %s\n\n", version, when.UTC().Format("2006-01-02"))
	since := "this repository's first commit"
	if previous != "" {
		since = previous
	}
	fmt.Fprintf(&b, "Cut from %s. %s since %s.\n\n", sha, plural(len(prs), "pull request"), since)
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
	section := Section(o.version, sha, previous, deps.Now(), prs)
	if o.dryRun {
		fmt.Fprintf(out, "RELEASE CUT version=%s sha=%s prs=%d previous=%s changelog=%s dry-run=yes\n",
			field(o.version), field(sha), len(prs), field(previous), field(o.changelog))
		fmt.Fprint(errs, section)
		return 0
	}
	if err := prependSection(o.changelog, section); err != nil {
		return refusal(errs, "CUT", fmt.Errorf("cannot write %s: %w (name a writable --changelog)", o.changelog, err))
	}
	progress(errs, "tagging %s at %s", o.version, sha)
	if err := forge.Tag(ctx, o.repo, o.version, sha); err != nil {
		// The changelog is already written; say so, because the remedy is to
		// tag by hand or to cut again, not to wonder which half happened.
		fmt.Fprintf(errs, "CUT FAIL version=%s sha=%s: %s (the changelog section is written at %s; create the tag by hand or delete the section and cut again)\n",
			field(o.version), field(sha), oneline.Err(err), field(o.changelog))
		return 1
	}
	fmt.Fprintf(out, "RELEASE CUT version=%s sha=%s prs=%d previous=%s changelog=%s dry-run=no\n",
		field(o.version), field(sha), len(prs), field(previous), field(o.changelog))
	return 0
}
