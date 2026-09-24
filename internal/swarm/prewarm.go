package swarm

import (
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/goenv"
	"github.com/mas-bandwidth/nova-tools/internal/safepath"
)

const PrewarmReceiptDir = "prewarm"

// PrewarmCommand is one cache-producing phase. Run is injectable so unit tests prove the
// complete plan without compiling the repository or reaching a network.
type PrewarmCommand struct {
	Name string
	Dir  string
	Env  []string
	Argv []string
}

// PrewarmInput names one repository tip already present in a local checkout. Prewarm never
// guesses a branch or fetches a remote: fleet adoption fetches the bench mirror first and
// hands this verb the exact full commit it is warming.
type PrewarmInput struct {
	Root   string
	Source string
	Repo   string
	Tip    string
	Run    func(PrewarmCommand) error
}

type PrewarmResult struct {
	Checkout string
	Tip      string
	Phases   int
	Reused   bool
}

// LispCacheDir is the shared ASDF output directory under a swarm root.
func LispCacheDir(root string) string { return filepath.Join(root, CacheDirName, "common-lisp") }

// JobLispCacheDir is the private writable overlay for one card. It lives beside the
// card's checkout, inside the already-owned job directory, and is reaped with that job.
func JobLispCacheDir(sourceRoot string) string {
	return filepath.Join(filepath.Dir(filepath.Clean(sourceRoot)), ".cache", "common-lisp")
}

// ASDFOutputTranslations points compiled Lisp output at the bench-shared cache without
// sharing the harness's whole XDG cache or its per-card HOME.
func ASDFOutputTranslations(root, sourceRoot string) string {
	cachePath := LispCacheDir(root)
	// Keep repositories and tips out of one another's FASLs. A fresh clone at the same
	// exact tip resolves to the same key, while a landing gets a new directory.
	if head, err := gitOutput(sourceRoot, "rev-parse", "HEAD"); err == nil && fullHexSHA(strings.TrimSpace(head)) {
		cachePath = filepath.Join(cachePath, strings.ToLower(strings.TrimSpace(head)))
	}
	return asdfOutputTranslations(cachePath, sourceRoot)
}

// JobASDFOutputTranslations sends a card's writes to its own overlay. The overlay is
// seeded from the exact-tip prewarm cache before launch; cards never write that seed or
// another card's compiled output.
func JobASDFOutputTranslations(sourceRoot string) string {
	return asdfOutputTranslations(JobLispCacheDir(sourceRoot), sourceRoot)
}

func asdfOutputTranslations(cachePath, sourceRoot string) string {
	if resolved, err := filepath.EvalSymlinks(cachePath); err == nil {
		cachePath = resolved
	}
	if resolved, err := filepath.EvalSymlinks(sourceRoot); err == nil {
		sourceRoot = resolved
	}
	dir := filepath.ToSlash(filepath.Clean(cachePath)) + "/"
	source := strings.TrimSuffix(filepath.ToSlash(filepath.Clean(sourceRoot)), "/") + "/"
	return "(:output-translations (" + strconv.Quote(source) + " (" + strconv.Quote(dir) + " :implementation)) :ignore-inherited-configuration)"
}

// PrepareLispJobCache makes a private copy-on-write seed for one card. A missing prewarm
// seed is valid and produces an empty overlay; the card will compile there without
// contaminating any sibling.
func PrepareLispJobCache(root, sourceRoot string) error {
	dest := JobLispCacheDir(sourceRoot)
	if fi, err := os.Lstat(dest); err == nil {
		if !fi.IsDir() {
			return fmt.Errorf("job Lisp cache %s is not a directory", dest)
		}
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	parent := filepath.Dir(dest)
	if fi, err := os.Lstat(parent); err == nil {
		if !fi.IsDir() {
			return fmt.Errorf("job cache parent %s is not a directory", parent)
		}
	} else if os.IsNotExist(err) {
		if err := os.Mkdir(parent, 0o755); err != nil {
			return err
		}
	} else {
		return err
	}
	tmp, err := os.MkdirTemp(parent, ".common-lisp-seed-")
	if err != nil {
		return err
	}
	defer func() { _ = safepath.RemoveUnder(parent, tmp) }()
	if head, err := gitOutput(sourceRoot, "rev-parse", "HEAD"); err == nil && fullHexSHA(strings.TrimSpace(head)) {
		seed := filepath.Join(LispCacheDir(root), strings.ToLower(strings.TrimSpace(head)))
		if err := copyLispSeed(seed, tmp); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("copy exact-tip Lisp seed: %w", err)
		}
	}
	if err := os.Rename(tmp, dest); err != nil {
		return fmt.Errorf("publish job Lisp cache: %w", err)
	}
	return nil
}

