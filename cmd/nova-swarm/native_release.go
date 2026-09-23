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

// releaseNativeJob is how --sweep-now removes the job directory (nova-tools #2379,
// on top of #2632's publish). It runs only once publishNativeResults has named
// res.resultsDir, <results-root>/<label>/<runID>/<attempt>, and after the verdict
// and any wall line have read the job directory. The job's other regular files and
// the slot's native.log join the published ones there (a file already published is
// kept, not replaced), a commit the clone holds that is not in its base is bundled
// and checked, and only then are the job directory and its sandbox tmp removed. A
// release that cannot prove the record keeps the directory and says so.
//
// The returned path is the results directory when the job directory was removed,
// and empty when it was kept.
func releaseNativeJob(cfg nativeRunConfig, res nativeRunResult, errOut io.Writer) string {
	if strings.TrimSpace(res.job) == "" || strings.TrimSpace(res.resultsDir) == "" || !safepath.NameOK(cfg.label) {
		return ""
	}
	root := res.root
	if strings.TrimSpace(root) == "" {
		root = cfg.root
	}
	if abs, err := swarm.AbsResolved(root); err == nil {
		root = abs
	}
	resultsRoot := cfg.resultsRoot
	if strings.TrimSpace(resultsRoot) == "" {
		resultsRoot = filepath.Join(root, "results")
	}
	if abs, err := swarm.AbsResolved(resultsRoot); err == nil {
		resultsRoot = abs
	}
	slot := filepath.Dir(filepath.Dir(res.job))
	tmp := res.tmp
	if strings.TrimSpace(tmp) == "" {
		tmp = filepath.Join(slot, "tmp", cfg.label)
	}
	out, err := swarm.ReleaseJobDir(swarm.ReleaseJobInput{
		JobDir:      res.job,
		TmpDir:      tmp,
		SlotDir:     slot,
		Root:        root,
		ResultsDir:  res.resultsDir,
		ResultsRoot: resultsRoot,
		Label:       cfg.label,
		NativeLog:   filepath.Join(slot, "native.log"),
		BenchHome:   cfg.benchHome,
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
