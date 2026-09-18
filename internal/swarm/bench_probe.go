// Bench probe: a card-shaped probe per bench (clone, go test one package, write beside
// repo, print the verdict), run by `nova-swarm bench probe --benches <file> --bench <name>`
// and by batch before its first card on a bench. A bench whose probe fails carries no card
// (ABSTAIN reason=bench-probe). The probe result is cached per (bench, binary sha256) in
// <root>/bench-probe.tsv.
package swarm

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// BenchProbeResult holds the outcome of one bench probe.
type BenchProbeResult struct {
	GoVer  string // go version output
	TestOK bool   // go test passed
	FileOK bool   // scratch/probe.txt written
	Bench  string // bench name
	BinSHA string // binary sha256
}

// ProbeBench runs the card-shaped probe on one bench. For the local bench it runs
// directly; for a remote bench it runs over ssh. scratchDir is "$OLDPWD/scratch" for
// temp files. repoURL is the repository to clone and test.
func ProbeBench(benchName, host, benchRoot, binaryPath, repoURL, scratchDir string) (BenchProbeResult, error) {
	res := BenchProbeResult{Bench: benchName}

	// Compute binary sha256 for the cache key.
	if binaryPath != "" {
		sum, err := BinarySHA256(binaryPath)
		if err == nil {
			res.BinSHA = sum
		}
	}

	if host == "" || host == "local" {
		return probeLocal(benchName, binaryPath, repoURL, scratchDir, res)
	}
	return probeRemote(benchName, host, benchRoot, binaryPath, repoURL, res)
}

// probeLocal runs the probe on the local machine.
func probeLocal(benchName, binaryPath, repoURL, scratchDir string, res BenchProbeResult) (BenchProbeResult, error) {
	// go version
	out, err := exec.Command("go", "version").CombinedOutput()
	res.GoVer = strings.TrimSpace(string(out))
	if err != nil {
		res.TestOK = false
		res.FileOK = false
		return res, fmt.Errorf("go version: %w", err)
	}

	// Find or clone the repo.
	repoDir, err := ensureRepoLocal(repoURL, scratchDir)
	if err != nil {
		res.TestOK = false
		res.FileOK = false
		return res, fmt.Errorf("ensure repo: %w", err)
	}

	// go test ./internal/oneline/
	testOut, err := exec.Command("go", "test", "./internal/oneline/").CombinedOutput()
	res.TestOK = err == nil
	_ = testOut

	// Write scratch/probe.txt
	if scratchDir == "" {
		scratchDir = os.Getenv("OLDPWD")
		if scratchDir != "" {
			scratchDir = filepath.Join(scratchDir, "scratch")
		}
	}
	if scratchDir == "" {
		// Fallback: use the repo's parent's scratch
		scratchDir = filepath.Join(filepath.Dir(repoDir), "scratch")
	}
	if err := os.MkdirAll(scratchDir, 0o755); err != nil {
		res.FileOK = false
		return res, nil // test result is what matters
	}
	probeTxt := fmt.Sprintf("bench=%s\ngo=%s\ntest=%v\n", benchName, res.GoVer, res.TestOK)
	if err := os.WriteFile(filepath.Join(scratchDir, "probe.txt"), []byte(probeTxt), 0o644); err != nil {
		res.FileOK = false
		return res, nil
	}
	res.FileOK = true

	return res, nil
}

// probeRemote runs the probe on a remote bench over ssh.
func probeRemote(benchName, host, benchRoot, binaryPath, repoURL string, res BenchProbeResult) (BenchProbeResult, error) {
	// Run the probe via ssh: go version, go test, write probe.txt
	script := fmt.Sprintf(`set -e
cd %s
GO_VER=$(go version 2>&1)
echo "go: $GO_VER"
cd repo 2>/dev/null || true
if [ -d .git ] || [ -d repo/.git ]; then
  cd repo 2>/dev/null || true
fi
TEST_OUT=$(go test ./internal/oneline/ 2>&1) && TEST_RC=0 || TEST_RC=$?
echo "test: $([ $TEST_RC -eq 0 ] && echo ok || echo FAIL)"
SCRATCH="%s/scratch"
mkdir -p "$SCRATCH"
printf 'bench=%s\ngo=%%s\ntest=%%s\n' "$GO_VER" "$([ $TEST_RC -eq 0 ] && echo ok || echo FAIL)" > "$SCRATCH/probe.txt"
echo "file: ok"
echo "BENCH PROBE bench=%s go=$GO_VER test=$([ $TEST_RC -eq 0 ] && echo ok || echo FAIL) file=ok"
`, benchRoot, benchRoot, benchName, benchName)

	out, err := exec.Command("ssh", host, "sh", "-c", script).CombinedOutput()
	output := string(out)
	_ = err // parse output regardless

	return parseProbeRemoteOutput(output, benchName, res), nil
}

