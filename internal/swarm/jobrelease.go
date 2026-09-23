package swarm

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/safepath"
)

// ReleaseJobDir is what nova-swarm native does when a card ends (nova-tools #2379).
//
// The job directory is the clone and the scratch, and it is the thing that filled a
// bench: hundreds of them, each a checkout, left behind after the card had already
// finished. The tool that ends the card removes it. A sweep that comes along hours
// later is how the disk fills up first.
//
// It removes nothing until the durable record is outside the directory and checked
// against itself, not against a side file of "what we pushed":
//
//  1. RESULT.md, the job's other regular files (usage.tsv, the harness log, notes),
//     and the slot's native.log are copied into ResultsDir. A copy whose bytes do not
//     hash back to the source is not a copy. The results path is resolved before that
//     copy: a component that is a symlink into the job or the temp directory, or that
//     resolves outside <root>/results, is not a destination. The lexical path can look
//     contained while the copy sits in the directory that is about to be removed.
//  2. A commit the clone holds that is not already in its remote-tracking base, and a
//     BRANCH line in RESULT.md whose ref is not already in that base, are written to
//     branch.bundle. The bundle is verified, and its listed tips have to be those
//     commits. A bundle that does not list them is not a bundle.
//  3. Only then are the job directory and the sandbox tmp removed, each strictly below
//     the slot, and neither of them the shared cache or the bench mirror.
//
// A failure at any earlier step keeps the directory. A named BRANCH that is not a ref
// in the clone, or whose commit is not in the bundle and not in the base, keeps it
// too: that is a result whose branch exists nowhere else, and removing the directory
// would be removing the only copy.
type ReleaseJobInput struct {
	JobDir     string // <slot>/jobs/<label>, the directory the card ran in
	TmpDir     string // <slot>/tmp/<label>, the sandbox tmp; empty skips it
	SlotDir    string // the removal root; job and tmp must sit strictly below it
	Root       string // the swarm root; results and the job must sit inside it
	ResultsDir string // where the durable record is written, outside the job
	Label      string // the card; a name, not a path
	NativeLog  string // the slot's native.log, copied when it is a regular file
	BenchHome  string // home used only to recognise ~/nova-bench/mirror; empty asks the OS
	git        releaseGit
}

// ReleaseJobResult is what the release stored. Removed is false when there was
// nothing to remove; a refusal is the error, and the directory is still there.
type ReleaseJobResult struct {
	Removed bool
	Results string
	Head    string
	Bundle  string
}

type gitState struct {
	SHA      string
	NoCommit bool
	Base     string
	Unique   bool
}

// releaseGit is the local git a release asks about a clone. Tests pass a fake;
// production leaves it nil and the real git answers. Nothing here reaches a host:
// the bundle is the record, and the base is the clone's own remote-tracking ref.
type releaseGit interface {
	state(repo string) (gitState, error)
	rev(repo, ref string) (string, error)
	ancestor(repo, sha, base string) (bool, error)
	bundle(repo, dest, base, sha, branch string) (string, error)
}

type localGit struct{}

