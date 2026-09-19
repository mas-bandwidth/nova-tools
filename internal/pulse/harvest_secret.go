package pulse

// NO PATH PUBLISHES A KEY (nova-tools #1814; Johnny's HOLD of #1838 at 3ad5e886).
//
// A harvest publishes two of a worker's own byte streams: the card's commits, and its
// RESULT.md, which every PR body is built from. Both end on a forge. A card's model that
// got hold of the seat's provider key -- which it could, until cmd/nova-swarm's shell shim
// took the key out of the shell the harness hands it, and which it still can through
// `/proc/<harness-pid>/environ` (measured on space; see docs/SPEC-SWARM.md) -- has in those
// two streams a way to publish it.
//
// THE FIRST CUT OF THIS FILE GUARDED ONE PATH AND THERE ARE FOUR. Johnny's read named the
// three that were missing: `harvest --bench` (which is how every Space card is harvested),
// `HarvestWorking`, and `manager.openPR`. So the guard is ONE function, secretFindings,
// and ONE refusal, secretRefusal.refuse, called by every one of them -- and
// TestEveryPublishSiteIsBehindTheSecretScan walks this package's syntax tree, finds every
// `git push`, `gh pr create|edit` and `Forge.CreatePR` call there is, and fails if one of
// them is not behind the guard. A fifth site cannot appear without it.
//
// A HIT REFUSES AND QUARANTINES, and prints nothing of what it matched. The job directory
// is MOVED beside its `jobs/` -- never deleted, because a key in a worker's output is
// evidence a person has to read -- one HUMAN line is written, and the card is counted
// refused. A bench job is moved over the same shell seam the harvest already reaches the
// bench through, so no test opens a connection.
//
// THE PR BODY IS NOT SCANNED SEPARATELY, on purpose: every body this package builds is the
// scanned RESULT.md text, truncated (openPR, benchPRBody, manager.openPR), so it is a
// PREFIX of what the guard read in full. TestThePRBodyIsAPrefixOfWhatTheScanRead pins that.

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/keyshape"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// secretFindings is THE guard. resultLines is the card's own RESULT.md, which every PR
// body this package builds is a prefix of; diffDir and diffSpec name the patch the push
// would carry, and an empty diffSpec asks the caller's clone for the first base ref that
// resolves. A diff that cannot be read is no finding and no error: the RESULT.md half
// still runs, and a check that could not see the diff says nothing about it rather than
// passing it.
//
// The environment it compares values against is this process's own: `nova-secrets exec`
// sets the seat's key around a pulse, so the one key most worth catching is one this
// process can recognise without holding a list of it. It is compared, never printed.
func secretFindings(jobDir, diffDir, diffSpec string, resultLines []string) ([]keyshape.Finding, error) {
	env := os.Environ()
	out, err := keyshape.ScanText(filepath.Join(jobDir, "RESULT.md"), strings.Join(resultLines, "\n"), env)
	if err != nil {
		return nil, err
	}
	if diffDir == "" {
		return out, nil
	}
	diff, path := harvestDiff(diffDir, diffSpec)
	if diff == "" {
		return out, nil
	}
	found, err := keyshape.ScanText(path, diff, env)
	if err != nil {
		return nil, err
	}
	return append(out, found...), nil
}

// secretScan is the shape the local Harvest asks in: the job directory is its own clone.
func secretScan(jobDir string, resultLines []string) ([]keyshape.Finding, error) {
	return secretFindings(jobDir, jobDir, "", resultLines)
}

// harvestDiff is the patch the push would carry. A named spec is asked for as given; an
// empty one tries the base refs commitsPastBase asks in, in the same order.
func harvestDiff(dir, spec string) (diff, path string) {
	ctx, cancel := context.WithTimeout(context.Background(), childTimeout)
	defer cancel()
	specs := []string{spec}
	if strings.TrimSpace(spec) == "" {
		specs = []string{"origin/dev...HEAD", "dev...HEAD", "origin/main...HEAD", "main...HEAD"}
	}
	for _, s := range specs {
		cmd := exec.CommandContext(ctx, "git", "diff", s)
		cmd.Dir = dir
		out, err := cmd.Output()
		if err != nil {
			continue
		}
		if strings.TrimSpace(string(out)) == "" {
			continue
		}
		return string(out), "diff:" + s
	}
	return "", ""
}

