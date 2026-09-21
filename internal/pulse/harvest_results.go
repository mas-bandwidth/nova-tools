package pulse

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// defaultResultsStore is the results directory on the bench relative to $HOME (Issue #2379 & #2407).
const defaultResultsStore = "nova-bench/results"

// resultsStoreDir returns the root of the results store:
// 1. in.ResultsDir if specified
// 2. NOVA_BENCH_RESULTS env var if set
// 3. ~/nova-bench/results (expanded from os.UserHomeDir())
func resultsStoreDir(customDir string) string {
	if customDir != "" {
		return customDir
	}
	if env := os.Getenv("NOVA_BENCH_RESULTS"); env != "" {
		return env
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(os.TempDir(), defaultResultsStore)
	}
	return filepath.Join(home, defaultResultsStore)
}

// isProtectedFromDeletion reports whether a path is protected and must NEVER be deleted.
// Essential 7 (Issue #2379): Shared caches (tmp/cache/...) and mirrors (~/nova-bench/mirror)
// must never be deleted under any circumstances. Roots, home, and system dirs are also guarded.
func isProtectedFromDeletion(dir, working string, roots string) (bool, string) {
	clean := filepath.Clean(dir)
	if clean == "" || clean == "." || clean == "/" || clean == "\\" {
		return true, "root or empty directory"
	}
	home, _ := os.UserHomeDir()
	if home != "" && (clean == filepath.Clean(home) || clean == filepath.Dir(filepath.Clean(home))) {
		return true, "home directory"
	}

	for _, sys := range []string{"/tmp", "/var", "/usr", "/etc", "/home", "/Users", "/bin", "/sbin", "/opt"} {
		if clean == filepath.Clean(sys) {
			return true, "system directory"
		}
	}

	slash := filepath.ToSlash(clean)

	// Shared caches: tmp/cache/... (e.g. tmp/cache/{go-mod,go-build,npm})
	if strings.Contains(slash, "/tmp/cache") || strings.Contains(slash, "tmp/cache") ||
		strings.Contains(slash, "/cache/") || strings.HasSuffix(slash, "/cache") ||
		filepath.Base(clean) == "cache" {
		return true, "shared cache under tmp/cache"
	}

	// Mirrors: ~/nova-bench/mirror
	if strings.Contains(slash, "nova-bench/mirror") || strings.Contains(slash, "/mirror/") ||
		strings.HasSuffix(slash, "/mirror") || filepath.Base(clean) == "mirror" ||
		strings.HasSuffix(clean, ".git") {
		return true, "bare mirror under mirror"
	}

	if working != "" {
		wClean := filepath.Clean(working)
		if clean == wClean || clean == filepath.Join(wClean, "tmp") {
			return true, "working root"
		}
	}

	for _, r := range strings.Split(roots, ",") {
		r = strings.TrimSpace(r)
		if r != "" && clean == filepath.Clean(r) {
			return true, "swarm root"
		}
	}

	return false, ""
}

// finishedJobDir determines the directory to delete when a card finishes folding.
// For sprint working layout: <working>/tmp/<guid>-<label>/jobs/<label> -> <working>/tmp/<guid>-<label>
// if that slot contains only this job in jobs/.
// Otherwise returns clean jobDir.
func finishedJobDir(jobDir string) string {
	clean := filepath.Clean(jobDir)
	jobsDir := filepath.Dir(clean)
	if filepath.Base(jobsDir) == "jobs" {
		slotDir := filepath.Dir(jobsDir)
		entries, err := os.ReadDir(jobsDir)
		if err == nil {
			hasOtherJobs := false
			label := filepath.Base(clean)
			for _, e := range entries {
				if e.IsDir() && e.Name() != label {
					hasOtherJobs = true
					break
				}
			}
			if !hasOtherJobs {
				return slotDir
			}
		}
	}
	return clean
}

// copyOrMoveFile copies content from src to dst and removes src if successful.
func copyOrMoveFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		return err
	}
	_ = os.Remove(src)
	return nil
}

