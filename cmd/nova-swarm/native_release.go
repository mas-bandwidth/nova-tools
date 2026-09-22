package main

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/safepath"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// releaseNativeJob stores the card's durable record under <root>/results/<label>
// and removes the job directory and its sandbox tmp. It runs only after the
// verdict has been read off the job directory, and after a wall line has read
// the clone: those still need the directory. A release that cannot prove the
// record keeps the directory and says so. The results directory is under the
// --root the caller already named; a home directory is not guessed.
//
// The returned path is the results directory when the job directory was removed,
// and empty when it was kept.
func releaseNativeJob(cfg nativeRunConfig, res nativeRunResult, errOut io.Writer) string {
	if strings.TrimSpace(res.job) == "" || !safepath.NameOK(cfg.label) {
		return ""
	}
	root := cfg.root
	if abs, err := swarm.AbsResolved(root); err == nil {
		root = abs
	}
	slot := filepath.Dir(filepath.Dir(res.job))
	results := filepath.Join(root, "results", cfg.label)
	tmp := res.tmp
	if strings.TrimSpace(tmp) == "" {
		tmp = filepath.Join(slot, "tmp", cfg.label)
	}
	out, err := swarm.ReleaseJobDir(swarm.ReleaseJobInput{
		JobDir:     res.job,
		TmpDir:     tmp,
		SlotDir:    slot,
		Root:       root,
		ResultsDir: results,
		Label:      cfg.label,
		NativeLog:  filepath.Join(slot, "native.log"),
		BenchHome:  cfg.benchHome,
	})
	if err != nil {
		fmt.Fprintf(errOut, "NATIVE NOTE: the job directory %s was kept: %s\n", oneline.Field(res.job), oneline.Escape(err.Error()))
		return ""
	}
	if !out.Removed {
		return ""
	}
	fmt.Fprintf(errOut, "NATIVE NOTE: removed job directory %s after storing results at %s\n", oneline.Field(res.job), oneline.Field(out.Results))
	return out.Results
}