// ReleaseJobDir copies the card's durable record out of the job directory and,
// once that record checks, removes the job directory and its sandbox tmp.
func ReleaseJobDir(in ReleaseJobInput) (ReleaseJobResult, error) {
	job, err := filepath.Abs(in.JobDir)
	if err != nil {
		return ReleaseJobResult{}, fmt.Errorf("the job directory could not be resolved: %w", err)
	}
	slot, err := filepath.Abs(in.SlotDir)
	if err != nil {
		return ReleaseJobResult{}, fmt.Errorf("the slot directory could not be resolved: %w", err)
	}
	root := ""
	if strings.TrimSpace(in.Root) != "" {
		root, err = filepath.Abs(in.Root)
		if err != nil {
			return ReleaseJobResult{}, fmt.Errorf("the swarm root could not be resolved: %w", err)
		}
	}
	results := ""
	if strings.TrimSpace(in.ResultsDir) != "" {
		results, err = filepath.Abs(in.ResultsDir)
		if err != nil {
			return ReleaseJobResult{}, fmt.Errorf("the results directory could not be resolved: %w", err)
		}
	}
	tmp := ""
	if strings.TrimSpace(in.TmpDir) != "" {
		tmp, err = filepath.Abs(in.TmpDir)
		if err != nil {
			return ReleaseJobResult{}, fmt.Errorf("the temp directory could not be resolved: %w", err)
		}
	}
	protected := protectedRoots(root, in.BenchHome)

	jobInfo, jobErr := os.Lstat(job)
	jobMissing := os.IsNotExist(jobErr)
	if jobErr != nil && !jobMissing {
		return ReleaseJobResult{}, fmt.Errorf("the job directory could not be read: %w", jobErr)
	}
	if jobMissing {
		if tmp == "" {
			return ReleaseJobResult{}, nil
		}
		if err := removeReleased(tmp, slot, protected); err != nil {
			return ReleaseJobResult{}, err
		}
		return ReleaseJobResult{Removed: true}, nil
	}
	if jobInfo.Mode()&os.ModeSymlink != 0 {
		return ReleaseJobResult{}, fmt.Errorf("the job directory %s is a symlink", job)
	}
	if !safepath.NameOK(in.Label) {
		return ReleaseJobResult{}, fmt.Errorf("the label %s is not a job name", in.Label)
	}
	if results == "" {
		return ReleaseJobResult{}, fmt.Errorf("the results directory is empty")
	}
	if root != "" && !releaseStrictlyWithin(root, results) {
		return ReleaseJobResult{}, fmt.Errorf("the results directory %s is not under the swarm root %s", results, root)
	}
	if releasePathWithin(job, results) {
		return ReleaseJobResult{}, fmt.Errorf("the results directory %s is inside the job directory", results)
	}
	if overlapsProtected(job, protected) || (tmp != "" && overlapsProtected(tmp, protected)) || overlapsProtected(results, protected) {
		return ReleaseJobResult{}, fmt.Errorf("refusing to remove or write a shared cache or the bench mirror")
	}
	if root != "" && !releaseStrictlyWithin(root, job) {
		return ReleaseJobResult{}, fmt.Errorf("the job directory %s is not under the swarm root %s", job, root)
	}
	if !releaseStrictlyWithin(slot, job) {
		return ReleaseJobResult{}, fmt.Errorf("the job directory %s is not under the slot %s", job, slot)
	}
	if tmp != "" && !releaseStrictlyWithin(slot, tmp) {
		return ReleaseJobResult{}, fmt.Errorf("the temp directory %s is not under the slot %s", tmp, slot)
	}
	if err := refuseResultsAlias(results, root, job, tmp); err != nil {
		return ReleaseJobResult{}, err
	}

	if err := os.MkdirAll(results, 0o755); err != nil {
		return ReleaseJobResult{}, fmt.Errorf("the results directory could not be made: %w", err)
	}
	// MkdirAll follows a directory symlink. Re-resolve after it exists so a
	// destination created through an alias is still refused before the copy.
	if err := refuseResultsAlias(results, root, job, tmp); err != nil {
		return ReleaseJobResult{}, err
	}
	sums, err := copyJobEvidence(job, results, in.NativeLog)
	if err != nil {
		return ReleaseJobResult{}, err
	}
	git := in.git
	if git == nil {
		git = localGit{}
	}
	head, bundle, err := preserveWork(job, results, git)
	if err != nil {
		return ReleaseJobResult{Results: results, Head: head}, err
	}
	if bundle != "" {
		sum, err := sha256File(bundle)
		if err != nil {
			return ReleaseJobResult{Results: results, Head: head}, fmt.Errorf("the bundle could not be read back: %w", err)
		}
		sums["branch.bundle"] = sum
	}
	if err := writeReleaseManifest(results, in.Label, head, bundle, sums); err != nil {
		return ReleaseJobResult{Results: results, Head: head, Bundle: bundle}, err
	}
	if err := removeReleased(job, slot, protected); err != nil {
		return ReleaseJobResult{Results: results, Head: head, Bundle: bundle}, err
	}
	if tmp != "" {
		if err := removeReleased(tmp, slot, protected); err != nil {
			return ReleaseJobResult{Removed: true, Results: results, Head: head, Bundle: bundle}, fmt.Errorf("the job directory was removed but the temp directory was kept: %w", err)
		}
	}
	return ReleaseJobResult{Removed: true, Results: results, Head: head, Bundle: bundle}, nil
}

func protectedRoots(root, benchHome string) []string {
	var out []string
	if strings.TrimSpace(root) != "" {
		out = append(out,
			filepath.Join(root, CacheDirName),
			filepath.Join(root, "tmp", "cache"),
		)
	}
	home := strings.TrimSpace(benchHome)
	if home == "" {
		if h, err := os.UserHomeDir(); err == nil {
			home = h
		}
	}
	if home != "" {
		out = append(out, filepath.Join(home, "nova-bench", "mirror"))
	}
	return out
}