// secretRefusal is what one hit does, held apart from the four callers so all four do the
// same thing. QuarantineRoot is the directory `quarantine/` is made under -- on the BENCH
// for `--bench`, and locally otherwise -- and an empty one quarantines the job beside its
// own `jobs/` directory. HumanDir is the LOCAL directory the `HUMAN` file is appended to;
// an empty one writes no file, and the HUMAN line is printed either way, because a person
// reading the run's output must see it whether or not a file could be written. Move is how
// the job directory travels: nil is os.Rename, and a bench passes a mover over its shell.
type secretRefusal struct {
	Site           string
	Label          string
	JobDir         string
	QuarantineRoot string
	HumanDir       string
	Out            io.Writer
	Move           func(src, dst string) error
}

// refuse prints one line per finding, quarantines the job and writes the HUMAN line. It
// returns nothing a caller can mistake for success: the caller counts the card refused and
// moves to the next one.
func (r secretRefusal) refuse(findings []keyshape.Finding) {
	for _, f := range findings {
		fmt.Fprintf(r.Out, "HARVEST REFUSED secret-shape %s label=%s site=%s\n",
			f.String(), field(r.Label), field(r.Site))
	}
	root := r.QuarantineRoot
	if root == "" {
		root = filepath.Dir(filepath.Dir(r.JobDir))
	}
	dest, moveErr := quarantineJob(root, r.Label, r.JobDir, r.Move)
	if moveErr != nil {
		fmt.Fprintf(r.Out, "HARVEST NOTE the quarantine of %s failed, so the job is left where it is and still not pushed: %s\n",
			field(r.JobDir), oneline.Err(moveErr))
		dest = r.JobDir
	}
	line := secretHumanLine(r.Label, dest, findings)
	fmt.Fprint(r.Out, line)
	appendSecretHuman(r.HumanDir, line)
}

// quarantineJob moves the job out of the harvest's way, into <root>/quarantine/<label>. It
// never deletes and never overwrites: a second hit on the same label lands beside the first
// with a numbered suffix. A nil move is os.Rename; a bench's is a remote one.
func quarantineJob(root, label, jobDir string, move func(src, dst string) error) (string, error) {
	dir := filepath.Join(root, "quarantine")
	dest := filepath.Join(dir, label)
	if move == nil {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", err
		}
		for n := 1; ; n++ {
			if _, err := os.Stat(dest); os.IsNotExist(err) {
				break
			}
			if n > 100 {
				return "", fmt.Errorf("a hundred quarantines already carry the label %s", oneline.Field(label))
			}
			dest = filepath.Join(dir, fmt.Sprintf("%s.%d", label, n))
		}
	}
	if move == nil {
		move = os.Rename
	}
	if err := move(jobDir, dest); err != nil {
		return "", err
	}
	return dest, nil
}

// benchMover quarantines a job that lives on a bench, over the shell seam the harvest
// already reaches that bench through. It takes a RUNNER already bound to its bench, never
// a bench name: the name is resolved once, at harvestBench's edge, and nothing below that
// guard names a machine again (internal/ci's bench-name rule). It never deletes: mkdir,
// a refusal to overwrite, `mv`, and nothing else.
func benchMover(run func(script string) (string, error)) func(src, dst string) error {
	return func(src, dst string) error {
		script := "mkdir -p " + shellQuote(filepath.Dir(dst)) +
			" && test ! -e " + shellQuote(dst) +
			" && mv " + shellQuote(src) + " " + shellQuote(dst)
		out, err := run(script)
		if err != nil {
			return fmt.Errorf("%s: %s", oneline.Err(err), oneline.Cap(out, 160))
		}
		return nil
	}
}

// secretHumanLine is the one line a person reads: what shape, where the job went, and the
// only remedy that matters. No matched text, ever.
func secretHumanLine(label, dest string, findings []keyshape.Finding) string {
	seen := map[string]bool{}
	var names []string
	for _, fd := range findings {
		if !seen[fd.Shape] {
			seen[fd.Shape] = true
			names = append(names, fd.Shape)
		}
	}
	return fmt.Sprintf("HUMAN task=secret reason=secret-shape card=%s shapes=%s hits=%d quarantine=%s remedy=%s\n",
		field(label), field(strings.Join(names, ",")), len(findings), field(dest),
		"a key reached a worker: rotate the seat's key, then read the quarantined job")
}

// appendSecretHuman adds the line to <dir>/HUMAN when a local directory was named.
func appendSecretHuman(dir, line string) {
	if strings.TrimSpace(dir) == "" {
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(dir, "HUMAN"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprint(f, line)
}
