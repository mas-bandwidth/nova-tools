package member

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/cardtree"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveStepCommitWalksTheBranch(t *testing.T) {
	t.Parallel()
	one := "abc11111" + strings.Repeat("a", 32)
	two := "abc11112" + strings.Repeat("b", 32)
	only := "123456789" + strings.Repeat("c", 31)
	lone := "fff999999" + strings.Repeat("9", 31)
	require.Len(t, one, 40)
	require.Len(t, two, 40)
	require.Len(t, only, 40)
	require.Len(t, lone, 40)
	commits := []string{one, two, only, lone}
	shared := "abc1111"
	run := func(_ context.Context, _ string, args ...string) (string, string, error) {
		switch gitSub(args) {
		case "rev-list":
			return strings.Join(commits, "\n") + "\n", "", nil
		case "rev-parse":
			pref := strings.TrimSuffix(args[len(args)-1], "^{commit}")
			var hits []string
			for _, c := range commits {
				if strings.HasPrefix(c, pref) {
					hits = append(hits, c)
				}
			}
			quiet := false
			for _, a := range args {
				if a == "--quiet" {
					quiet = true
				}
			}
			if pref == lone[:9] || len(hits) > 1 {
				if quiet {
					return "", "", errors.New("exit status 1")
				}
				return "", "error: short object ID " + pref + " is ambiguous\nfatal: Needed a single revision\n", errors.New("exit status 128")
			}
			if len(hits) == 1 {
				return hits[0] + "\n", "", nil
			}
			return "", "fatal: Needed a single revision\n", errors.New("exit status 128")
		default:
			return "", "", fmt.Errorf("unexpected git %v", args)
		}
	}
	branch := "work/c1"
	got, err := resolveStepCommit(context.Background(), "checkout", branch, only[:9], run)
	require.NoError(t, err)
	assert.Equal(t, only, got, "a nine-hex prefix is the one commit of the branch")
	got, err = resolveStepCommit(context.Background(), "checkout", branch, one, run)
	require.NoError(t, err)
	assert.Equal(t, one, got, "a full sha on the branch is that sha")

	_, err = resolveStepCommit(context.Background(), "checkout", branch, shared, run)
	var miss *cardtree.CommitMiss
	require.ErrorAs(t, err, &miss)
	assert.Equal(t, "ambiguous", miss.Kind)
	assert.Contains(t, err.Error(), "the step line's commit "+shared+" is ambiguous on "+branch)

	_, err = resolveStepCommit(context.Background(), "checkout", branch, lone[:9], run)
	require.ErrorAs(t, err, &miss)
	assert.Equal(t, "ambiguous", miss.Kind, "git calling the prefix ambiguous is a refusal even when one commit of the branch has it")

	_, err = resolveStepCommit(context.Background(), "checkout", branch, strings.Repeat("e", 40), run)
	require.ErrorAs(t, err, &miss)
	assert.Equal(t, "unknown", miss.Kind)
	assert.Contains(t, err.Error(), "is on no commit of "+branch)
	assert.NotContains(t, err.Error(), "is no sha")

	missing := func(context.Context, string, ...string) (string, string, error) {
		return "", "fatal: ambiguous argument 'work/c1': unknown revision or path not in the working tree.\n", errors.New("exit status 128")
	}
	_, err = resolveStepCommit(context.Background(), "checkout", branch, only[:9], missing)
	require.ErrorAs(t, err, &miss)
	assert.Equal(t, "unknown", miss.Kind, "a missing ref's 'ambiguous argument' is not an ambiguous prefix")

	called := 0
	boom := func(context.Context, string, ...string) (string, string, error) {
		called++
		return "", "", errors.New("exec: \"git\" not found")
	}
	_, err = resolveStepCommit(context.Background(), "checkout", branch, only[:9], boom)
	require.Error(t, err)
	var op *cardtree.CommitMiss
	assert.False(t, errors.As(err, &op), "an operational git failure is not a missing commit")
	assert.Contains(t, err.Error(), "could not be resolved on "+branch)
	assert.Equal(t, 1, called)

	called = 0
	_, err = resolveStepCommit(context.Background(), "checkout", "../x", only[:9], boom)
	require.ErrorAs(t, err, &miss)
	assert.Equal(t, "unknown", miss.Kind)
	assert.Equal(t, 0, called, "a branch that is not a ref name is not handed to git")
}

func gitSub(args []string) string {
	for i := 0; i < len(args); i++ {
		if args[i] == "-c" {
			i++
			continue
		}
		if strings.HasPrefix(args[i], "-") {
			continue
		}
		return args[i]
	}
	return ""
}

func TestCloneDirIsTheLaunchCheckout(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	slots := filepath.Join(root, "slots")
	p := pk("c1")
	p.Gen, p.Epoch = 1, 7
	dir := filepath.Join(slots, "c1.g1.e7", "jobs", "c1", "repo")
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".git"), 0o755))
	assert.Equal(t, dir, cloneDir(p, []string{"nova-worker", "member", "--root", root}))
	assert.Equal(t, dir, cloneDir(p, []string{"nova-worker", "member", "--slots", slots}))
	assert.Empty(t, cloneDir(p, []string{"go", "test", "--slots", slots}), "a test process is not the member verb")
	p.Kind = "read"
	p.Attempt = 2
	assert.Empty(t, cloneDir(p, []string{"nova-worker", "member", "--slots", slots}), "a read's launch name is its attempt, not the work generation")
}