func overlapsProtected(path string, roots []string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return true
	}
	paths := []string{filepath.Clean(abs)}
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		paths = append(paths, filepath.Clean(real))
	}
	for _, root := range roots {
		rabs, err := filepath.Abs(root)
		if err != nil {
			continue
		}
		rs := []string{filepath.Clean(rabs)}
		if real, err := filepath.EvalSymlinks(rabs); err == nil {
			rs = append(rs, filepath.Clean(real))
		}
		for _, p := range paths {
			for _, r := range rs {
				if releasePathWithin(r, p) || releasePathWithin(p, r) {
					return true
				}
			}
		}
	}
	return false
}

func releasePathWithin(root, path string) bool {
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(path))
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

func releaseStrictlyWithin(root, path string) bool {
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(path))
	if err != nil || rel == "." {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// refuseResultsAlias refuses a results directory that is not really inside the
// results root and outside the job. Containment of the cleaned path is not
// enough: results/<label> can be a symlink into <job>/saved, the copy and the
// manifest land there, and removeReleased then deletes the only durable copy.
// Every component at and below the results root is resolved before writing.
func refuseResultsAlias(results, root, job, tmp string) error {
	resolvedJob, err := AbsResolved(job)
	if err != nil {
		return fmt.Errorf("the job directory could not be resolved: %w", err)
	}
	resolvedTmp := ""
	if tmp != "" {
		resolvedTmp, err = AbsResolved(tmp)
		if err != nil {
			return fmt.Errorf("the temp directory could not be resolved: %w", err)
		}
	}
	start, rel, resultsRoot, err := resultsAliasWalk(results, root)
	if err != nil {
		return err
	}
	dest, err := resolveResultsDestination(start, rel, resultsRoot, resolvedJob, resolvedTmp)
	if err != nil {
		return err
	}
	return requireResultsContainment(results, dest, resultsRoot, resolvedJob, resolvedTmp)
}

// resultsAliasWalk is the walk from a resolved ancestor over the lexical
// components of the destination. The results root is <root>/results when a
// swarm root was named, and the parent of ResultsDir otherwise. The root of
// that walk is resolved; the results directory itself is not, so a symlink
// there cannot move the boundary onto its target.
func resultsAliasWalk(results, root string) (start, rel, resultsRoot string, err error) {
	if strings.TrimSpace(root) != "" {
		var resolvedRoot string
		resolvedRoot, err = AbsResolved(root)
		if err != nil {
			return "", "", "", fmt.Errorf("the swarm root could not be resolved: %w", err)
		}
		rel, err = filepath.Rel(filepath.Clean(root), filepath.Clean(results))
		if err != nil {
			return "", "", "", fmt.Errorf("the results directory %s is not under the swarm root: %w", results, err)
		}
		return resolvedRoot, rel, filepath.Join(resolvedRoot, "results"), nil
	}
	parent := filepath.Dir(filepath.Clean(results))
	grand := filepath.Dir(parent)
	var resolvedGrand string
	resolvedGrand, err = AbsResolved(grand)
	if err != nil {
		return "", "", "", fmt.Errorf("the results directory could not be resolved: %w", err)
	}
	rel = filepath.Join(filepath.Base(parent), filepath.Base(results))
	return resolvedGrand, rel, filepath.Join(resolvedGrand, filepath.Base(parent)), nil
}

func resolveResultsDestination(start, rel, resultsRoot, job, tmp string) (string, error) {
	if rel == "" || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("the results directory is not under the results root")
	}
	cur := start
	elems := strings.Split(rel, string(filepath.Separator))
	for i, elem := range elems {
		if elem == "" || elem == "." {
			continue
		}
		if elem == ".." {
			return "", fmt.Errorf("the results directory resolves through %q", elem)
		}
		next := filepath.Join(cur, elem)
		fi, err := os.Lstat(next)
		if os.IsNotExist(err) {
			for _, e := range elems[i:] {
				if e == "" || e == "." || e == ".." {
					return "", fmt.Errorf("the results directory resolves through %q", e)
				}
			}
			parts := append([]string{cur}, elems[i:]...)
			return filepath.Clean(filepath.Join(parts...)), nil
		}
		if err != nil {
			return "", fmt.Errorf("the results directory could not be resolved: %w", err)
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			target, err := filepath.EvalSymlinks(next)
			if err != nil {
				return "", fmt.Errorf("the results directory %s is a symlink that could not be resolved: %w", next, err)
			}
			target = filepath.Clean(target)
			if err := judgeResultsSymlink(next, target, resultsRoot, job, tmp); err != nil {
				return "", err
			}
			cur = target
			continue
		}
		if !fi.IsDir() {
			return "", fmt.Errorf("the results directory %s is not a directory", next)
		}
		cur = next
	}
	return filepath.Clean(cur), nil
}