func copyLispSeed(seed, dest string) error {
	return filepath.WalkDir(seed, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(seed, path)
		if err != nil || rel == "." {
			return err
		}
		out := filepath.Join(dest, rel)
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return os.MkdirAll(out, info.Mode().Perm())
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("Lisp seed entry %s is not a regular file", path)
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		outFile, err := os.OpenFile(out, os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode().Perm())
		if err != nil {
			in.Close()
			return err
		}
		_, copyErr := io.Copy(outFile, in)
		inCloseErr := in.Close()
		closeErr := outFile.Close()
		if copyErr != nil {
			return copyErr
		}
		if inCloseErr != nil {
			return inCloseErr
		}
		return closeErr
	})
}

// Prewarm clones an exact tip into the reference path sparse staging already consumes, then
// fills the bench caches for modules, ordinary builds, compiled test binaries and Lisp.
// A new reference checkout is renamed into view only after all four phases succeed.
func Prewarm(in PrewarmInput) (PrewarmResult, error) {
	root := filepath.Clean(strings.TrimSpace(in.Root))
	source := filepath.Clean(strings.TrimSpace(in.Source))
	repo := strings.TrimSpace(in.Repo)
	tip := strings.ToLower(strings.TrimSpace(in.Tip))
	if root == "." || strings.TrimSpace(in.Root) == "" {
		return PrewarmResult{}, fmt.Errorf("root is required; refusing to guess a swarm root")
	}
	if source == "." || strings.TrimSpace(in.Source) == "" {
		return PrewarmResult{}, fmt.Errorf("source is required; refusing to fetch or guess a checkout")
	}
	owner, name, ok := strings.Cut(repo, "/")
	if !ok || !prewarmComponent(owner) || !prewarmComponent(name) || strings.Contains(name, "/") {
		return PrewarmResult{}, fmt.Errorf("repo %q wants exactly owner/name using letters, digits, dot, dash or underscore", repo)
	}
	if !fullHexSHA(tip) {
		return PrewarmResult{}, fmt.Errorf("tip wants a full 40-character commit sha, got %q", in.Tip)
	}
	resolved, err := gitOutput(source, "rev-parse", "--verify", tip+"^{commit}")
	if err != nil {
		return PrewarmResult{}, fmt.Errorf("tip %s is not a commit in source %s: %w", tip, source, err)
	}
	if strings.ToLower(strings.TrimSpace(resolved)) != tip {
		return PrewarmResult{}, fmt.Errorf("source resolved tip %s as %s", tip, strings.TrimSpace(resolved))
	}

	ownerDir := filepath.Join(root, "ref", owner)
	final := filepath.Join(ownerDir, name+"@"+tip)
	receiptPath := filepath.Join(root, PrewarmReceiptDir, owner, name+"@"+tip+".receipt")
	checkout := final
	reused := false
	if fi, statErr := os.Stat(final); statErr == nil {
		if !fi.IsDir() || !isGitCheckout(final) {
			return PrewarmResult{}, fmt.Errorf("reference path %s exists and is not a git checkout", final)
		}
		head, headErr := gitOutput(final, "rev-parse", "HEAD")
		if headErr != nil || strings.ToLower(strings.TrimSpace(head)) != tip {
			return PrewarmResult{}, fmt.Errorf("reference path %s does not hold exact tip %s", final, tip)
		}
		reused = true
	} else if !os.IsNotExist(statErr) {
		return PrewarmResult{}, fmt.Errorf("reference path %s: %w", final, statErr)
	} else {
		if err := os.MkdirAll(ownerDir, 0o755); err != nil {
			return PrewarmResult{}, err
		}
		tmpRoot, err := os.MkdirTemp(ownerDir, "."+name+"@"+tip+"-prewarm-")
		if err != nil {
			return PrewarmResult{}, err
		}
		defer func() { _ = safepath.RemoveUnder(ownerDir, tmpRoot) }()
		checkout = filepath.Join(tmpRoot, "checkout")
		if _, err := gitOutput("", "clone", "--quiet", "--local", "--no-hardlinks", "--no-checkout", source, checkout); err != nil {
			return PrewarmResult{}, fmt.Errorf("clone exact-tip reference: %w", err)
		}
		if _, err := gitOutput(checkout, "checkout", "--quiet", "--detach", tip); err != nil {
			return PrewarmResult{}, fmt.Errorf("checkout exact tip %s: %w", tip, err)
		}
	}
	// A repeated prewarm must earn a fresh receipt. Remove the prior success before
	// any cache-producing work so a failed rerun cannot leave stale evidence behind.
	if err := os.Remove(receiptPath); err != nil && !os.IsNotExist(err) {
		return PrewarmResult{}, fmt.Errorf("invalidate prior prewarm receipt: %w", err)
	}
	if err := normalizeTrackedTimes(checkout); err != nil {
		return PrewarmResult{}, fmt.Errorf("pin source mtimes to exact tip: %w", err)
	}

	if err := EnsureCacheDirs(root); err != nil {
		return PrewarmResult{}, err
	}
	if err := os.MkdirAll(LispCacheDir(root), 0o755); err != nil {
		return PrewarmResult{}, err
	}
	env := prewarmEnv(root, checkout)
	commands := []PrewarmCommand{
		{Name: "modules", Dir: checkout, Env: env, Argv: []string{"go", "mod", "download"}},
		{Name: "build", Dir: checkout, Env: env, Argv: []string{"make", "build"}},
		// Make consumes one dollar while expanding RUN in the recipe; two here hand
		// go test the intended no-test regexp ^$ instead of ^ (which runs everything).
		{Name: "test-binaries", Dir: checkout, Env: env, Argv: []string{"make", "test-full", "RUN=^$$", "PKGS=./cmd/... ./internal/..."}},
		{Name: "lisp", Dir: checkout, Env: env, Argv: []string{"make", "compile-lisp"}},
	}
	run := in.Run
	if run == nil {
		run = runPrewarmCommand
	}
	for _, command := range commands {
		if err := run(command); err != nil {
			return PrewarmResult{}, fmt.Errorf("prewarm phase %s: %w", command.Name, err)
		}
	}
	if !reused {
		if err := os.Rename(checkout, final); err != nil {
			return PrewarmResult{}, fmt.Errorf("publish reference checkout %s: %w", final, err)
		}
		checkout = final
	}
	receipt := fmt.Sprintf("PREWARM OK repo=%s tip=%s phases=modules,build,test-binaries,lisp\n", repo, tip)
	if err := os.MkdirAll(filepath.Dir(receiptPath), 0o755); err != nil {
		return PrewarmResult{}, fmt.Errorf("make prewarm receipt directory: %w", err)
	}
	if err := writeAtomic(receiptPath, []byte(receipt), 0o644); err != nil {
		return PrewarmResult{}, fmt.Errorf("write prewarm receipt: %w", err)
	}
	return PrewarmResult{Checkout: checkout, Tip: tip, Phases: len(commands), Reused: reused}, nil
}

