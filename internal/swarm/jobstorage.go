package swarm

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/safepath"
)

// ErrProtectedStoragePath is returned when a cleanup operation targets a shared cache or mirror.
var ErrProtectedStoragePath = errors.New("refusing to remove protected storage path (shared cache or mirror)")

// ResultFileName is the canonical name of a card's published result.
const ResultFileName = "RESULT.md"

// JobStorageCleanupOpts describes the parameters for atomic job storage cleanup at card completion.
type JobStorageCleanupOpts struct {
	JobDir           string
	TmpDir           string
	SlotDir          string
	Root             string
	BenchHome        string
	ResultsDir       string
	Label            string
	IsHarnessFailure bool
	ResultsMoved     bool
	ReleaseLease     func()
}

// JobStorageCleanResult reports what cleanup performed.
type JobStorageCleanResult struct {
	Cleaned      bool
	JobRemoved   string
	TmpRemoved   string
	ResultsMoved bool
	ResultsDir   string
}

// IsProtectedStoragePath reports whether path is or contains or sits inside a protected shared
// cache (tmp/cache/{go-mod,go-build,npm}, <root>/cache) or mirror (~/nova-bench/mirror).
func IsProtectedStoragePath(path, benchHome, root string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	clean := filepath.Clean(path)

	// Direct pattern check on path components
	sep := string(filepath.Separator)
	norm := filepath.ToSlash(clean)

	// Check shared caches
	if strings.Contains(norm, "/tmp/cache/") || strings.HasSuffix(norm, "/tmp/cache") ||
		strings.Contains(norm, "tmp/cache/go-mod") || strings.Contains(norm, "tmp/cache/go-build") || strings.Contains(norm, "tmp/cache/npm") ||
		strings.Contains(norm, "/cache/gomod") || strings.Contains(norm, "/cache/gobuild") || strings.Contains(norm, "/cache/npm") ||
		strings.Contains(norm, "/cache/go-mod") || strings.Contains(norm, "/cache/go-build") {
		return true
	}

	// Check mirrors
	if strings.Contains(norm, "nova-bench/mirror") || strings.HasSuffix(norm, "/mirror") && strings.Contains(norm, "nova-bench") {
		return true
	}

	// Check against root/cache
	if strings.TrimSpace(root) != "" {
		cacheRoot := filepath.Clean(CacheRoot(root))
		if clean == cacheRoot || isUnder(cacheRoot, clean) || isUnder(clean, cacheRoot) {
			return true
		}
		tmpCache := filepath.Clean(filepath.Join(root, "tmp", "cache"))
		if clean == tmpCache || isUnder(tmpCache, clean) || isUnder(clean, tmpCache) {
			return true
		}
		if clean == filepath.Clean(root) {
			return true
		}
	}

	// Check against benchHome/nova-bench/mirror
	if strings.TrimSpace(benchHome) != "" {
		mirrorRoot := filepath.Clean(filepath.Join(benchHome, "nova-bench", "mirror"))
		if clean == mirrorRoot || isUnder(mirrorRoot, clean) || isUnder(clean, mirrorRoot) {
			return true
		}
		if clean == filepath.Clean(benchHome) {
			return true
		}
	} else if home, err := os.UserHomeDir(); err == nil && home != "" {
		mirrorRoot := filepath.Clean(filepath.Join(home, "nova-bench", "mirror"))
		if clean == mirrorRoot || isUnder(mirrorRoot, clean) || isUnder(clean, mirrorRoot) {
			return true
		}
	}

	// Path equality with root or home
	if clean == "/" || clean == "." || clean == ".." {
		return true
	}
	_ = sep
	return false
}

func isUnder(base, target string) bool {
	rel, err := filepath.Rel(base, target)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// AssertNotProtectedStoragePath verifies that path is not protected.
func AssertNotProtectedStoragePath(path, benchHome, root string) error {
	if IsProtectedStoragePath(path, benchHome, root) {
		return fmt.Errorf("%w: %q is in protected set (shared cache or mirror)", ErrProtectedStoragePath, path)
	}
	// Also check symlink target if path exists
	if fi, err := os.Lstat(path); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		target, err := filepath.EvalSymlinks(path)
		if err == nil && IsProtectedStoragePath(target, benchHome, root) {
			return fmt.Errorf("%w: symlink %q targets protected path %q", ErrProtectedStoragePath, path, target)
		}
	}
	return nil
}