func judgeResultsSymlink(link, target, resultsRoot, job, tmp string) error {
	in, err := releaseLocated(job, target, false)
	if err != nil {
		return fmt.Errorf("the results directory %s is a symlink that could not be placed against the job: %w", link, err)
	}
	if in {
		return fmt.Errorf("the results directory %s is a symlink into the job", link)
	}
	if tmp != "" {
		in, err = releaseLocated(tmp, target, false)
		if err != nil {
			return fmt.Errorf("the results directory %s is a symlink that could not be placed against the temp directory: %w", link, err)
		}
		if in {
			return fmt.Errorf("the results directory %s is a symlink into the temp directory", link)
		}
	}
	in, err = releaseLocated(resultsRoot, target, true)
	if err != nil {
		return fmt.Errorf("the results directory %s is a symlink that could not be placed under the results root: %w", link, err)
	}
	if !in {
		return fmt.Errorf("the results directory %s is a symlink outside the results root", link)
	}
	return nil
}

func requireResultsContainment(results, dest, resultsRoot, job, tmp string) error {
	in, err := releaseLocated(resultsRoot, dest, true)
	if err != nil {
		return fmt.Errorf("the results directory %s could not be placed under the results root: %w", results, err)
	}
	if !in {
		return fmt.Errorf("the results directory %s resolves outside the results root %s", results, resultsRoot)
	}
	in, err = releaseLocated(job, dest, false)
	if err != nil {
		return fmt.Errorf("the results directory %s could not be placed against the job: %w", results, err)
	}
	if in {
		return fmt.Errorf("the results directory %s resolves inside the job directory", results)
	}
	if tmp == "" {
		return nil
	}
	in, err = releaseLocated(tmp, dest, false)
	if err != nil {
		return fmt.Errorf("the results directory %s could not be placed against the temp directory: %w", results, err)
	}
	if in {
		return fmt.Errorf("the results directory %s resolves inside the temp directory", results)
	}
	return nil
}

// releaseLocated reports whether path is inside root. When strict is set, the
// root itself does not count. An uncomputable relative path is an error, not a no.
func releaseLocated(root, path string, strict bool) (bool, error) {
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(path))
	if err != nil {
		return false, err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false, nil
	}
	if rel == "." {
		return !strict, nil
	}
	return true, nil
}

// copyJobEvidence copies the durable files out of the job. Top-level regular files
// are the notes, the usage row and the harness log; RESULT.md may instead sit in
// the clone, and the slot's native.log sits beside the job, not in it. Symlinks
// are skipped: a link is not the file, and following one is how a copy leaves the
// directory it was meant to stay in.
func copyJobEvidence(job, results, nativeLog string) (map[string]string, error) {
	sums := map[string]string{}
	entries, err := os.ReadDir(job)
	if err != nil {
		return nil, fmt.Errorf("the job directory could not be listed: %w", err)
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || strings.HasPrefix(name, ".") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			return nil, err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			continue
		}
		sum, err := copyVerified(filepath.Join(job, name), filepath.Join(results, name))
		if err != nil {
			return nil, fmt.Errorf("copying %s: %w", name, err)
		}
		sums[name] = sum
	}
	if res, ok := FindCardResult(job); ok {
		fi, err := os.Lstat(res)
		if err != nil {
			return nil, fmt.Errorf("RESULT.md could not be read: %w", err)
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("RESULT.md is a symlink")
		}
		if _, copied := sums["RESULT.md"]; !copied {
			sum, err := copyVerified(res, filepath.Join(results, "RESULT.md"))
			if err != nil {
				return nil, fmt.Errorf("copying RESULT.md: %w", err)
			}
			sums["RESULT.md"] = sum
		}
	}
	if strings.TrimSpace(nativeLog) != "" {
		fi, err := os.Lstat(nativeLog)
		if err == nil && fi.Mode().IsRegular() {
			sum, err := copyVerified(nativeLog, filepath.Join(results, "native.log"))
			if err != nil {
				return nil, fmt.Errorf("copying native.log: %w", err)
			}
			sums["native.log"] = sum
		}
	}
	return sums, nil
}

