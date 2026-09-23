package pulse

import (
	"fmt"
	"os"
	"strings"
)

// UnstickOptions is the world around one typed stale-base refusal. RemoteSHA,
// PRCount and PushDelete are the orphan half (a branch on the remote with no
// pull request in any state). Leave them nil to drop only the local
// refs/harvest bookkeeping ref. Nothing here reads a log line.
type UnstickOptions struct {
	Clones     []string
	SeenPath   string
	Runner     GitRunner
	Repos      []string
	RemoteSHA  func(repo, branch string) (string, error)
	PRCount    func(repo, branch string) (int, error)
	PushDelete func(repo, branch string) error
}

// UnstickStaleBase is the remedy for one stale-base decision (#2648). It reads
// the refusal's Branch and Label fields. It does not read Error's sentence: that
// sentence is for a person, and matching it is how the pre-#2598 sed went blind
// when the sentence gained declared=.
func UnstickStaleBase(r *StaleBaseRefusal, opt UnstickOptions) ([]string, error) {
	if r == nil || r.Missing || strings.TrimSpace(r.Branch) == "" {
		return nil, nil
	}
	branch := strings.TrimSpace(r.Branch)
	label := strings.TrimSpace(r.Label)
	if label == "" {
		label = branch
	}
	key := label + "\t" + branch
	if opt.SeenPath != "" && seenExact(opt.SeenPath, key) {
		return nil, nil
	}
	runner := opt.Runner
	if runner == nil {
		runner = defaultGitRunner{}
	}
	var lines []string
	for _, clone := range opt.Clones {
		if strings.TrimSpace(clone) == "" {
			continue
		}
		ref := "refs/harvest/" + branch
		if _, err := runner.Run(clone, "rev-parse", "-q", "--verify", ref); err != nil {
			continue
		}
		if _, err := runner.Run(clone, "update-ref", "-d", ref); err != nil {
			return lines, fmt.Errorf("unstick %s: %w", ref, err)
		}
		lines = append(lines, fmt.Sprintf("UNSTICK-CACHE %s %s (dropped refs/harvest/%s)", label, branch, branch))
	}
	if opt.RemoteSHA != nil && opt.PRCount != nil && opt.PushDelete != nil {
		for _, repo := range opt.Repos {
			sha, err := opt.RemoteSHA(repo, branch)
			if err != nil || strings.TrimSpace(sha) == "" {
				continue
			}
			n, err := opt.PRCount(repo, branch)
			if err != nil || n != 0 {
				continue
			}
			if err := opt.PushDelete(repo, branch); err != nil {
				return lines, err
			}
			lines = append(lines, fmt.Sprintf("UNSTICK-ORPHAN %s %s:%s deleted", label, repo, branch))
		}
	}
	if opt.SeenPath != "" {
		if err := seenAdd(opt.SeenPath, key); err != nil {
			return lines, err
		}
	}
	return lines, nil
}

// remedyStaleBase runs the unstick remedy from the typed refusal in hand.
// A sentence printed to the log is not read back.
func remedyStaleBase(err error, label string, clones []string) {
	sb, ok := err.(*StaleBaseRefusal)
	if !ok || sb == nil {
		return
	}
	if sb.Label == "" {
		sb.Label = label
	}
	_, _ = UnstickStaleBase(sb, UnstickOptions{Clones: clones})
}

func seenExact(path, key string) bool {
	raw, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	for _, l := range strings.Split(string(raw), "\n") {
		if l == key {
			return true
		}
	}
	return false
}

func seenAdd(path, key string) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = fmt.Fprintln(f, key)
	return err
}
