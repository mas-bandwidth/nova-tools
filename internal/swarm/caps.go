package swarm

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// THE CAPS REGISTRY (issue #917, the mechanical half). A route's ceiling is not inferred
// from a worker description and never assumed: it is a row in a registry a person wrote,
// `provider<TAB>model|*<TAB>max_inflight<TAB>key_fingerprint`, read from `<root>/caps.tsv`.
// A row for the exact model overrides the provider's `*` row, a row is scoped to the key
// whose fingerprint it carries, and a route with no row is REFUSED by name -- because a
// ceiling nobody measured is a number this tool would otherwise invent.
//
// THE KEY IS NEVER IN THE REGISTRY. The fourth column is the sha256 prefix (8 hex) of the
// key value the worker's secret names, computed here from the value and never printed or
// written anywhere else: the fingerprint is the only form of the key that reaches a file or
// a line.

// CapRow is one row of the registry.
type CapRow struct {
	Provider    string
	Model       string // the model id, or "*" for every model this provider serves
	MaxInflight int
	KeyFP       string // the sha256 prefix the row is scoped to
}

// Caps is a parsed caps registry, in file order.
type Caps struct {
	Rows []CapRow
}

// KeyFingerprint is the sha256 prefix (8 lowercase hex) of a key value. It is the ONLY form
// of the key that may reach a registry row or an event line: the key itself is never here.
func KeyFingerprint(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])[:8]
}

// LoadCaps reads `<root>/caps.tsv`. Blank lines and `#` comments are skipped; a row that is
// not four tab-separated fields, a cap that is not a positive number, or a fingerprint that
// is not 8 lowercase hex is refused naming the line number -- a registry this tool guessed
// at is a registry that enforces nothing.
func LoadCaps(path string) (*Caps, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("the caps registry %s could not be read: %s", path, redactedReason(err))
	}
	defer f.Close()
	caps := &Caps{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	line := 0
	for sc.Scan() {
		line++
		raw := strings.TrimRight(sc.Text(), "\r")
		if strings.TrimSpace(raw) == "" || strings.HasPrefix(strings.TrimSpace(raw), "#") {
			continue
		}
		fields := strings.Split(raw, "\t")
		if len(fields) != 4 {
			return nil, fmt.Errorf("the caps registry %s line %d wants provider<TAB>model|*<TAB>max_inflight<TAB>key_fingerprint, got %d field(s)", path, line, len(fields))
		}
		provider := strings.TrimSpace(fields[0])
		model := strings.TrimSpace(fields[1])
		if provider == "" || model == "" {
			return nil, fmt.Errorf("the caps registry %s line %d names an empty provider or model", path, line)
		}
		capN, err := strconv.Atoi(strings.TrimSpace(fields[2]))
		if err != nil || capN < 1 {
			return nil, fmt.Errorf("the caps registry %s line %d wants max_inflight to be at least 1, got %q", path, line, fields[2])
		}
		fp := strings.TrimSpace(fields[3])
		if !isFingerprint(fp) {
			return nil, fmt.Errorf("the caps registry %s line %d wants the 8-hex sha256 prefix of the key value, got %q", path, line, fp)
		}
		caps.Rows = append(caps.Rows, CapRow{Provider: provider, Model: model, MaxInflight: capN, KeyFP: fp})
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("the caps registry %s could not be read: %s", path, redactedReason(err))
	}
	return caps, nil
}

// isFingerprint is exactly the 8 lowercase hex characters KeyFingerprint produces.
func isFingerprint(s string) bool {
	if len(s) != 8 {
		return false
	}
	for _, r := range s {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}

// Lookup answers the ceiling for (provider, model, fingerprint): a row naming the exact
// model overrides the provider's `*` row, and a row scoped to a different fingerprint does
// not apply. A route with no matching row is not found, and the caller refuses it by name.
func (c *Caps) Lookup(provider, model, fp string) (int, bool) {
	if c == nil {
		return 0, false
	}
	wildcard, hasWildcard := 0, false
	for _, r := range c.Rows {
		if r.Provider != provider || r.KeyFP != fp {
			continue
		}
		switch r.Model {
		case model:
			return r.MaxInflight, true
		case "*":
			wildcard, hasWildcard = r.MaxInflight, true
		}
	}
	return wildcard, hasWildcard
}

// CapState is one route's live count, the fingerprint it runs under and the ceiling the
// registry gives it. Found is false when the route has no row, which is what `status`
// prints as cap=-.
type CapState struct {
	Route    string
	KeyFP    string
	Inflight int
	Cap      int
	Found    bool
}

// CapStatus is the live count per (route, fingerprint) for every running task, in
// first-seen order. The registry supplies the cap; a route with no row carries Found=false.
func CapStatus(p *Pool, caps *Caps, slotsStore string) []CapState {
	if p == nil {
		return nil
	}
	tasks, err := p.List(Running)
	if err != nil {
		return nil
	}
	type key struct{ route, fp string }
	seen := map[key]*CapState{}
	var order []key
	for _, sc := range tasks {
		if sc.Provider == "" || sc.Model == "" {
			continue
		}
		route := sc.Provider + "/" + sc.Model
		k := key{route, sc.KeyFP}
		st := seen[k]
		if st == nil {
			st = &CapState{Route: route, KeyFP: sc.KeyFP}
			seen[k] = st
			order = append(order, k)
		}
		st.Inflight++
	}
	for _, k := range order {
		st := seen[k]
		provider, model, _ := strings.Cut(st.Route, "/")
		if c, ok := caps.Lookup(provider, model, st.KeyFP); ok {
			st.Cap, st.Found = c, true
		}
		// The bench slot store's leases are counted into the same route (issue #917).
		st.Inflight += SlotStoreInflightKey(slotsStore, st.Route, st.KeyFP)
	}
	var out []CapState
	for _, k := range order {
		out = append(out, *seen[k])
	}
	return out
}

// CapsInflight is the in-flight count for (provider, model, fingerprint): the running tasks
// in the pool that carry that route and fingerprint, plus, when a bench slot store is named,
// the leases under it whose route and key match. Two fingerprints never count against each
// other's cap.
func CapsInflight(p *Pool, slotsStore, provider, model, fp string) int {
	n := 0
	if p != nil {
		if tasks, err := p.List(Running); err == nil {
			for _, sc := range tasks {
				if sc.Provider == provider && sc.Model == model && sc.KeyFP == fp {
					n++
				}
			}
		}
	}
	n += SlotStoreInflightKey(slotsStore, provider+"/"+model, fp)
	return n
}

// SlotStoreInflightKey counts the leases in a bench slot store whose `route` and `key`
// fields match. A lease is a regular file holding a JSON object; one that does not read as
// one, or that carries another route or key, is not counted.
func SlotStoreInflightKey(store, route, fp string) int {
	if strings.TrimSpace(store) == "" {
		return 0
	}
	entries, err := os.ReadDir(store)
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(store, e.Name()))
		if err != nil {
			continue
		}
		var lease struct {
			Route string `json:"route"`
			Key   string `json:"key"`
		}
		if err := json.Unmarshal(raw, &lease); err != nil {
			continue
		}
		if lease.Route == route && lease.Key == fp {
			n++
		}
	}
	return n
}
