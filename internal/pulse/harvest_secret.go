package pulse

// THE HARVEST NEVER PUSHES A KEY (nova-tools #1814).
//
// A harvest does two things with a worker's own bytes: it pushes the card's commits, and
// it copies RESULT.md verbatim into the body of the draft PR it opens. Both end on a
// forge. A card's model that got hold of the seat's provider key -- which it could, until
// cmd/nova-swarm's shell shim took the key out of the shell the harness hands it -- had in
// those two paths a way to publish it. This file is the backstop behind the shim: before
// any push and before any PR, the RESULT.md and the diff the push would carry are read for
// the SHAPE of a key (internal/keyshape) and for the value of any secret-named variable
// this process itself holds.
//
// A HIT REFUSES AND QUARANTINES, and prints nothing of what it matched. The job directory
// is MOVED to <root>/quarantine/<label> -- never deleted, because a key in a worker's
// output is evidence a person has to read -- one HUMAN line is appended to <root>/HUMAN,
// and the card is counted refused, so the harvest's own exit code is already 1.
//
// THE PR BODY IS NOT SCANNED SEPARATELY, on purpose: openPR's body is
// strings.Join(resultLines, "\n") truncated to MaxBodyBytes, so it is a PREFIX of the text
// this scan reads in full. TestThePRBodyIsAPrefixOfWhatTheScanRead pins that, and a change
// to openPR that makes the body something else breaks it rather than opening a hole.

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/keyshape"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// secretScan reads the two texts a harvest is about to publish and returns every finding.
// The environment it compares against is this process's own: `nova-secrets exec` sets the
// seat's key around a pulse, so the one key most worth catching is one this process can
// recognise without holding a list of it.
func secretScan(jobDir string, resultLines []string) ([]keyshape.Finding, error) {
	env := os.Environ()
	var out []keyshape.Finding
	body := strings.Join(resultLines, "\n")
	found, err := keyshape.ScanText(filepath.Join(jobDir, "RESULT.md"), body, env)
	if err != nil {
		return nil, err
	}
	out = append(out, found...)
	diff, path := harvestDiff(jobDir)
	if diff != "" {
		found, err = keyshape.ScanText(path, diff, env)
		if err != nil {
			return nil, err
		}
		out = append(out, found...)
	}
	return out, nil
}

// harvestDiff is the patch the push would carry: the first base ref that resolves, the
// same order commitsPastBase asks in. An unreadable clone is an empty diff, and the
// RESULT.md scan still runs: a check that cannot see the diff says nothing about it rather
// than passing it.
func harvestDiff(dir string) (diff, path string) {
	ctx, cancel := context.WithTimeout(context.Background(), childTimeout)
	defer cancel()
	for _, ref := range []string{"origin/dev", "dev", "origin/main", "main"} {
		cmd := exec.CommandContext(ctx, "git", "diff", ref+"...HEAD")
		cmd.Dir = dir
		out, err := cmd.Output()
		if err != nil {
			continue
		}
		if strings.TrimSpace(string(out)) == "" {
			continue
		}
		return string(out), "diff:" + ref + "...HEAD"
	}
	return "", ""
}

// refuseSecret prints one refusal line per finding, quarantines the job and writes the
// HUMAN line. It returns nothing a caller can mistake for success: the caller counts the
// card refused and moves to the next one.
func refuseSecret(in HarvestInput, root, label, jobDir string, findings []keyshape.Finding) {
	for _, f := range findings {
		fmt.Fprintf(in.Stderr, "HARVEST REFUSED secret-shape %s label=%s\n", f.String(), field(label))
	}
	dest, moveErr := quarantineJob(root, label, jobDir)
	if moveErr != nil {
		fmt.Fprintf(in.Stderr, "HARVEST NOTE the quarantine of %s failed, so the job is left where it is and still not pushed: %s\n",
			field(jobDir), oneline.Err(moveErr))
		dest = jobDir
	}
	appendSecretHuman(root, label, dest, findings)
}

// quarantineJob moves the job out of the harvest's way, into <root>/quarantine/<label>.
// It never deletes and never overwrites: a second hit on the same label lands beside the
// first with a numbered suffix.
func quarantineJob(root, label, jobDir string) (string, error) {
	dir := filepath.Join(root, "quarantine")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	dest := filepath.Join(dir, label)
	for n := 1; ; n++ {
		if _, err := os.Stat(dest); os.IsNotExist(err) {
			break
		}
		dest = filepath.Join(dir, fmt.Sprintf("%s.%d", label, n))
		if n > 100 {
			return "", fmt.Errorf("a hundred quarantines already carry the label %s", label)
		}
	}
	if err := os.Rename(jobDir, dest); err != nil {
		return "", err
	}
	return dest, nil
}

// appendSecretHuman writes the one line a person reads: what shape, where the job went,
// and the only remedy that matters. No matched text, ever.
func appendSecretHuman(root, label, dest string, findings []keyshape.Finding) {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(root, "HUMAN"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	shapes := map[string]bool{}
	var names []string
	for _, fd := range findings {
		if !shapes[fd.Shape] {
			shapes[fd.Shape] = true
			names = append(names, fd.Shape)
		}
	}
	fmt.Fprintf(f, "HUMAN task=secret reason=secret-shape card=%s shapes=%s hits=%d quarantine=%s remedy=%s\n",
		field(label), field(strings.Join(names, ",")), len(findings), field(dest),
		"a key reached a worker: rotate the seat's key, then read the quarantined job")
}