// relocateHarvestedWorking relocates durable outputs for a finished job into
// <resultsStore>/<label>/ and removes the finished job directory (including clone and sandbox tmp).
func relocateHarvestedWorking(j harvestJob, in HarvestInput) error {
	resultsRoot := resultsStoreDir(in.ResultsDir)
	destDir := filepath.Join(resultsRoot, j.label)
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return fmt.Errorf("creating results dir %s: %w", destDir, err)
	}

	// 1. Result identity & archive invariant:
	// <label> serves as the canonical record alias. If a prior RESULT.md already exists
	// (e.g. from an earlier attempt, retry, or previous run), archive the prior outputs
	// under destDir/attempts/<timestamp> to prevent overwriting evidence across retries.
	if oldRes, err := os.ReadFile(filepath.Join(destDir, "RESULT.md")); err == nil && len(oldRes) > 0 {
		stamp := in.Now().UTC().Format("20060102T150405Z")
		attemptDir := filepath.Join(destDir, "attempts", stamp)
		_ = os.MkdirAll(attemptDir, 0o755)
		for _, f := range []string{"RESULT.md", "notes.txt", "usage.tsv", "harness.log", "native.log", "branch.bundle", "git-ref"} {
			oldPath := filepath.Join(destDir, f)
			if d, err := os.ReadFile(oldPath); err == nil {
				_ = os.WriteFile(filepath.Join(attemptDir, f), d, 0o644)
			}
		}
	}

	// 2. Move / write RESULT.md
	resSrc := filepath.Join(j.dir, "RESULT.md")
	if data, err := os.ReadFile(resSrc); err == nil && len(data) > 0 {
		_ = os.WriteFile(filepath.Join(destDir, "RESULT.md"), data, 0o644)
	} else if err != nil {
		return fmt.Errorf("missing RESULT.md at %s: %w", resSrc, err)
	} else {
		return fmt.Errorf("RESULT.md at %s is empty", resSrc)
	}

	slotDir := ""
	if filepath.Base(filepath.Dir(j.dir)) == "jobs" {
		slotDir = filepath.Dir(filepath.Dir(j.dir))
	}

	// 3. notes.txt
	for _, p := range []string{
		filepath.Join(j.dir, "notes.txt"),
		filepath.Join(cloneDir(j.dir), "notes.txt"),
		filepath.Join(slotDir, "notes.txt"),
	} {
		if slotDir == "" && p == filepath.Join(slotDir, "notes.txt") {
			continue
		}
		if data, err := os.ReadFile(p); err == nil {
			_ = os.WriteFile(filepath.Join(destDir, "notes.txt"), data, 0o644)
			break
		}
	}

	// 4. usage.tsv
	for _, p := range []string{
		filepath.Join(j.dir, "usage.tsv"),
		filepath.Join(slotDir, "usage.tsv"),
	} {
		if slotDir == "" && p == filepath.Join(slotDir, "usage.tsv") {
			continue
		}
		if data, err := os.ReadFile(p); err == nil {
			_ = os.WriteFile(filepath.Join(destDir, "usage.tsv"), data, 0o644)
			break
		}
	}

	// 5. harness.log / native.log
	for _, p := range []string{
		filepath.Join(j.dir, "harness.log"),
		filepath.Join(j.dir, "harness-output.log"),
		filepath.Join(j.dir, "native.log"),
		filepath.Join(slotDir, "native.log"),
	} {
		if slotDir == "" && p == filepath.Join(slotDir, "native.log") {
			continue
		}
		if data, err := os.ReadFile(p); err == nil {
			_ = os.WriteFile(filepath.Join(destDir, "harness.log"), data, 0o644)
			break
		}
	}
	if slotDir != "" {
		if data, err := os.ReadFile(filepath.Join(slotDir, "native.log")); err == nil {
			_ = os.WriteFile(filepath.Join(destDir, "native.log"), data, 0o644)
		}
	}

	// 6. git ref / bundle
	bundlePath := filepath.Join(destDir, "branch.bundle")
	for _, p := range []string{
		filepath.Join(j.dir, "branch.bundle"),
		filepath.Join(j.dir, "git.bundle"),
		filepath.Join(slotDir, "branch.bundle"),
	} {
		if slotDir == "" && p == filepath.Join(slotDir, "branch.bundle") {
			continue
		}
		if data, err := os.ReadFile(p); err == nil {
			_ = os.WriteFile(bundlePath, data, 0o644)
			break
		}
	}
	clone := cloneDir(j.dir)
	if _, err := os.Stat(bundlePath); err != nil && clone != "" {
		if fi, err := os.Stat(filepath.Join(clone, ".git")); err == nil || fi != nil {
			_, _ = runChild(clone, nil, "git", "bundle", "create", bundlePath, "HEAD")
		}
	}
	headSHA := ""
	if clone != "" {
		if sha, err := runChild(clone, nil, "git", "rev-parse", "HEAD"); err == nil {
			f := strings.Fields(sha)
			if len(f) > 0 {
				headSHA = f[0]
				_ = os.WriteFile(filepath.Join(destDir, "git-ref"), []byte(headSHA+"\n"), 0o644)
			}
		}
	}

	// 7. manifest.tsv recording card identity and attempt metadata
	manifestLine := fmt.Sprintf("%s\t%s\t%s\t%s\n", in.Now().UTC().Format(time.RFC3339), j.label, j.dir, headSHA)
	_ = os.WriteFile(filepath.Join(destDir, "manifest.tsv"), []byte(manifestLine), 0o644)

	// 8. .harvested marker in results store
	_ = os.WriteFile(filepath.Join(destDir, ".harvested"), nil, 0o644)

	// 9. Readback / atomic check: verify RESULT.md is present and NON-EMPTY in results dir
	// before removing working storage. A zero-byte write fails closed.
	fi, err := os.Stat(filepath.Join(destDir, "RESULT.md"))
	if err != nil {
		return fmt.Errorf("readback check failed: RESULT.md missing in %s: %w", destDir, err)
	}
	if fi.Size() == 0 {
		return fmt.Errorf("readback check failed: RESULT.md in %s is 0 bytes", destDir)
	}
	verifyBytes, err := os.ReadFile(filepath.Join(destDir, "RESULT.md"))
	if err != nil || len(verifyBytes) == 0 {
		return fmt.Errorf("readback check failed: cannot read non-empty bytes from %s: %w", destDir, err)
	}

	// 10. Delete the finished job directory (including clone and sandbox tmp)
	targetDir := finishedJobDir(j.dir)
	if protected, reason := isProtectedFromDeletion(targetDir, in.Working, in.Roots); protected {
		return fmt.Errorf("refusing to delete protected directory %s (%s)", targetDir, reason)
	}
	if err := os.RemoveAll(targetDir); err != nil {
		return fmt.Errorf("removing finished job dir %s: %w", targetDir, err)
	}
	return nil
}

