package pulse

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// admitted is one candidate's identity, written when pool admits it, so a later
// hold can name the same source kind and id the next pool looks up.
type admitted struct {
	Kind    string
	ID      string
	Label   string
	Attempt int
}

func readIdentity(root string) ([]admitted, error) {
	raw, err := os.ReadFile(filepath.Join(root, "identity.tsv"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []admitted
	for _, line := range strings.Split(string(raw), "\n") {
		parts := strings.Split(line, "\t")
		if len(parts) < 4 {
			continue
		}
		n, _ := strconv.Atoi(parts[3])
		out = append(out, admitted{Kind: parts[0], ID: parts[1], Label: parts[2], Attempt: n})
	}
	return out, nil
}

// identitiesFor finds every identity a label might name: the raw id, the
// stored label, or the sanitized id. Two hits are a collision, not a guess.
func identitiesFor(root, label string) ([]admitted, error) {
	rows, err := readIdentity(root)
	if err != nil {
		return nil, err
	}
	var out []admitted
	for _, r := range rows {
		if r.Label == label || r.ID == label || sanitizeID(r.ID) == label || sanitizeID(r.Label) == label {
			out = append(out, r)
		}
	}
	return out, nil
}

func lookupIdentity(root, label string) (kind, id string, attempt int, ok bool, err error) {
	matches, err := identitiesFor(root, label)
	if err != nil {
		return "", "", 0, false, err
	}
	if len(matches) == 1 {
		return matches[0].Kind, matches[0].ID, matches[0].Attempt, true, nil
	}
	if len(matches) > 1 {
		return "", "", 0, false, fmt.Errorf("ambiguous identity for %s", label)
	}
	return "", "", 0, false, nil
}

// cardIdentity reads the IDENTITY line a launched card carries. That line is
// the source, id, and attempt the card was admitted under, not a label guess.
func cardIdentity(path string) (admitted, bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return admitted{}, false
	}
	for _, line := range strings.Split(string(raw), "\n") {
		f := strings.Fields(line)
		if len(f) != 4 || f[0] != "IDENTITY" {
			continue
		}
		kind, id, attempt := "", "", 0
		for _, tok := range f[1:] {
			k, v, cut := strings.Cut(tok, "=")
			if !cut {
				continue
			}
			switch k {
			case "kind":
				kind = v
			case "id":
				id = v
			case "attempt":
				attempt, _ = strconv.Atoi(v)
			}
		}
		if kind != "" && id != "" {
			return admitted{Kind: kind, ID: id, Attempt: attempt}, true
		}
	}
	return admitted{}, false
}

// readSeen returns the set of (source,id) already carded, running, pr, or held.
// A retry state is not in the returned set, so a rewritten card is pooled again
// (rule 2). A hold is not a retry: the provider read may have been accepted,
// and the source stays out until a reconciler changes the seen state.
func readSeen(root string) (map[string]bool, error) {
	seen := map[string]bool{}
	raw, err := os.ReadFile(filepath.Join(root, "seen.tsv"))
	if err != nil {
		if os.IsNotExist(err) {
			return seen, nil
		}
		return nil, err
	}
	for _, line := range strings.Split(strings.TrimRight(string(raw), "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) < 3 {
			continue
		}
		if parts[2] == "carded" || parts[2] == "running" || parts[2] == "pr" || parts[2] == "hold" {
			seen[parts[0]+"\x00"+parts[1]] = true
		}
	}
	return seen, nil
}
