package cardcontract

import (
	"fmt"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/cardtree"
	"github.com/mas-bandwidth/nova-tools/internal/typedrec"
)

// CompleteResult derives commit metadata from Git and validates the existing result
// before publication (docs/SPEC-CARD-CONTRACT.md section 3). The resolver verifies
// membership in HEAD's history; artifact verifies a referenced output exists. Neither
// the verdict nor any rated-source revision in the evidence is inferred or changed.
func CompleteResult(raw []byte, head, branch string, resolve func(string) (string, error), artifact func(string) error) ([]byte, error) {
	r := typedrec.ParseCardResult(raw)
	if !typedrec.IsFullSha(head) || branch == "" {
		return nil, fmt.Errorf("the checkout has no full HEAD and branch")
	}
	if r.Head == "" {
		return nil, fmt.Errorf("the result names no commit; commit the work and name it")
	}
	stated, err := resolve(r.Head)
	if err != nil || stated != head {
		return nil, fmt.Errorf("result head %q is not the checkout's HEAD %s", r.Head, head)
	}
	// Only these Git-derived fields are repaired. Parsing again proves all six
	// required keys and the child's verdict still exist; none is filled by a guess.
	lines := strings.Split(string(raw), "\n")
	for i, line := range lines {
		if strings.EqualFold(strings.TrimSpace(line), "## Body") {
			break
		}
		key, _, ok := strings.Cut(strings.TrimSpace(line), ":")
		if ok && strings.EqualFold(key, "head") {
			lines[i] = "head: " + head
		}
		if ok && strings.EqualFold(key, "branch") {
			lines[i] = "branch: " + branch
		}
	}
	text := strings.Join(lines, "\n")
	r = typedrec.ParseCardResult([]byte(text))
	if !r.Shaped || r.Gate == "" || r.Output == "" {
		return nil, fmt.Errorf("RESULT.md needs head, branch, verdict, gate, output and report; preserve the actual verdict and evidence")
	}
	if r.Output != "-" {
		if err := artifact(r.Output); err != nil {
			return nil, fmt.Errorf("result output %q: %w", r.Output, err)
		}
	}
	// Complete only the body: a task's pinned source SHA in report metadata is
	// separate from the result commit and stays exactly as the child wrote it.
	for i, line := range lines {
		if strings.EqualFold(strings.TrimSpace(line), "## Body") {
			body, err := cardtree.CompleteVerdicts(strings.Join(lines[i+1:], "\n"), resolve)
			if err != nil {
				return nil, err
			}
			return []byte(strings.Join(lines[:i+1], "\n") + "\n" + body), nil
		}
	}
	return []byte(text), nil
}
