package cardcontract

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestJobMDNamesTheWorkerPlaybookFirst pins R-WORKER-PLAYBOOK: in every profile, for a
// work card and a read, the JOB.md Install writes has the worker playbook line as its
// first instruction, the line right after the title, word for word the same, once.
func TestJobMDNamesTheWorkerPlaybookFirst(t *testing.T) {
	t.Parallel()
	const want = "Read nova-sprint help worker-playbook first: how to take, work, finish and report, and your harness's section."
	for _, family := range Families {
		for _, kind := range []string{"work", "read"} {
			t.Run(family+"/"+kind, func(t *testing.T) {
				t.Parallel()
				job := t.TempDir()
				f := Frame{Kind: kind, Card: "c1", Attempt: 1, Model: family, Repo: "https://example.invalid/r.git", BaseRef: "main", ReviewBase: "main", Branch: "sprint/c1.g1.e1"}
				s := Staged{Job: job, Repo: filepath.Join(job, "repo"), Head: strings.Repeat("a", 40), Git: "/usr/bin/git"}
				require.NoError(t, Install(For(family), f, s, filepath.Join(job, ".shims")))
				raw, err := os.ReadFile(filepath.Join(job, JobName))
				require.NoError(t, err)
				lines := strings.Split(string(raw), "\n")
				title := "# JOB: "
				if kind == "read" {
					title = ReadTitle
				}
				assert.True(t, strings.HasPrefix(lines[0], title), "%s %s: line 1 is the title %q: %q", family, kind, title, lines[0])
				first := ""
				for _, l := range lines[1:] {
					if strings.TrimSpace(l) != "" {
						first = l
						break
					}
				}
				assert.Equal(t, want, first, "%s %s: the first instruction names the worker playbook", family, kind)
				assert.Equal(t, 1, strings.Count(string(raw), want), "%s %s: the playbook line is written once", family, kind)
			})
		}
	}
}