// benchRelocateScript builds the shell script executed on the bench to move durable outputs
// and delete the finished job directory (Essential 7 from #2379 & #2407).
func benchRelocateScript(jobDir, label, branch, resultsStore string) string {
	var rExpr string
	if resultsStore == "" {
		rExpr = `"$HOME/` + defaultResultsStore + `/` + label + `"`
	} else {
		rExpr = shellQuote(filepath.Join(resultsStore, label))
	}
	qJ := shellQuote(jobDir)

	return fmt.Sprintf(`R=%s; J=%s; mkdir -p "$R" || exit 1
if [ -f "$R/RESULT.md" ]; then
  A="$R/attempts/$(date -u +%%Y%%m%%dT%%H%%M%%SZ 2>/dev/null || echo prev)"
  mkdir -p "$A"
  for f in RESULT.md notes.txt usage.tsv harness.log native.log branch.bundle git-ref; do
    [ -f "$R/$f" ] && cp -p "$R/$f" "$A/$f" 2>/dev/null || true
  done
fi
if [ -d "$J/repo" ]; then
  git -C "$J/repo" bundle create "$R/branch.bundle" HEAD 2>/dev/null || true
  git -C "$J/repo" rev-parse HEAD > "$R/git-ref" 2>/dev/null || true
elif [ -d "$J/.git" ]; then
  git -C "$J" bundle create "$R/branch.bundle" HEAD 2>/dev/null || true
  git -C "$J" rev-parse HEAD > "$R/git-ref" 2>/dev/null || true
fi
[ -f "$J/RESULT.md" ] && cp -p "$J/RESULT.md" "$R/RESULT.md"
if [ -f "$J/notes.txt" ]; then cp -p "$J/notes.txt" "$R/notes.txt"; elif [ -f "$J/repo/notes.txt" ]; then cp -p "$J/repo/notes.txt" "$R/notes.txt"; fi
P="$(dirname "$(dirname "$J")")"
if [ -f "$J/usage.tsv" ]; then cp -p "$J/usage.tsv" "$R/usage.tsv"; elif [ -f "$P/usage.tsv" ]; then cp -p "$P/usage.tsv" "$R/usage.tsv"; fi
if [ -f "$J/harness.log" ]; then cp -p "$J/harness.log" "$R/harness.log"; elif [ -f "$J/harness-output.log" ]; then cp -p "$J/harness-output.log" "$R/harness.log"; elif [ -f "$J/native.log" ]; then cp -p "$J/native.log" "$R/harness.log"; elif [ -f "$P/native.log" ]; then cp -p "$P/native.log" "$R/harness.log"; fi
touch "$R/.harvested"
if [ -f "$R/RESULT.md" ] && [ -s "$R/RESULT.md" ]; then
  S="$J"
  if [ "$(basename "$(dirname "$J")")" = "jobs" ]; then
    if [ -d "$P" ] && [ "$P" != "/" ] && [ "$P" != "$HOME" ]; then
      cnt=0
      for d in "$P/jobs"/*/; do [ -d "$d" ] && cnt=$((cnt+1)); done
      if [ "$cnt" -le 1 ]; then S="$P"; fi
    fi
  fi
  case "$S" in
    *cache*|*mirror*|""|"/"|"$HOME"|"/home"|"/tmp"|"/var"|*".git")
      ;;
    *)
      rm -rf "$S"
      ;;
  esac
fi`, rExpr, qJ)
}

// relocateHarvestedBench relocates durable outputs into results store on the bench
// and deletes the finished job directory over the shell seam.
func relocateHarvestedBench(shell BenchShell, bench, dir, label, branch, resultsStore string) error {
	script := benchRelocateScript(dir, label, branch, resultsStore)
	_, err := shell.Run(bench, script)
	return err
}
