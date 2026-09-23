package pulse

// gate-facts is the G1/G2/G3 stamp harvest writes on a PR body: ci-ok at the head,
// merge-tree against the landing base, and the card PATHS versus
// `git diff --name-only base...head`. One line. A conflicting merge-tree is
// merge-tree=conflict and exit 2; a clean tree is merge-tree=clean and exit 0
// when ci-ok is success and the diff stays inside PATHS. A --rollup file and a
// live gh rollup are evidence for one commit: head or headRefOid must be the
// resolved --head, or the stamp is refused.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	hyg "github.com/mas-bandwidth/nova-tools/internal/hygiene"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// CISource is G1. The shipped sources are a --rollup file, a gh pr view, or
// pending when neither is named. Tests inject a fake; they never call GitHub.
// head is the resolved commit the stamp names. A source that cannot show the
// checks are for that commit returns an error instead of another revision's status.
type CISource interface {
	CI(repo string, pr int, head string) (status, job, test string, err error)
}

// GateFactsInput is the verb's input, held apart from flag parsing.
type GateFactsInput struct {
	Dir         string
	Base        string
	Head        string
	PR          int
	Repo        string // owner/name, only for a live gh rollup
	Card        string
	Paths       string // comma-separated globs, or "none"; empty means read --card
	Rollup      string
	ReceiptFile string
	Timeout     time.Duration
	Max         int // 0 = all extra/conflict names
	Source      CISource
	Stdout      io.Writer
	Stderr      io.Writer
}

const (
	gateFactsToken = "GATEFACTS"
	ciOKSuccess    = "success"
	ciOKFailure    = "failure"
	ciOKPending    = "pending"
	mergeClean     = "clean"
	mergeConflict  = "conflict"
	pathsOK        = "ok"
	pathsExtra     = "extra"
	pathsSkip      = "-"
)

// GateFacts prints one GATEFACTS receipt and returns the exit code.
func GateFacts(in GateFactsInput) int {
	if in.Stdout == nil {
		in.Stdout = io.Discard
	}
	if in.Stderr == nil {
		in.Stderr = io.Discard
	}
	if in.Timeout <= 0 {
		in.Timeout = 120 * time.Second
	}
	for _, need := range []struct{ value, name, wants string }{
		{in.Dir, "dir", "the git working copy this stamp reads"},
		{in.Base, "base", "the landing-base ref, such as origin/dev"},
		{in.Head, "head", "the head commit or branch to stamp"},
	} {
		if strings.TrimSpace(need.value) == "" {
			fmt.Fprintf(in.Stderr, "GATEFACTS REFUSED: missing --%s; refusing to guess (%s)\n", need.name, need.wants)
			return 2
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), in.Timeout)
	defer cancel()

	if _, err := os.Stat(filepath.Join(in.Dir, ".git")); err != nil {
		fmt.Fprintf(in.Stderr, "GATEFACTS REFUSED dir=%s: not a git working copy (pass --dir <clone>)\n", oneline.Field(in.Dir))
		return 2
	}

	baseSHA, err := gitLine(ctx, in.Dir, "rev-parse", "--verify", in.Base+"^{commit}")
	if err != nil {
		fmt.Fprintf(in.Stderr, "GATEFACTS REFUSED base=%s: %s (the landing base must resolve in --dir)\n", oneline.Field(in.Base), oneline.Err(err))
		return 2
	}
	headSHA, err := gitLine(ctx, in.Dir, "rev-parse", "--verify", in.Head+"^{commit}")
	if err != nil {
		fmt.Fprintf(in.Stderr, "GATEFACTS REFUSED head=%s: %s (the head must resolve in --dir)\n", oneline.Field(in.Head), oneline.Err(err))
		return 2
	}

	tree, files, err := mergeTree(ctx, in.Dir, in.Base, in.Head)
	if err != nil {
		fmt.Fprintf(in.Stderr, "GATEFACTS REFUSED merge-tree: %s (git merge-tree --write-tree against --base)\n", oneline.Err(err))
		return 2
	}

	src := in.Source
	if src == nil {
		src = defaultCISource(in)
	}
	ciOK, job, test, err := src.CI(in.Repo, in.PR, headSHA)
	if err != nil {
		var bind *ciHeadError
		if errors.As(err, &bind) {
			fmt.Fprintf(in.Stderr, "GATEFACTS REFUSED ci-ok: %s\n", oneline.Err(err))
		} else {
			fmt.Fprintf(in.Stderr, "GATEFACTS REFUSED ci-ok: %s (pass --rollup <file>, or --pr with --repo)\n", oneline.Err(err))
		}
		return 2
	}
	ciOK, job, test, err = normalizeCIOK(ciOK, job, test)
	if err != nil {
		fmt.Fprintf(in.Stderr, "GATEFACTS REFUSED ci-ok: %s\n", oneline.Err(err))
		return 2
	}

	pathState, extra, err := diffVsPaths(ctx, in)
	if err != nil {
		fmt.Fprintf(in.Stderr, "GATEFACTS REFUSED paths: %s\n", oneline.Err(err))
		return 2
	}

	line := formatGateFacts(gateFactsLine{
		CIOK: ciOK, Job: job, Test: test,
		MergeTree: tree, Files: files,
		Paths: pathState, Extra: extra,
		Base: baseSHA, Head: headSHA, PR: in.PR,
		Max: in.Max,
	})
	if in.ReceiptFile != "" {
		if err := os.WriteFile(in.ReceiptFile, []byte(line+"\n"), 0o644); err != nil {
			fmt.Fprintf(in.Stderr, "GATEFACTS REFUSED receipt-file=%s: %s (the stamp could not be written)\n", oneline.Field(in.ReceiptFile), oneline.Err(err))
			return 2
		}
	}
	fmt.Fprintln(in.Stdout, line)

	if tree == mergeConflict {
		return 2
	}
	if ciOK != ciOKSuccess || pathState == pathsExtra {
		return 1
	}
	return 0
}

