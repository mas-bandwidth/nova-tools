package swarm

import (
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/pkg/gitrun"
	"github.com/mas-bandwidth/nova-tools/pkg/goenv"
	"github.com/mas-bandwidth/nova-tools/pkg/safepath"
	"github.com/mas-bandwidth/nova-tools/pkg/subproc"
)

// LispCacheDir is the shared ASDF output directory under a swarm root.
func LispCacheDir(root string) string { return filepath.Join(root, CacheDirName, "common-lisp") }

// JobLispCacheDir is the private writable overlay for one card. It lives beside the
// card's checkout, inside the already-owned job directory, and is reaped with that job.
func JobLispCacheDir(sourceRoot string) string {
	return filepath.Join(filepath.Dir(filepath.Clean(sourceRoot)), ".cache", "common-lisp")
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
	// ignored: a deferred cleanup of this call's own temp directory, below its parent
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
			return fmt.Errorf("lisp seed entry %s is not a regular file", path)
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

func fullHexSHA(s string) bool {
	if len(s) != 40 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

func gitOutput(dir string, args ...string) (string, error) {
	return gitrun.Output(context.Background(), gitrun.Options{Dir: dir, Env: append(goenv.WithoutSecrets(os.Environ()), "GIT_TERMINAL_PROMPT=0"), Timeout: subproc.GitLongBudget}, args...)
}
