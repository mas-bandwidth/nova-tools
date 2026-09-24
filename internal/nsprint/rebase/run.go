package rebase

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Run executes the card script in gitDir and records the outcome on the
// book. gitDir is the checkout (its origin is the remote the script
// fetches and pushes). work is a directory outside that checkout where
// the script file and the range-diff are written. A clean rebase pushes
// and carries reads only when the script says the range-diff is empty. A
// conflict records one fix task and does not add a card.
func Run(b *Book, card Card, gitDir, work string) error {
	if b == nil {
		return fmt.Errorf("rebase: nil book")
	}
	if err := cardReady(card); err != nil {
		return err
	}
	if gitDir == "" || work == "" {
		return fmt.Errorf("rebase: checkout and work directory are required")
	}
	path, err := writeScript(work, card)
	if err != nil {
		return err
	}
	stdout, stderr, code, err := execScript(path, gitDir, work)
	if err != nil {
		return err
	}
	out, err := parseOutcome(stdout)
	if err != nil {
		return fmt.Errorf("rebase %s: %w\n%s", card.Key, err, stderr)
	}
	switch code {
	case 0:
		if out.conflict {
			return fmt.Errorf("rebase %s: clean exit printed a conflict\n%s", card.Key, stderr)
		}
		if !out.sawReads || !headRx.MatchString(out.newHead) {
			return fmt.Errorf("rebase %s: clean exit did not push a head\n%s", card.Key, stdout)
		}
		b.heads[card.Number] = out.newHead
		if out.carry {
			b.carry(card.Number, card.Head, out.newHead)
		}
		return nil
	case 3:
		if !out.conflict || out.sawReads || out.newHead != "" {
			return fmt.Errorf("rebase %s: conflict exit printed %q", card.Key, stdout)
		}
		if out.fixKey != ConflictKey(card.Number, card.Head) || out.author != card.Author || out.noCard != card.Key {
			return fmt.Errorf("rebase %s: conflict lines %q are not this card", card.Key, stdout)
		}
		b.addFix(FixTask{
			Key:    out.fixKey,
			Author: card.Author,
			Repo:   card.Repo,
			Number: card.Number,
			Head:   card.Head,
		})
		return nil
	default:
		return fmt.Errorf("rebase %s: exit %d\n%s", card.Key, code, stderr)
	}
}

// Classify runs the card script's range-diff rule on a file git range-diff
// already wrote. It is the same function the rebase path calls. true means
// the reads carry.
func Classify(card Card, work, rangeDiffPath string) (bool, error) {
	if err := cardReady(card); err != nil {
		return false, err
	}
	if work == "" || rangeDiffPath == "" {
		return false, fmt.Errorf("rebase: work directory and range-diff file are required")
	}
	path, err := writeScript(work, card)
	if err != nil {
		return false, err
	}
	stdout, stderr, code, err := execScript(path, "--classify", rangeDiffPath)
	if err != nil {
		return false, err
	}
	if code != 0 {
		return false, fmt.Errorf("rebase classify %s: exit %d\n%s", card.Key, code, stderr)
	}
	switch strings.TrimSpace(stdout) {
	case "READS carry":
		return true, nil
	case "READS drop":
		return false, nil
	default:
		return false, fmt.Errorf("rebase classify %s: %q", card.Key, stdout)
	}
}

func cardReady(card Card) error {
	if card.Kind != KindScript || card.ModelCalls != 0 {
		return fmt.Errorf("rebase: card %s is not a script with zero model calls", card.Key)
	}
	if !strings.HasPrefix(card.Script, "#!/bin/sh\n") {
		return fmt.Errorf("rebase: card %s has no script", card.Key)
	}
	return nil
}

func writeScript(work string, card Card) (string, error) {
	if err := os.MkdirAll(work, 0o755); err != nil {
		return "", fmt.Errorf("rebase: work dir: %w", err)
	}
	path := filepath.Join(work, "card.sh")
	if err := os.WriteFile(path, []byte(card.Script), 0o755); err != nil {
		return "", fmt.Errorf("rebase: write card: %w", err)
	}
	return path, nil
}

// execScript runs the card through /bin/sh rather than exec'ing the file it
// just wrote. On Linux, exec of a file another goroutine's fork may still hold
// open for writing fails with ETXTBSY (golang/go#22315): dev run 36015701004,
// test (4/8 space) on vision-nova-20, "fork/exec .../card.sh: text file busy"
// under parallel subtests. sh opens the script for reading, which that race
// cannot refuse; cardReady already requires the #!/bin/sh line, so the
// interpreter is the one the card names.
func execScript(path string, args ...string) (stdout, stderr string, code int, err error) {
	cmd := exec.Command("/bin/sh", append([]string{path}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_EDITOR=true")
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	err = cmd.Run()
	stdout, stderr = out.String(), errb.String()
	if err == nil {
		return stdout, stderr, 0, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return stdout, stderr, exitErr.ExitCode(), nil
	}
	return stdout, stderr, -1, fmt.Errorf("rebase: run card: %w\n%s", err, stderr)
}

type outcome struct {
	conflict bool
	carry    bool
	sawReads bool
	newHead  string
	fixKey   string
	author   string
	noCard   string
}

func parseOutcome(stdout string) (outcome, error) {
	var out outcome
	for _, line := range strings.Split(stdout, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		switch {
		case line == "READS carry":
			out.sawReads = true
			out.carry = true
		case line == "READS drop":
			out.sawReads = true
			out.carry = false
		case strings.HasPrefix(line, "PUSH "):
			out.newHead = strings.TrimPrefix(line, "PUSH ")
		case strings.HasPrefix(line, "FIX "):
			out.conflict = true
			out.fixKey = strings.TrimPrefix(line, "FIX ")
		case strings.HasPrefix(line, "AUTHOR "):
			out.author = strings.TrimPrefix(line, "AUTHOR ")
		case strings.HasPrefix(line, "NOCARD "):
			out.noCard = strings.TrimPrefix(line, "NOCARD ")
		default:
			return outcome{}, fmt.Errorf("unexpected line %q", line)
		}
	}
	return out, nil
}
