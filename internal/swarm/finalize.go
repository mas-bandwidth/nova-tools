package swarm

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

// FINALIZE IS THE RUNNER'S STEP AFTER EVERY END -- exit, reap, budget, violation -- once the
// process group is dead, and its ORDER is the rule (rule 12):
//
//	1. the usage file, <pool>/usage/<job>.tsv, outside everything reclaim removes
//	2. the published report, copied byte for byte to <pool>/reports/<job>/RESULT.md, or a
//	   MALFORMED or NO-RESULT marker in its place, and a REV beside it holding the attempt
//	   and the SHA-256 of what was copied
//	3. ONLY THEN the job's files move to done/ or failed/
//	4. ONLY THEN the job's RUN line is printed
//
// A completed job reclaimed before the first triage lost its only RESULT.md once. So triage
// and `result --id` read <pool>/reports/<job>/ for a finalized job and <job>/RESULT.md only
// for a running one, and the first triage after a reclaim reads the same bytes it would
// have read before it.

// The three names under <pool>/reports/<job>/.
const (
	MarkerMalformed = "MALFORMED"
	MarkerNoResult  = "NO-RESULT"
	MarkerRev       = "REV"
	CopiedResult    = "RESULT.md"
)

// Ending is everything finalize needs to know about a job that has ended.
type Ending struct {
	Sidecar  Sidecar
	JobDir   string
	Provider string
	Model    string
	End      string
	RC       int // -1 is no exit code: unknown, or a launch that never happened
	Started  time.Time
	Ended    time.Time
	Usage    ProviderUsage
	Repo     string
}

// Finalized is what finalize did, so the caller can print one line about it.
type Finalized struct {
	UsagePath     string
	UsageExisted  bool
	Class         string
	MalformedLine int
	Hash          string
	Report        Report
	Published     bool
}

func treeBytes(dir string) int64 {
	var n int64
	// ignored: a size estimate; an unreadable entry is skipped and counts as zero bytes
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if info, err := d.Info(); err == nil {
			n += info.Size()
		}
		return nil
	})
	return n
}

func dashOr(s string) string {
	if strings.TrimSpace(s) == "" {
		return Dash
	}
	return s
}