func copyVerified(src, dst string) (string, error) {
	in, err := os.Open(src)
	if err != nil {
		return "", err
	}
	defer in.Close()
	h := sha256.New()
	tmp := dst + ".tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(out, io.TeeReader(in, h)); err != nil {
		out.Close()
		os.Remove(tmp)
		return "", err
	}
	if err := out.Sync(); err != nil {
		out.Close()
		os.Remove(tmp)
		return "", err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return "", err
	}
	if err := os.Rename(tmp, dst); err != nil {
		os.Remove(tmp)
		return "", err
	}
	sum := hex.EncodeToString(h.Sum(nil))
	got, err := sha256File(dst)
	if err != nil {
		return "", err
	}
	if got != sum {
		return "", fmt.Errorf("the copy at %s does not match the source", dst)
	}
	return sum, nil
}

func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func writeReleaseManifest(results, label, head, bundle string, sums map[string]string) error {
	var b strings.Builder
	fmt.Fprintf(&b, "label=%s\n", label)
	if head == "" {
		fmt.Fprintf(&b, "head=-\n")
	} else {
		fmt.Fprintf(&b, "head=%s\n", head)
	}
	if bundle == "" {
		fmt.Fprintf(&b, "bundle=-\n")
	} else {
		fmt.Fprintf(&b, "bundle=%s\n", filepath.Base(bundle))
	}
	names := make([]string, 0, len(sums))
	for name := range sums {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		fmt.Fprintf(&b, "file %s %s\n", name, sums[name])
	}
	path := filepath.Join(results, "release-manifest.txt")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(b.String()), 0o644); err != nil {
		return fmt.Errorf("the manifest could not be written: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("the manifest could not be written: %w", err)
	}
	for _, name := range names {
		got, err := sha256File(filepath.Join(results, name))
		if err != nil || got != sums[name] {
			return fmt.Errorf("the stored %s does not match the manifest", name)
		}
	}
	if bundle != "" {
		if _, err := os.Stat(bundle); err != nil {
			return fmt.Errorf("the bundle is not in the results directory: %w", err)
		}
	}
	return nil
}

func preserveWork(job, results string, git releaseGit) (head, bundle string, err error) {
	repo := jobWorkRepo(job)
	if repo == "" {
		return "", "", nil
	}
	st, err := git.state(repo)
	if err != nil {
		return "", "", fmt.Errorf("the clone could not be read, so the job directory is kept: %w", err)
	}
	if st.NoCommit {
		if branch := branchFromResult(job); branch != "" {
			return "", "", fmt.Errorf("RESULT.md names BRANCH %s and the clone has no commit", branch)
		}
		return "", "", nil
	}
	branch := branchFromResult(job)
	branchSHA := ""
	if branch != "" {
		if !branchRefOK(branch) {
			return st.SHA, "", fmt.Errorf("RESULT.md names a BRANCH %q that is not a ref name", branch)
		}
		branchSHA, err = git.rev(repo, "refs/heads/"+branch)
		if err != nil || branchSHA == "" {
			return st.SHA, "", fmt.Errorf("RESULT.md names BRANCH %s and that ref is not in the clone", branch)
		}
	}
	needBranch := false
	if branchSHA != "" {
		inBase := false
		if st.Base != "" {
			inBase, err = git.ancestor(repo, branchSHA, st.Base)
			if err != nil {
				return st.SHA, "", fmt.Errorf("the named branch could not be checked against the clone base: %w", err)
			}
		}
		needBranch = !inBase
	}
	if !st.Unique && !needBranch {
		return st.SHA, "", nil
	}
	dest := filepath.Join(results, "branch.bundle")
	include := ""
	if needBranch {
		include = branch
	}
	heads, err := git.bundle(repo, dest, st.Base, st.SHA, include)
	if err != nil {
		return st.SHA, "", fmt.Errorf("the branch could not be bundled, so the job directory is kept: %w", err)
	}
	if st.Unique && !headsContain(heads, st.SHA) {
		return st.SHA, "", fmt.Errorf("the bundle does not list HEAD %s, so the job directory is kept", st.SHA)
	}
	if needBranch && !headsContain(heads, branchSHA) {
		return st.SHA, "", fmt.Errorf("the bundle does not list BRANCH %s at %s, so the job directory is kept", branch, branchSHA)
	}
	return st.SHA, dest, nil
}