type gateFactsLine struct {
	CIOK, Job, Test string
	MergeTree       string
	Files           []string
	Paths           string
	Extra           []string
	Base, Head      string
	PR              int
	Max             int
}

func formatGateFacts(l gateFactsLine) string {
	verdict := "OK"
	if l.MergeTree == mergeConflict || l.CIOK != ciOKSuccess || l.Paths == pathsExtra {
		verdict = "FAIL"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s ci-ok=%s", gateFactsToken, verdict, oneline.Field(l.CIOK))
	if l.CIOK != ciOKSuccess {
		fmt.Fprintf(&b, " job=%s test=%s", oneline.Field(dash(l.Job)), oneline.Field(dash(l.Test)))
	}
	fmt.Fprintf(&b, " merge-tree=%s files=%s", oneline.Field(l.MergeTree), joinCapped(l.Files, l.Max))
	if n := len(l.Files); n > 0 {
		fmt.Fprintf(&b, " conflicts=%d", n)
	}
	fmt.Fprintf(&b, " paths=%s extra=%s", oneline.Field(l.Paths), joinCapped(l.Extra, l.Max))
	if n := len(l.Extra); n > 0 {
		fmt.Fprintf(&b, " extra-n=%d", n)
	}
	fmt.Fprintf(&b, " base=%s head=%s", oneline.Field(sha12(l.Base)), oneline.Field(sha12(l.Head)))
	if l.PR > 0 {
		fmt.Fprintf(&b, " pr=%d", l.PR)
	}
	return b.String()
}

func joinCapped(paths []string, max int) string {
	if len(paths) == 0 {
		return "-"
	}
	sort.Strings(paths)
	n := len(paths)
	if max > 0 && n > max {
		n = max
	}
	parts := make([]string, n)
	for i := 0; i < n; i++ {
		parts[i] = oneline.Field(paths[i])
	}
	return strings.Join(parts, ",")
}

func normalizeCIOK(status, job, test string) (string, string, string, error) {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case ciOKSuccess:
		return ciOKSuccess, "", "", nil
	case ciOKFailure:
		return ciOKFailure, dash(job), dash(test), nil
	case ciOKPending, "":
		return ciOKPending, dash(job), dash(test), nil
	default:
		return "", "", "", fmt.Errorf("ci-ok is success, failure or pending, got %s", oneline.Field(status))
	}
}