// DefaultResultsDir returns the default results store location on this bench for label.
func DefaultResultsDir(benchHome, slotDir, label string) string {
	if strings.TrimSpace(benchHome) != "" {
		nb := filepath.Join(benchHome, "nova-bench")
		if fi, err := os.Stat(nb); err == nil && fi.IsDir() {
			return filepath.Join(nb, "results", label)
		}
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		nb := filepath.Join(home, "nova-bench")
		if fi, err := os.Stat(nb); err == nil && fi.IsDir() {
			return filepath.Join(nb, "results", label)
		}
	}
	if strings.TrimSpace(slotDir) != "" {
		return filepath.Join(slotDir, "results", label)
	}
	return filepath.Join("results", label)
}

// HasResultsMoved reports whether durable results have moved out of jobDir into resultsDir
// or have been marked harvested.
func HasResultsMoved(jobDir, resultsDir string) bool {
	if strings.TrimSpace(jobDir) != "" {
		// A .harvested marker means the harvester processed and moved results
		if _, err := os.Stat(filepath.Join(jobDir, ".harvested")); err == nil {
			return true
		}
	}
	if strings.TrimSpace(resultsDir) != "" {
		// If resultsDir has RESULT.md or harness log and jobDir does not have RESULT.md
		hasResInDst := fileExists(filepath.Join(resultsDir, ResultFileName)) || fileExists(filepath.Join(resultsDir, "harness-output.log"))
		hasResInSrc := strings.TrimSpace(jobDir) != "" && fileExists(filepath.Join(jobDir, ResultFileName))
		if hasResInDst && !hasResInSrc {
			return true
		}
	}
	return false
}

// RelocateJobResults moves durable result files from jobDir to resultsDir.
func RelocateJobResults(jobDir, resultsDir string) error {
	if strings.TrimSpace(jobDir) == "" || strings.TrimSpace(resultsDir) == "" {
		return fmt.Errorf("relocate results wants non-empty jobDir and resultsDir")
	}
	if err := os.MkdirAll(resultsDir, 0o755); err != nil {
		return fmt.Errorf("creating results dir %s: %w", resultsDir, err)
	}

	// Locate result file if under repo/
	if resPath, found := FindCardResult(jobDir); found {
		dst := filepath.Join(resultsDir, ResultFileName)
		if err := moveOrCopy(resPath, dst); err != nil {
			return fmt.Errorf("moving result file %s to %s: %w", resPath, dst, err)
		}
	}

	// Standard durable files to move
	candidates := []string{
		"harness-output.log",
		"harness.log",
		"native.log",
		"usage.tsv",
		TimelineFileName,
		"wall.md",
		"blocked.md",
		"REPORT.md",
		"branch.bundle",
	}

	entries, err := os.ReadDir(jobDir)
	if err == nil {
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			name := e.Name()
			match := false
			for _, c := range candidates {
				if name == c {
					match = true
					break
				}
			}
			if !match && strings.HasSuffix(name, ".md") {
				match = true
			}
			if match {
				src := filepath.Join(jobDir, name)
				dst := filepath.Join(resultsDir, name)
				_ = moveOrCopy(src, dst)
			}
		}
	}

	return nil
}

func moveOrCopy(src, dst string) error {
	if src == dst {
		return nil
	}
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	// Fallback to copy and remove across filesystem boundaries
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Sync(); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	in.Close()
	return os.Remove(src)
}