func jobWorkRepo(job string) string {
	repo := filepath.Join(job, "repo")
	if gitDirExists(repo) {
		return repo
	}
	if gitDirExists(job) {
		return job
	}
	return ""
}

func gitDirExists(dir string) bool {
	_, err := os.Lstat(filepath.Join(dir, ".git"))
	return err == nil
}

func branchFromResult(job string) string {
	path, ok := FindCardResult(job)
	if !ok {
		return ""
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(raw), "\n") {
		t := strings.TrimSpace(line)
		if rest, ok := strings.CutPrefix(t, "BRANCH:"); ok {
			return strings.TrimSpace(rest)
		}
		if rest, ok := strings.CutPrefix(t, "BRANCH "); ok {
			return strings.TrimSpace(rest)
		}
	}
	return ""
}

func branchRefOK(branch string) bool {
	if branch == "" || strings.HasPrefix(branch, "-") || strings.Contains(branch, "..") {
		return false
	}
	if strings.ContainsAny(branch, " \t:~^?*[\\") {
		return false
	}
	return true
}

func headsContain(heads, sha string) bool {
	if sha == "" {
		return false
	}
	for _, line := range strings.Split(heads, "\n") {
		fields := strings.Fields(line)
		if len(fields) > 0 && fields[0] == sha {
			return true
		}
	}
	return false
}

func removeReleased(path, slot string, protected []string) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s is a symlink", path)
	}
	if overlapsProtected(path, protected) {
		return fmt.Errorf("refusing to remove a shared cache or the bench mirror")
	}
	if _, err := safepath.ResolvedUnder(path, slot); err != nil {
		return err
	}
	if err := safepath.RemoveUnder(slot, path); err != nil {
		return err
	}
	return nil
}

func (localGit) state(repo string) (gitState, error) {
	if _, err := exec.LookPath("git"); err != nil {
		return gitState{}, errors.New("git is not on PATH")
	}
	out, err := gitCombined(repo, "rev-parse", "--verify", "HEAD")
	if err != nil {
		if noCommitText(out) {
			return gitState{NoCommit: true}, nil
		}
		return gitState{}, fmt.Errorf("cannot read HEAD: %s", oneLine(out))
	}
	st := gitState{SHA: out}
	st.Base = wallBaseRef(repo)
	if st.Base == "" {
		st.Unique = true
		return st, nil
	}
	anc, err := localGit{}.ancestor(repo, st.SHA, st.Base)
	if err != nil {
		return gitState{}, err
	}
	st.Unique = !anc
	return st, nil
}

func (localGit) rev(repo, ref string) (string, error) {
	out, err := gitCombined(repo, "rev-parse", "--verify", ref)
	if err != nil {
		return "", err
	}
	return out, nil
}

func (localGit) ancestor(repo, sha, base string) (bool, error) {
	_, err := gitCombined(repo, "merge-base", "--is-ancestor", sha, base)
	if err == nil {
		return true, nil
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) && ee.ExitCode() == 1 {
		return false, nil
	}
	return false, fmt.Errorf("merge-base --is-ancestor failed: %v", err)
}

func (localGit) bundle(repo, dest, base, sha, branch string) (string, error) {
	args := []string{"bundle", "create", dest}
	switch {
	case base != "" && sha != "":
		args = append(args, base+".."+sha)
	case sha != "":
		args = append(args, sha)
	default:
		return "", errors.New("nothing to bundle")
	}
	if branch != "" {
		args = append(args, "refs/heads/"+branch)
	}
	if out, err := gitCombined(repo, args...); err != nil {
		return "", fmt.Errorf("%s", oneLine(out))
	}
	if out, err := gitCombined(repo, "bundle", "verify", dest); err != nil {
		return "", fmt.Errorf("%s", oneLine(out))
	}
	heads, err := gitCombined(repo, "bundle", "list-heads", dest)
	if err != nil {
		return "", err
	}
	return heads, nil
}

func gitCombined(repo string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func noCommitText(out string) bool {
	low := strings.ToLower(out)
	return strings.Contains(low, "needed a single revision") ||
		strings.Contains(low, "unknown revision") ||
		strings.Contains(low, "ambiguous argument")
}