func defaultCISource(in GateFactsInput) CISource {
	if strings.TrimSpace(in.Rollup) != "" {
		return fileCISource{path: in.Rollup}
	}
	if in.PR > 0 {
		return ghCISource{timeout: in.Timeout}
	}
	return pendingCISource{}
}

type pendingCISource struct{}

func (pendingCISource) CI(string, int, string) (string, string, string, error) {
	return ciOKPending, "-", "-", nil
}

// fileCISource is --rollup: the check rollup as JSON, so a test never reaches gh.
// The file is evidence for one commit. Compact form names it in "head"; the gh
// form names it in "headRefOid". A missing or different id is not this revision.
// Compact form: {"ci-ok":"failure","job":"...","test":"...","head":"<sha>"}.
// gh form: {"headRefOid":"<sha>","statusCheckRollup":[{"name":"ci-ok","status":"COMPLETED","conclusion":"SUCCESS"}]}.
type fileCISource struct{ path string }

type rollupFile struct {
	CIOK              string       `json:"ci-ok"`
	Job               string       `json:"job"`
	Test              string       `json:"test"`
	Head              string       `json:"head"`
	HeadRefOid        string       `json:"headRefOid"`
	StatusCheckRollup []rollupItem `json:"statusCheckRollup"`
}

type rollupItem struct {
	Name       string `json:"name"`
	Context    string `json:"context"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	State      string `json:"state"`
}

func (f fileCISource) CI(_ string, _ int, head string) (string, string, string, error) {
	raw, err := os.ReadFile(f.path)
	if err != nil {
		return "", "", "", fmt.Errorf("cannot read --rollup %s: %s", f.path, oneline.Err(err))
	}
	var src rollupFile
	if err := json.Unmarshal(raw, &src); err != nil {
		return "", "", "", fmt.Errorf("--rollup %s is not the rollup json: %s", f.path, oneline.Err(err))
	}
	return acceptRollup(head, src)
}

// ciHeadError is a rollup that is not this revision's checks. It is a refusal,
// not a ci-ok value: another commit's success must not be printed beside this head.
type ciHeadError struct{ reason string }

func (e *ciHeadError) Error() string { return e.reason }

// acceptRollup folds a rollup only after its head id matches the resolved commit.
func acceptRollup(head string, src rollupFile) (string, string, string, error) {
	if err := requireSameHead(head, src); err != nil {
		return "", "", "", err
	}
	if src.CIOK != "" {
		return src.CIOK, src.Job, src.Test, nil
	}
	status, job := foldCIOK(src.StatusCheckRollup)
	test := src.Test
	if job == "" {
		job = src.Job
	}
	return status, job, test, nil
}

// requireSameHead refuses a rollup that does not name the resolved head.
// head and headRefOid are the same claim; when both are set they must agree.
func requireSameHead(want string, src rollupFile) error {
	got, err := rollupOID(src)
	if err != nil {
		return err
	}
	want = strings.TrimSpace(want)
	if want == "" {
		return &ciHeadError{reason: "ci-ok has no resolved head (pass --head that resolves to a commit)"}
	}
	if got == "" {
		return &ciHeadError{reason: fmt.Sprintf(
			"rollup names no head (put head or headRefOid of %s in the file; an unbound rollup is not this revision)",
			showOID(want))}
	}
	if !strings.EqualFold(want, got) {
		return &ciHeadError{reason: fmt.Sprintf(
			"rollup head %s is not requested head %s (refusing another revision's checks)",
			showOID(got), showOID(want))}
	}
	return nil
}

func rollupOID(src rollupFile) (string, error) {
	h := strings.TrimSpace(src.Head)
	o := strings.TrimSpace(src.HeadRefOid)
	if h != "" && o != "" && !strings.EqualFold(h, o) {
		return "", &ciHeadError{reason: fmt.Sprintf(
			"rollup head %s and headRefOid %s disagree (name one commit)",
			showOID(h), showOID(o))}
	}
	if o != "" {
		return o, nil
	}
	return h, nil
}

func showOID(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 64 {
		return s[:64]
	}
	return s
}

func foldCIOK(items []rollupItem) (status, job string) {
	if len(items) == 0 {
		return ciOKPending, ""
	}
	var ci *rollupItem
	var failing []string
	pending := false
	for i := range items {
		it := &items[i]
		name := it.Name
		if name == "" {
			name = it.Context
		}
		bucket := rollupBucket(it.Status, it.Conclusion, it.State)
		if strings.EqualFold(name, "ci-ok") {
			ci = it
		}
		switch bucket {
		case ciOKFailure:
			if !strings.EqualFold(name, "ci-ok") && name != "" {
				failing = append(failing, name)
			}
		case ciOKPending:
			pending = true
		}
	}
	if ci == nil {
		if pending {
			return ciOKPending, first(failing)
		}
		if len(failing) > 0 {
			return ciOKFailure, failing[0]
		}
		return ciOKPending, ""
	}
	switch rollupBucket(ci.Status, ci.Conclusion, ci.State) {
	case ciOKSuccess:
		return ciOKSuccess, ""
	case ciOKFailure:
		return ciOKFailure, first(failing)
	default:
		return ciOKPending, first(failing)
	}
}

func rollupBucket(status, conclusion, state string) string {
	c := strings.ToUpper(strings.TrimSpace(conclusion))
	s := strings.ToUpper(strings.TrimSpace(status))
	st := strings.ToUpper(strings.TrimSpace(state))
	switch c {
	case "FAILURE", "CANCELLED", "CANCELED", "TIMED_OUT", "ACTION_REQUIRED", "STARTUP_FAILURE":
		return ciOKFailure
	case "SUCCESS", "NEUTRAL", "SKIPPED":
		if s != "" && s != "COMPLETED" {
			return ciOKPending
		}
		return ciOKSuccess
	}
	switch st {
	case "FAILURE", "ERROR":
		return ciOKFailure
	case "SUCCESS", "PENDING", "EXPECTED":
		if st == "SUCCESS" {
			return ciOKSuccess
		}
		return ciOKPending
	}
	if s == "IN_PROGRESS" || s == "QUEUED" || s == "PENDING" || s == "" {
		return ciOKPending
	}
	return ciOKPending
}

func first(v []string) string {
	if len(v) == 0 {
		return ""
	}
	return v[0]
}

// ghCISource is the live G1 path: one `gh pr view --json statusCheckRollup,headRefOid`.
// The rollup is the pull request's current head. It is accepted only when
// headRefOid is the resolved --head; a green current head is not a stamp for
// a different requested commit.
type ghCISource struct{ timeout time.Duration }

func (g ghCISource) CI(repo string, pr int, head string) (string, string, string, error) {
	if strings.TrimSpace(repo) == "" || pr < 1 {
		return "", "", "", fmt.Errorf("a live rollup wants --repo owner/name and --pr <n>")
	}
	if os.Getenv("NOVA_TEST_NO_HOST") == "1" {
		return "", "", "", fmt.Errorf("refusing to call gh under NOVA_TEST_NO_HOST; pass --rollup <file>")
	}
	timeout := g.timeout
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gh", "pr", "view", fmt.Sprintf("%d", pr),
		"--repo", repo, "--json", "statusCheckRollup,headRefOid")
	out, err := cmd.Output()
	if err != nil {
		return "", "", "", fmt.Errorf("gh pr view %d: %s", pr, oneline.Err(err))
	}
	var src rollupFile
	if err := json.Unmarshal(out, &src); err != nil {
		return "", "", "", fmt.Errorf("gh pr view did not answer json: %s", oneline.Err(err))
	}
	return acceptRollup(head, src)
}

func mergeTree(ctx context.Context, dir, base, head string) (state string, files []string, err error) {
	out, code, err := factsGit(ctx, dir, "merge-tree", "--write-tree", "--name-only", "--no-messages", base, head)
	if err != nil && code != 1 {
		return "", nil, err
	}
	if code == 0 {
		return mergeClean, nil, nil
	}
	if code != 1 {
		return "", nil, fmt.Errorf("git merge-tree exit %d: %s", code, oneline.Escape(strings.TrimSpace(out)))
	}
	return mergeConflict, conflictNames(out), nil
}

func conflictNames(out string) []string {
	var files []string
	seen := map[string]bool{}
	first := true
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if first {
			first = false
			if isOID(line) {
				continue
			}
		}
		if seen[line] {
			continue
		}
		seen[line] = true
		files = append(files, line)
	}
	sort.Strings(files)
	return files
}

func isOID(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	for _, r := range s {
		if r >= '0' && r <= '9' || r >= 'a' && r <= 'f' || r >= 'A' && r <= 'F' {
			continue
		}
		return false
	}
	return true
}

func diffVsPaths(ctx context.Context, in GateFactsInput) (state string, extra []string, err error) {
	globs, declared, err := loadPaths(in)
	if err != nil {
		return "", nil, err
	}
	changed, err := nameOnlyDiff(ctx, in.Dir, in.Base, in.Head)
	if err != nil {
		return "", nil, err
	}
	if !declared {
		return pathsSkip, nil, nil
	}
	if len(globs) == 0 {
		// PATHS: none — any changed path is extra.
		if len(changed) == 0 {
			return pathsOK, nil, nil
		}
		return pathsExtra, changed, nil
	}
	if err := hyg.ValidatePaths(globs); err != nil {
		return "", nil, err
	}
	var extras []string
	for _, p := range changed {
		if !declaredCovers(globs, p) {
			extras = append(extras, p)
		}
	}
	if len(extras) == 0 {
		return pathsOK, nil, nil
	}
	sort.Strings(extras)
	return pathsExtra, extras, nil
}

func loadPaths(in GateFactsInput) (globs []string, declared bool, err error) {
	if strings.TrimSpace(in.Paths) != "" {
		rest := strings.TrimSpace(in.Paths)
		if rest == "none" {
			return nil, true, nil
		}
		for _, g := range strings.Split(rest, ",") {
			g = strings.TrimSpace(g)
			if g != "" && g != "none" {
				globs = append(globs, g)
			}
		}
		if len(globs) == 0 {
			return nil, false, fmt.Errorf("--paths names no glob and is not `none`")
		}
		return globs, true, nil
	}
	if strings.TrimSpace(in.Card) == "" {
		return nil, false, nil
	}
	raw, err := os.ReadFile(in.Card)
	if err != nil {
		return nil, false, fmt.Errorf("cannot read --card %s: %s", in.Card, oneline.Err(err))
	}
	globs, declared = parsePATHS(string(raw))
	return globs, declared, nil
}

func nameOnlyDiff(ctx context.Context, dir, base, head string) ([]string, error) {
	out, code, err := factsGit(ctx, dir, "diff", "--name-only", "--no-renames", "-z", base+"..."+head)
	if err != nil {
		return nil, err
	}
	if code != 0 {
		return nil, fmt.Errorf("git diff --name-only %s...%s exit %d: %s", base, head, code, oneline.Escape(strings.TrimSpace(out)))
	}
	var files []string
	for _, p := range strings.Split(out, "\x00") {
		p = strings.TrimSpace(p)
		if p != "" {
			files = append(files, p)
		}
	}
	sort.Strings(files)
	return files, nil
}

func gitLine(ctx context.Context, dir string, args ...string) (string, error) {
	out, code, err := factsGit(ctx, dir, args...)
	if err != nil {
		return "", err
	}
	if code != 0 {
		return "", fmt.Errorf("git %s exit %d: %s", strings.Join(args, " "), code, oneline.Escape(strings.TrimSpace(out)))
	}
	return strings.TrimSpace(out), nil
}

func factsGit(ctx context.Context, dir string, args ...string) (string, int, error) {
	cmdArgs := append([]string{"--no-replace-objects", "-c", "core.quotePath=false"}, args...)
	cmd := exec.CommandContext(ctx, "git", cmdArgs...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.Output()
	if ctx.Err() != nil {
		return string(out), -1, fmt.Errorf("git %s: %v", args[0], ctx.Err())
	}
	if err == nil {
		return string(out), 0, nil
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		// merge-tree --name-only puts conflict paths on stdout at exit 1.
		if len(out) > 0 {
			return string(out), ee.ExitCode(), nil
		}
		return string(ee.Stderr), ee.ExitCode(), fmt.Errorf("git %s: %s", args[0], oneline.Escape(strings.TrimSpace(string(ee.Stderr))))
	}
	return string(out), -1, fmt.Errorf("git %s: %s", args[0], oneline.Err(err))
}
