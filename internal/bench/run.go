package bench

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// RunAgeLimit is the duration after which an inactive bench run directory with
// no live process is considered a leak and swept.
const RunAgeLimit = 2 * time.Hour

// DefaultFloorGB is the minimum disk headroom in GiB required on a bench by default.
const DefaultFloorGB = 10

// RunPath returns the canonical run directory path for kind and id under root:
// <root>/runs/<kind>-<id>. If root is already a runs directory, it is made directly
// under root.
func RunPath(root, kind, id string) string {
	clean := filepath.Clean(root)
	if strings.HasSuffix(clean, "/runs") || clean == "runs" {
		return clean + "/" + kind + "-" + id
	}
	return clean + "/runs/" + kind + "-" + id
}

// MakeRunLine returns the remote shell line that creates tree/, tmp/ and gocache/
// subdirectories inside the run directory and prints the run directory path.
func MakeRunLine(root, kind, id string) string {
	dir := RunPath(root, kind, id)
	return "mkdir -p " + Quote(dir+"/tree") + " " + Quote(dir+"/tmp") + " " + Quote(dir+"/gocache") + " && echo " + Quote(dir)
}

// CheckRunDir validates that dir is a safe, non-climbing run directory strictly
// located under root. It prevents variable path or escape vulnerabilities.
func CheckRunDir(root, dir string) error {
	if err := CheckPath("run", dir); err != nil {
		return err
	}
	cleanDir := filepath.Clean(dir)
	cleanRoot := filepath.Clean(root)
	if cleanDir == cleanRoot || cleanDir == "." || cleanDir == "/" {
		return fmt.Errorf("run dir %q is the root or filesystem root itself", dir)
	}
	cleanRootRuns := cleanRoot
	if !strings.HasSuffix(cleanRootRuns, "/runs") && cleanRootRuns != "runs" {
		cleanRootRuns += "/runs"
	}
	if !strings.HasPrefix(cleanDir, cleanRoot+"/") && !strings.HasPrefix(cleanDir, cleanRootRuns+"/") {
		return fmt.Errorf("run dir %q is not under root %q", dir, root)
	}
	return nil
}

// FloorLine returns the command to check free disk space on root's filesystem.
func FloorLine(root string) string {
	return "df -Pk " + Quote(root)
}

// parseDFOutput extracts the available disk space in GiB from df -Pk output.
func parseDFOutput(out string) (float64, bool) {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) < 2 {
		return 0, false
	}
	for i := len(lines) - 1; i >= 1; i-- {
		fields := strings.Fields(lines[i])
		if len(fields) >= 4 {
			kib, err := strconv.ParseInt(fields[3], 10, 64)
			if err == nil {
				return float64(kib) / (1024 * 1024), true
			}
		}
	}
	return 0, false
}

// CheckFloor checks whether the filesystem hosting root on host has at least floorGB
// GiB of available disk space. If under the floor, it returns an error describing the shortage.
func CheckFloor(ctx context.Context, t Transport, host, root string, floorGB int) (float64, error) {
	if floorGB <= 0 {
		return 0, nil
	}
	ctx, cancel := context.WithTimeout(ctx, StepBudget)
	defer cancel()
	var out, errb bytes.Buffer
	code, err := t.Shell(ctx, host, FloorLine(root), &out, &errb)
	if err != nil {
		return 0, fmt.Errorf("%s: checking disk space: %w", host, err)
	}
	if code != 0 {
		return 0, fmt.Errorf("%s: df exit %d: %s", host, code, lastLine(errb.String()))
	}
	freeGB, ok := parseDFOutput(out.String())
	if !ok {
		return 0, fmt.Errorf("%s: could not parse df output: %s", host, strings.TrimSpace(out.String()))
	}
	if freeGB < float64(floorGB) {
		return freeGB, fmt.Errorf("%s: disk %.1f GiB, red under the floor of %d GiB", host, freeGB, floorGB)
	}
	return freeGB, nil
}

// SweepListLine returns the remote shell command to list candidate runs with their
// modification times (seconds since epoch) under <root>/runs/* and <root>/*/*.
func SweepListLine(root string) string {
	clean := filepath.Clean(root)
	runs := clean + "/runs"
	return "stat -c '%Y %n' " + Quote(runs) + "/* " + Quote(clean) + "/*/* 2>/dev/null || true"
}

// ProcessCheckLine returns the command that checks if a live process is using dir.
func ProcessCheckLine(dir string) string {
	return "fuser " + Quote(dir) + " 2>/dev/null"
}

// SweepRuns sweeps run directories under root on host that are older than bound and have
// no live process using them. For each removed directory, it prints 'REMOVED <path>' to out.
func SweepRuns(ctx context.Context, t Transport, host, root string, bound time.Duration, now time.Time, out io.Writer) ([]string, error) {
	if out == nil {
		out = io.Discard
	}
	ctx, cancel := context.WithTimeout(ctx, StepBudget*5)
	defer cancel()

	var listOut, listErr bytes.Buffer
	code, err := t.Shell(ctx, host, SweepListLine(root), &listOut, &listErr)
	if err != nil {
		return nil, fmt.Errorf("%s: listing runs for sweep: %w", host, err)
	}
	if code != 0 {
		return nil, fmt.Errorf("%s: listing runs exit %d: %s", host, code, lastLine(listErr.String()))
	}

	var removed []string
	lines := strings.Split(strings.TrimSpace(listOut.String()), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		sec, err := strconv.ParseInt(fields[0], 10, 64)
		if err != nil {
			continue
		}
		dir := fields[1]
		mtime := time.Unix(sec, 0)
		if now.Sub(mtime) < bound {
			continue
		}

		// Check if a process is using dir
		var pOut, pErr bytes.Buffer
		pCode, pErrVal := t.Shell(ctx, host, ProcessCheckLine(dir), &pOut, &pErr)
		if pErrVal != nil {
			continue
		}
		if pCode == 0 && strings.TrimSpace(pOut.String()) != "" {
			continue
		}
		if pCode != 1 {
			continue
		}

		// Verify dir is safely under root before removing
		if err := CheckRunDir(root, dir); err != nil {
			continue
		}

		var rmErr bytes.Buffer
		rmCode, rmErrVal := t.Shell(ctx, host, RemoveLine(dir), io.Discard, &rmErr)
		if rmErrVal == nil && rmCode == 0 {
			removed = append(removed, dir)
			fmt.Fprintf(out, "REMOVED %s\n", dir)
		}
	}
	return removed, nil
}