func prewarmComponent(s string) bool {
	if s == "" || s == "." || s == ".." {
		return false
	}
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || strings.ContainsRune("._-", r) {
			continue
		}
		return false
	}
	return true
}

func fullHexSHA(s string) bool {
	if len(s) != 40 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

func gitOutput(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(goenv.WithoutSecrets(os.Environ()), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

func prewarmEnv(root, checkout string) []string {
	env := goenv.Clean(os.Environ())
	for _, name := range []string{"GOMODCACHE", "GOCACHE", "GOTOOLCHAIN", "ASDF_OUTPUT_TRANSLATIONS"} {
		env = envWithoutName(env, name)
	}
	return append(env,
		"GOMODCACHE="+GoModCacheDir(root),
		"GOCACHE="+GoBuildCacheDir(root),
		"GOTOOLCHAIN=local",
		"ASDF_OUTPUT_TRANSLATIONS="+ASDFOutputTranslations(root, checkout),
	)
}

func envWithoutName(env []string, name string) []string {
	out := env[:0]
	for _, entry := range env {
		key, _, _ := strings.Cut(entry, "=")
		if !strings.EqualFold(key, name) {
			out = append(out, entry)
		}
	}
	return out
}

func runPrewarmCommand(c PrewarmCommand) error {
	if len(c.Argv) == 0 {
		return fmt.Errorf("empty command")
	}
	cmd := exec.Command(c.Argv[0], c.Argv[1:]...)
	cmd.Dir = c.Dir
	cmd.Env = c.Env
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
