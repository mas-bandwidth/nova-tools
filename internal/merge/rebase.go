package merge

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// Work list 5, once more, for the rebase cut: the open pull request list is read from the
// host and every field in it is DATA. A head branch decides a git argument; it is checked
// where it arrives, and the card the cut writes carries no instruction the worker did not
// already have.

// RebasePR is one row of the open pull request list the rebase cutter reads. MergeState
// is the host's own word (DIRTY, CLEAN, BLOCKED, ...), never a verdict this tool made.
type RebasePR struct {
	Number     int
	HeadRef    string
	Title      string
	MergeState string
}

// RebaseList is the gh interface the rebase verb reads through: the tests inject a fake,
// the host is the only implementation that shells to gh. It is separate from Host because
// the merge pass reads one pull request at a time and this verb reads the whole open list.
type RebaseList interface {
	OpenPRs() ([]RebasePR, error)
}

// RebaseCard is what the injected cut function is handed: the pull request's own fields.
// The function decides the card's shape; this type deliberately does not.
type RebaseCard struct {
	PR      int
	HeadRef string
	Title   string
}

// RebaseCut writes one card and returns the path it wrote. It is injected so the rebase
// pass can be driven without the pulse cutter, and so the one template stays in the cut
// machinery rather than being copied into a second printf.
type RebaseCut func(card RebaseCard) (string, error)

// Launcher starts one card on a bench. The production one shells the deployment's bench
// launcher; the tests inject a fake that records what it was handed.
type Launcher interface {
	Launch(card string) error
}

// RebaseInput is the whole rebase pass, apart from flag parsing, so a test drives it with
// a fake list, a fake cut and a fake launcher and reaches no network.
type RebaseInput struct {
	Markers string // one empty file per PR number, named pr-<n>
	Out     string // the directory the cut writes cards into
	List    RebaseList
	Cut     RebaseCut
	Launch  Launcher
	Stdout  io.Writer
	Stderr  io.Writer
}

// RebaseWanted is the selection the hand loop made: an open pull request whose branch is
// rowan/<something> and whose head the host calls DIRTY. The replays branches get their own
// verb elsewhere and are excluded on purpose. It is a plain filter: a decision model has no
// part in a question this tool can answer from two fields.
func RebaseWanted(pr RebasePR) bool {
	if !strings.EqualFold(strings.TrimSpace(pr.MergeState), "DIRTY") {
		return false
	}
	if !strings.HasPrefix(pr.HeadRef, "rowan/") {
		return false
	}
	if strings.HasPrefix(pr.HeadRef, "rowan/replays-") {
		return false
	}
	return true
}

// Rebase runs one pass: list, keep the DIRTY rowan/* pull requests with no marker, cut one
// card and write one marker for each, launch each, and print one line. Exit 2 is a pass
// that could not run at all; 0 is a pass that ran, whether it cut nothing or many.
func Rebase(in RebaseInput) int {
	if in.List == nil || in.Cut == nil {
		fmt.Fprintf(in.Stderr, "REBASE REFUSED: this pass was given no pull request list or no cutter; wire both before running it\n")
		return 2
	}
	prs, err := in.List.OpenPRs()
	if err != nil {
		fmt.Fprintf(in.Stderr, "REBASE REFUSED: the open pull request list could not be read: %s; check the token gh uses and run the same verb again\n", oneline.Err(err))
		return 2
	}
	cards := 0
	for _, pr := range prs {
		if !RebaseWanted(pr) {
			continue
		}
		marker := filepath.Join(in.Markers, fmt.Sprintf("pr-%d", pr.Number))
		if _, err := os.Stat(marker); err == nil {
			continue
		}
		card, err := in.Cut(RebaseCard{PR: pr.Number, HeadRef: pr.HeadRef, Title: pr.Title})
		if err != nil {
			fmt.Fprintf(in.Stderr, "REBASE REFUSED: PR #%d: %s; fix the cut and run the same verb again\n", pr.Number, oneline.Err(err))
			return 2
		}
		if err := os.MkdirAll(in.Markers, 0o755); err != nil {
			fmt.Fprintf(in.Stderr, "REBASE REFUSED: --markers %s: %s; pass a directory this verb may create\n", oneline.Field(in.Markers), oneline.Err(err))
			return 2
		}
		if err := os.WriteFile(marker, nil, 0o644); err != nil {
			fmt.Fprintf(in.Stderr, "REBASE REFUSED: the marker %s could not be written: %s; pass a writable --markers directory\n", oneline.Field(marker), oneline.Err(err))
			return 2
		}
		if in.Launch != nil {
			if err := in.Launch.Launch(card); err != nil {
				fmt.Fprintf(in.Stderr, "REBASE NOTE PR #%d card=%s is cut and marked and was not launched: %s; launch it by hand, the marker stops a second cut\n",
					pr.Number, oneline.Field(card), oneline.Err(err))
			}
		}
		cards++
	}
	fmt.Fprintf(in.Stdout, "REBASE tick cards=%d\n", cards)
	return 0
}

// BenchLauncher is the production Launcher: it starts one card on a bench through the
// deployment's bench launcher, the same argv the hand loop used. It is a value so a bench
// can be named without a flag, and every field has the hand loop's own default.
type BenchLauncher struct {
	Command  string // the executable; empty is flash-native-bench.sh
	Bench    string // empty is space
	Runner   string // empty is swarm-space
	Deadline string // whole seconds; empty is 900
	Timeout  time.Duration
	Run      Runner
}

func (b BenchLauncher) command() string {
	if b.Command != "" {
		return b.Command
	}
	return "flash-native-bench.sh"
}

func (b BenchLauncher) bench() string {
	if b.Bench != "" {
		return b.Bench
	}
	return "space"
}

func (b BenchLauncher) runner() string {
	if b.Runner != "" {
		return b.Runner
	}
	return "swarm-space"
}

func (b BenchLauncher) deadline() string {
	if b.Deadline != "" {
		return b.Deadline
	}
	return "900"
}

func (b BenchLauncher) timeout() time.Duration {
	if b.Timeout > 0 {
		return b.Timeout
	}
	return 15 * time.Minute
}

// Launch starts one card and returns the launcher's own refusal, so a pass can note it.
func (b BenchLauncher) Launch(card string) error {
	name := strings.TrimSuffix(filepath.Base(card), filepath.Ext(card))
	args := []string{b.bench(), b.runner(), card, name, b.deadline()}
	if err := guard(args, ""); err != nil {
		return err
	}
	runner := b.Run
	if runner == nil {
		runner = Exec{}
	}
	ctx, cancel := contextWithTimeout(b.timeout())
	defer cancel()
	if _, err := runner.Run(ctx, "", b.command(), args...); err != nil {
		return fmt.Errorf("%s %s: %w", b.command(), strings.Join(args, " "), err)
	}
	return nil
}
