package pulse

// The forge side of `wait --until pr-check`: one `gh pr view` per poll, folded to the two
// words the condition asks about. It is the production half of the WaitInput.PR seam and is
// never called from a test.

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// ghPRCheckTimeout bounds one forge call. A gh that hangs must not eat the whole wait.
const ghPRCheckTimeout = 60 * time.Second

// ghWaitView is the slice of `gh pr view --json` this condition reads.
type ghWaitView struct {
	State             string `json:"state"`
	StatusCheckRollup []struct {
		Status     string `json:"status"`
		Conclusion string `json:"conclusion"`
		State      string `json:"state"`
	} `json:"statusCheckRollup"`
}

// GHPRState asks the forge about one pull request.
func GHPRState(repo string, number int) (PRState, error) {
	ctx, cancel := context.WithTimeout(context.Background(), ghPRCheckTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "gh", "pr", "view", strconv.Itoa(number), "-R", repo,
		"--json", "state,statusCheckRollup").Output()
	if err != nil {
		return PRState{}, fmt.Errorf("gh pr view %d -R %s: %w", number, repo, err)
	}
	var v ghWaitView
	if err := json.Unmarshal(out, &v); err != nil {
		return PRState{}, fmt.Errorf("gh pr view %d -R %s did not answer JSON: %w", number, repo, err)
	}
	return PRState{State: strings.ToUpper(strings.TrimSpace(v.State)), Checks: RollupState(checkWords(v))}, nil
}

// checkWords flattens the rollup to one word per check: a check run's conclusion while it
// has one and its status while it does not, and a commit status's state.
func checkWords(v ghWaitView) []string {
	var words []string
	for _, c := range v.StatusCheckRollup {
		switch {
		case strings.TrimSpace(c.Conclusion) != "":
			words = append(words, c.Conclusion)
		case strings.TrimSpace(c.Status) != "":
			words = append(words, c.Status)
		case strings.TrimSpace(c.State) != "":
			words = append(words, c.State)
		}
	}
	return words
}

// RollupState folds a head's check words into one of green, red, pending or none.
//
// RED WINS OVER PENDING, and pending wins over green. A head with one failure and six still
// running is red now: waiting for the rest to finish before saying so is how a waiter sits
// on a dead PR for half an hour. A head with nothing on it is `none`, never green -- "no
// evidence is not negative evidence" (2026-09-21), and a wait for green on a head no CI ever
// touched must not return the moment it is asked.
func RollupState(words []string) string {
	if len(words) == 0 {
		return "none"
	}
	pending := false
	for _, w := range words {
		switch strings.ToUpper(strings.TrimSpace(w)) {
		case "SUCCESS", "NEUTRAL", "SKIPPED":
		case "FAILURE", "ERROR", "CANCELLED", "TIMED_OUT", "STARTUP_FAILURE", "ACTION_REQUIRED", "STALE":
			return "red"
		default:
			// QUEUED, IN_PROGRESS, PENDING, WAITING, REQUESTED, EXPECTED and anything
			// this tool has not seen: still moving, so not yet an answer.
			pending = true
		}
	}
	if pending {
		return "pending"
	}
	return "green"
}
