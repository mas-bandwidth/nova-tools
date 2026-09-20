package merge

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

const ancestryGitTimeout = 30 * time.Second

// OrderByAncestry performs a topological sort on branches in gitDir based on git commit ancestry.
// Base PRs appear before stacked PRs that branch off them. Independent branches that have no
// ancestry relationship maintain a deterministic order based on their original relative input order.
func OrderByAncestry(gitDir string, branches []string) ([]string, error) {
	g := NewGit(gitDir, ancestryGitTimeout, nil)
	return OrderByAncestryGit(g, branches)
}

// OrderByAncestryGit performs a topological sort on branches using the provided Git runner.
func OrderByAncestryGit(g *Git, branches []string) ([]string, error) {
	if len(branches) == 0 {
		return []string{}, nil
	}

	// Preserve input order while deduplicating branch names.
	unique := make([]string, 0, len(branches))
	seen := make(map[string]bool, len(branches))
	for _, b := range branches {
		if b == "" || seen[b] {
			continue
		}
		seen[b] = true
		unique = append(unique, b)
	}

	n := len(unique)
	if n == 0 {
		return []string{}, nil
	}
	// Resolve commit SHAs up front for all unique branches. This validates ref existence
	// and allows avoiding git subprocess calls for identical commits and asymmetric ancestry.
	commits := make([]string, n)
	for i, b := range unique {
		sha, err := g.Out("rev-parse", "--verify", "-q", b+"^{commit}")
		if err != nil {
			return nil, fmt.Errorf("resolve branch %s: %w", b, err)
		}
		commits[i] = strings.TrimSpace(sha)
	}

	if n == 1 {
		return append([]string(nil), unique...), nil
	}

	// adj[i] lists the indices of branches that unique[i] must precede (i.e. unique[i] is an ancestor of unique[j]).
	adj := make([][]int, n)
	inDegree := make([]int, n)

	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			if commits[i] == commits[j] {
				// Both are ancestors of each other: identical commit.
				// Neither strictly precedes the other by ancestry; preserve input order.
				continue
			}

			iAnc, err := isAncestor(g, commits[i], commits[j])
			if err != nil {
				return nil, err
			}
			if iAnc {
				// unique[i] is an ancestor of unique[j]: unique[i] must precede unique[j].
				adj[i] = append(adj[i], j)
				inDegree[j]++
				continue
			}

			jAnc, err := isAncestor(g, commits[j], commits[i])
			if err != nil {
				return nil, err
			}
			if jAnc {
				// unique[j] is an ancestor of unique[i]: unique[j] must precede unique[i].
				adj[j] = append(adj[j], i)
				inDegree[i]++
			}
		}
	}

	// Kahn's algorithm with tie-breaking by original input index to ensure determinism.
	emitted := make([]bool, n)
	result := make([]string, 0, n)

	for len(result) < n {
		next := -1
		for i := 0; i < n; i++ {
			if !emitted[i] && inDegree[i] == 0 {
				next = i
				break
			}
		}
		if next == -1 {
			return nil, fmt.Errorf("ancestry cycle detected among branches: %v", unique)
		}

		emitted[next] = true
		result = append(result, unique[next])

		for _, succ := range adj[next] {
			inDegree[succ]--
		}
	}

	return result, nil
}

// isAncestor reports whether branch/commit a is an ancestor of branch/commit b using
// git merge-base --is-ancestor a b.
func isAncestor(g *Git, a, b string) (bool, error) {
	if a == b {
		return false, nil
	}
	_, err := g.Run("merge-base", "--is-ancestor", a, b)
	if err == nil {
		return true, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return false, nil
	}
	return false, err
}