// parseProbeRemoteOutput extracts probe results from the remote output.
func parseProbeRemoteOutput(output, benchName string, res BenchProbeResult) BenchProbeResult {
	res.GoVer = "unknown"
	res.TestOK = false
	res.FileOK = false
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "BENCH PROBE ") {
			for _, f := range strings.Fields(line) {
				if k, v, ok := strings.Cut(f, "="); ok {
					switch k {
					case "go":
						res.GoVer = v
					case "test":
						res.TestOK = v == "ok"
					case "file":
						res.FileOK = v == "ok"
					}
				}
			}
			return res
		}
		if strings.HasPrefix(line, "go: ") {
			res.GoVer = strings.TrimPrefix(line, "go: ")
		}
		if strings.HasPrefix(line, "test: ") {
			res.TestOK = strings.TrimPrefix(line, "test: ") == "ok"
		}
		if strings.HasPrefix(line, "file: ") {
			res.FileOK = strings.TrimPrefix(line, "file: ") == "ok"
		}
	}
	return res
}

// ensureRepoLocal finds or clones the repo for the probe.
func ensureRepoLocal(repoURL, scratchDir string) (string, error) {
	// Check if we're in a repo already.
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(filepath.Join(cwd, ".git")); err == nil {
		return cwd, nil
	}
	if _, err := os.Stat(filepath.Join(cwd, "repo", ".git")); err == nil {
		return filepath.Join(cwd, "repo"), nil
	}
	// Try to clone into repo/.
	repoDir := filepath.Join(cwd, "repo")
	if _, err := os.Stat(repoDir); os.IsNotExist(err) {
		if repoURL == "" {
			return "", fmt.Errorf("no repo found and no repo URL to clone")
		}
		if err := exec.Command("git", "clone", "-q", repoURL, repoDir).Run(); err != nil {
			return "", fmt.Errorf("clone: %w", err)
		}
		return repoDir, nil
	}
	return "", fmt.Errorf("no .git found in %s or %s", cwd, filepath.Join(cwd, "repo"))
}

// ReadBenchProbeCache reads the probe cache from <root>/bench-probe.tsv and returns
// whether the given (bench, binarySHA) pair has a passing, fresh entry.
func ReadBenchProbeCache(root, benchName, binarySHA string) bool {
	path := filepath.Join(root, "bench-probe.tsv")
	raw, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	key := benchName + "\t" + binarySHA
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "\t", 4)
		if len(parts) < 4 {
			continue
		}
		if parts[0]+"\t"+parts[1] == key && parts[3] == "ok" {
			if ts, err := time.Parse(time.RFC3339, parts[2]); err == nil {
				if time.Since(ts) < time.Hour {
					return true
				}
			}
		}
	}
	return false
}

// WriteBenchProbeCache appends a probe result to <root>/bench-probe.tsv.
func WriteBenchProbeCache(root, benchName, binarySHA, result string) error {
	path := filepath.Join(root, "bench-probe.tsv")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	line := fmt.Sprintf("%s\t%s\t%s\t%s\n", benchName, binarySHA, time.Now().UTC().Format(time.RFC3339), result)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(line)
	return err
}

// BinarySHA256 returns the lowercase hex sha256 of a binary file.
func BinarySHA256(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

// BenchProbeLine formats the BENCH PROBE output line.
func (r BenchProbeResult) BenchProbeLine() string {
	testWord := "FAIL"
	if r.TestOK {
		testWord = "ok"
	}
	fileWord := "FAIL"
	if r.FileOK {
		fileWord = "ok"
	}
	return fmt.Sprintf("BENCH PROBE bench=%s go=%s test=%s file=%s", r.Bench, r.GoVer, testWord, fileWord)
}

// ProbeOK reports whether the probe passed all checks.
func (r BenchProbeResult) ProbeOK() bool {
	return r.TestOK && r.FileOK
}