// CleanJobStorage atomically removes the job directory and sandbox tmp under slotDir.
// It explicitly refuses to remove shared caches (tmp/cache/{go-mod,go-build,npm}) and
// mirrors (~/nova-bench/mirror) and refuses any path outside slotDir.
func CleanJobStorage(jobDir, tmpDir, slotDir, root, benchHome string) error {
	if strings.TrimSpace(slotDir) == "" {
		return fmt.Errorf("clean job storage wants a non-empty slotDir")
	}

	// 1. Guard against protected paths (shared caches, mirrors, roots)
	if err := AssertNotProtectedStoragePath(jobDir, benchHome, root); err != nil {
		return err
	}
	if err := AssertNotProtectedStoragePath(tmpDir, benchHome, root); err != nil {
		return err
	}

	// 2. Remove jobDir if present
	if strings.TrimSpace(jobDir) != "" {
		if fi, err := os.Lstat(jobDir); err == nil {
			if fi.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("%w: jobDir is a symlink: %s", safepath.ErrUnsafe, jobDir)
			}
			// Atomic rename to staging under the same directory
			parent := filepath.Dir(jobDir)
			base := filepath.Base(jobDir)
			staged := filepath.Join(parent, fmt.Sprintf(".deleting-%s-%d", base, time.Now().UnixNano()))
			if err := os.Rename(jobDir, staged); err != nil {
				// Fallback to direct safepath removal if rename fails
				if err := safepath.RemoveUnder(slotDir, jobDir); err != nil {
					return fmt.Errorf("removing jobDir %s: %w", jobDir, err)
				}
			} else {
				if err := safepath.RemoveUnder(slotDir, staged); err != nil {
					return fmt.Errorf("removing staged jobDir %s: %w", staged, err)
				}
			}
		}
	}

	// 3. Remove tmpDir if present
	if strings.TrimSpace(tmpDir) != "" {
		if fi, err := os.Lstat(tmpDir); err == nil {
			if fi.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("%w: tmpDir is a symlink: %s", safepath.ErrUnsafe, tmpDir)
			}
			parent := filepath.Dir(tmpDir)
			base := filepath.Base(tmpDir)
			staged := filepath.Join(parent, fmt.Sprintf(".deleting-%s-%d", base, time.Now().UnixNano()))
			if err := os.Rename(tmpDir, staged); err != nil {
				if err := safepath.RemoveUnder(slotDir, tmpDir); err != nil {
					return fmt.Errorf("removing tmpDir %s: %w", tmpDir, err)
				}
			} else {
				if err := safepath.RemoveUnder(slotDir, staged); err != nil {
					return fmt.Errorf("removing staged tmpDir %s: %w", staged, err)
				}
			}
		}
	}

	return nil
}

// CleanupJobStorageAtCompletion performs job storage cleanup at card completion:
// When a card finishes (or is idle-reaped / wall-refused), if results have moved or for
// harness-written failures (idle-reaped, wall-refused) where nothing is kept beyond RESULT.md
// and harness log, remove the job directory including clone and sandbox tmp.
func CleanupJobStorageAtCompletion(opts JobStorageCleanupOpts) (JobStorageCleanResult, error) {
	resultsMoved := opts.ResultsMoved || HasResultsMoved(opts.JobDir, opts.ResultsDir)

	shouldClean := resultsMoved || opts.IsHarnessFailure
	if !shouldClean && opts.ResultsDir != "" {
		// If a resultsDir is configured and card finished, relocate results
		if err := RelocateJobResults(opts.JobDir, opts.ResultsDir); err == nil {
			resultsMoved = true
			shouldClean = true
		}
	}

	if !shouldClean {
		return JobStorageCleanResult{Cleaned: false}, nil
	}

	targetResultsDir := opts.ResultsDir
	if targetResultsDir == "" {
		targetResultsDir = DefaultResultsDir(opts.BenchHome, opts.SlotDir, opts.Label)
	}

	// For harness-written failures, preserve RESULT.md and harness log in results store
	if opts.IsHarnessFailure {
		_ = RelocateJobResults(opts.JobDir, targetResultsDir)
		resultsMoved = true
	}

	if opts.ReleaseLease != nil {
		opts.ReleaseLease()
	}

	if err := CleanJobStorage(opts.JobDir, opts.TmpDir, opts.SlotDir, opts.Root, opts.BenchHome); err != nil {
		return JobStorageCleanResult{Cleaned: false, ResultsMoved: resultsMoved, ResultsDir: targetResultsDir}, err
	}

	return JobStorageCleanResult{
		Cleaned:      true,
		JobRemoved:   opts.JobDir,
		TmpRemoved:   opts.TmpDir,
		ResultsMoved: resultsMoved,
		ResultsDir:   targetResultsDir,
	}, nil
}
