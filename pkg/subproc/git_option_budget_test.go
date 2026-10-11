package subproc_test

import (
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/subproc"
	"github.com/stretchr/testify/assert"
)

// TestGitNetworkBudgetSkipsRepositoryOptionValues: --git-dir and --work-tree name the
// repository a following verb acts on, so the budget reader consumes their value like
// the values of -C and -c, in the separated form and, as a control, the equals form; a
// path value spelled like a network verb stays a value, never the subcommand.
func TestGitNetworkBudgetSkipsRepositoryOptionValues(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		name string
		args []string
		want time.Duration
	}{
		{"git-dir separated before fetch", []string{"--git-dir", "/repo/.git", "fetch", "origin"}, subproc.GitLongBudget},
		{"git-dir separated before push", []string{"--git-dir", "/repo/.git", "push", "origin", "main"}, subproc.GitLongBudget},
		{"work-tree separated before fetch", []string{"--work-tree", "/work", "fetch"}, subproc.GitLongBudget},
		{"work-tree separated before push", []string{"--work-tree", "/work", "push", "origin", "main"}, subproc.GitLongBudget},
		{"mixed leading options before pull", []string{"-C", "/repo", "--git-dir", "/repo/.git", "-c", "core.sshCommand=ssh", "--no-pager", "pull", "--ff-only"}, subproc.GitLongBudget},
		{"git-dir equals form before fetch", []string{"--git-dir=/repo/.git", "fetch"}, subproc.GitLongBudget},
		{"work-tree equals form before push", []string{"--work-tree=/work", "push"}, subproc.GitLongBudget},
		{"git-dir value spelled fetch stays a path", []string{"--git-dir", "fetch", "rev-parse", "HEAD"}, subproc.GitBudget},
		{"work-tree value spelled fetch stays a path", []string{"--work-tree", "fetch", "status", "--porcelain"}, subproc.GitBudget},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, c.want, subproc.GitBudgetFor(c.args), "GitBudgetFor(%v)", c.args)
			assert.Equal(t, c.want, subproc.BudgetOf("git", c.args), "BudgetOf(git, %v)", c.args)
		})
	}
}
