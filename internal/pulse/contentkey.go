package pulse

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Swarm Cards v2 (#2498, #2507): Resume by content key.
// The key is hash(card text without its label suffix) + base sha (+ route when applicable).
// The queue refuses to deal a card whose key already has a DONE result unless --force;
// fill/dealer prints "cached" with the prior result's PR.
// When the base moves, the key changes, distinguishing "done at an older base" from "never run".
//
// Architectural Boundaries (Stella, stella-48879a69c241):
// 1. Computation Key vs Reuse Authority: Text plus base identifies candidate computation,
//    not permission to reuse effects. Include repository, declared inputs/dependencies,
//    and route/provider where applicable.
// 2. Verified Artifacts Only: Reuse ONLY retained, verified result artifacts under declared
//    reuse policy. DONE means Returned, not approved, landed, or current-gated.
// 3. Active Leases: UNKNOWN or live attempts retain lease and fence and CANNOT be duplicated,
//    including with --force.
// 4. Ledger Integrity: Reuse is not a new zero-cost execution row.

const (
	KeyStatusDone    = "DONE"
	KeyStatusActive  = "ACTIVE"
	KeyStatusFailed  = "FAILED"
	KeysFileName     = "keys.tsv"
	KeysHeader       = "# key\tstatus\tpr\thead\tbase\tcard\tat\tattempt\n"
	defaultKeyPrefix = "ck"
)

// CardKey represents the composite computation key of a card.
type CardKey struct {
	TextHash string // 16-hex hash of card text without label suffix
	BaseSHA  string // base commit SHA (12-hex or full hex)
	Route    string // route or model tier if applicable
	Repo     string // repository if applicable
}

// String returns the formatted content key string: <textHash>:<baseSHA> or <textHash>:<baseSHA>:<route>.
func (k CardKey) String() string {
	base := strings.TrimSpace(k.BaseSHA)
	if base == "" {
		base = "dev"
	}
	if k.Route != "" {
		return fmt.Sprintf("%s:%s:%s", k.TextHash, base, k.Route)
	}
	return fmt.Sprintf("%s:%s", k.TextHash, base)
}

// ParseCardKey parses a formatted card key string back into a CardKey struct.
func ParseCardKey(s string) (CardKey, error) {
	s = strings.TrimSpace(s)
	parts := strings.Split(s, ":")
	if len(parts) < 2 {
		return CardKey{}, fmt.Errorf("invalid card key %q: expected <textHash>:<baseSHA>[:<route>]", s)
	}
	k := CardKey{
		TextHash: parts[0],
		BaseSHA:  parts[1],
	}
	if len(parts) >= 3 {
		k.Route = parts[2]
	}
	return k, nil
}

var (
	reResultCardLabel = regexp.MustCompile(`^(RESULT:?\s*)(CARD-[\w.-]+|card-[\w.-]+|\d+)\b\s*`)
	reLabelHeader     = regexp.MustCompile(`(?i)^LABEL:\s*.*$`)
	reBaseHeader      = regexp.MustCompile(`(?i)^BASE:\s*(\S+)`)
	reLineSha         = regexp.MustCompile(`\bsha=([a-f0-9]{7,40})\b`)
	reOntoBase        = regexp.MustCompile(`\bonto\s+([a-f0-9]{7,40}|\w+)\b`)
)

// StripCardLabel removes the variable label token/suffix (such as CARD-1, card-001, read-812, etc.)
// from card text so that re-cuts of the same card content yield an identical normalized text.
func StripCardLabel(text string, explicitLabel ...string) string {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	var out []string
	for i, line := range lines {
		trimmed := strings.TrimRight(line, " \t")
		if i == 0 {
			fields := strings.Fields(trimmed)
			if len(fields) > 0 && (fields[0] == "RESULT" || fields[0] == "RESULT:") {
				var keep []string
				keep = append(keep, "RESULT")
				labelSkipped := false
				for _, f := range fields[1:] {
					if strings.HasPrefix(f, "sha=") {
						continue
					}
					if !labelSkipped {
						labelSkipped = true
						continue
					}
					keep = append(keep, f)
				}
				trimmed = strings.Join(keep, " ")
			}
			for _, l := range explicitLabel {
				if l != "" {
					trimmed = strings.ReplaceAll(trimmed, "CARD-"+l, "")
					trimmed = strings.ReplaceAll(trimmed, "card-"+l, "")
					trimmed = strings.ReplaceAll(trimmed, l, "")
				}
			}
			fields = strings.Fields(trimmed)
			if len(fields) > 0 {
				trimmed = strings.Join(fields, " ")
			}
		}
		if reLabelHeader.MatchString(trimmed) {
			continue
		}
		out = append(out, trimmed)
	}
	for len(out) > 0 && strings.TrimSpace(out[0]) == "" {
		out = out[1:]
	}
	for len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
		out = out[:len(out)-1]
	}
	return strings.Join(out, "\n")
}

// TextHash computes a 16-character hexadecimal hash of the normalized card text.
func TextHash(normalizedText string) string {
	h := sha256.Sum256([]byte(normalizedText))
	return hex.EncodeToString(h[:8])
}

// ComputeCardKey computes the content key for a card text, base SHA, and route.
func ComputeCardKey(cardText, baseSHA, route string, explicitLabel ...string) CardKey {
	norm := StripCardLabel(cardText, explicitLabel...)
	th := TextHash(norm)
	base := strings.TrimSpace(baseSHA)
	if len(base) > 12 {
		base = base[:12]
	}
	if base == "" {
		base = ExtractCardBase(cardText)
		if len(base) > 12 {
			base = base[:12]
		}
	}
	if base == "" {
		base = "dev"
	}
	return CardKey{
		TextHash: th,
		BaseSHA:  base,
		Route:    strings.TrimSpace(route),
	}
}

// ExtractCardBase extracts the base SHA or branch from card text headers or content.
func ExtractCardBase(cardText string) string {
	scanner := bufio.NewScanner(strings.NewReader(cardText))
	for scanner.Scan() {
		line := scanner.Text()
		if m := reBaseHeader.FindStringSubmatch(line); len(m) > 1 {
			return m[1]
		}
		if m := reLineSha.FindStringSubmatch(line); len(m) > 1 {
			return m[1]
		}
		if m := reOntoBase.FindStringSubmatch(line); len(m) > 1 {
			return m[1]
		}
	}
	return ""
}

// KeyRecord is one durable entry in the queue's content key store.
type KeyRecord struct {
	Key       string // Content key string
	Status    string // DONE, ACTIVE, FAILED
	PR        int    // Result PR number
	Head      string // Result commit head SHA
	Base      string // Base SHA the card ran against
	Card      string // Card filename/identifier
	At        string // Timestamp RFC3339
	AttemptID string // Attempt ID (for active/live attempts)
	Repo      string // Target repository
	Route     string // Route/model tier
}

// KeyDecision represents the dealer's decision for a candidate card.
type KeyDecision int

const (
	DecisionNeverRun KeyDecision = iota
	DecisionCached
	DecisionOlderBase
	DecisionActive
	DecisionForce
)

// String returns a human-readable name for a KeyDecision.
func (d KeyDecision) String() string {
	switch d {
	case DecisionNeverRun:
		return "never-run"
	case DecisionCached:
		return "cached"
	case DecisionOlderBase:
		return "done-at-older-base"
	case DecisionActive:
		return "active-attempt"
	case DecisionForce:
		return "force-reexecute"
	default:
		return "unknown"
	}
}

// KeyStore manages card content keys under a queue directory.
type KeyStore struct {
	queue  string
	path   string
	mu     sync.Mutex
	keys   map[string]KeyRecord
	byHash map[string][]KeyRecord
}

// NewKeyStore initializes or loads a KeyStore under the queue directory.
func NewKeyStore(queue string) *KeyStore {
	ks := &KeyStore{
		queue:  queue,
		path:   filepath.Join(queue, KeysFileName),
		keys:   map[string]KeyRecord{},
		byHash: map[string][]KeyRecord{},
	}
	_ = ks.Load()
	return ks
}

// NewKeyStoreFromFile initializes or loads a KeyStore directly from a file path.
func NewKeyStoreFromFile(path string) *KeyStore {
	queue := filepath.Dir(path)
	ks := &KeyStore{
		queue:  queue,
		path:   path,
		keys:   map[string]KeyRecord{},
		byHash: map[string][]KeyRecord{},
	}
	_ = ks.Load()
	return ks
}

// Load reads existing records from keys.tsv and ledger.tsv into memory.
func (ks *KeyStore) Load() error {
	ks.mu.Lock()
	defer ks.mu.Unlock()

	// Read keys.tsv if present
	if raw, err := os.ReadFile(ks.path); err == nil {
		lines := strings.Split(string(raw), "\n")
		for _, l := range lines {
			l = strings.TrimSpace(l)
			if l == "" || strings.HasPrefix(l, "#") {
				continue
			}
			f := strings.Split(l, "\t")
			if len(f) < 2 {
				continue
			}
			rec := KeyRecord{
				Key:    f[0],
				Status: f[1],
			}
			if len(f) > 2 && f[2] != "-" && f[2] != "" {
				rec.PR, _ = strconv.Atoi(f[2])
			}
			if len(f) > 3 && f[3] != "-" {
				rec.Head = f[3]
			}
			if len(f) > 4 && f[4] != "-" {
				rec.Base = f[4]
			}
			if len(f) > 5 && f[5] != "-" {
				rec.Card = f[5]
			}
			if len(f) > 6 && f[6] != "-" {
				rec.At = f[6]
			}
			if len(f) > 7 && f[7] != "-" {
				rec.AttemptID = f[7]
			}
			ks.keys[rec.Key] = rec
			if ck, err := ParseCardKey(rec.Key); err == nil {
				ks.byHash[ck.TextHash] = append(ks.byHash[ck.TextHash], rec)
			}
		}
	}

	// Read ledger.tsv if present, joining any approved/done PRs with keys
	if rows, err := ReadLedger(ks.queue); err == nil {
		for _, r := range rows {
			if r.Key != "" && r.Key != ledgerDash {
				if existing, ok := ks.keys[r.Key]; !ok || existing.Status != KeyStatusDone {
					rec := KeyRecord{
						Key:    r.Key,
						Status: KeyStatusDone,
						PR:     r.PR,
						Head:   r.Head,
						Card:   r.Card,
						At:     r.At,
					}
					if ck, err := ParseCardKey(r.Key); err == nil {
						rec.Base = ck.BaseSHA
						ks.byHash[ck.TextHash] = append(ks.byHash[ck.TextHash], rec)
					}
					ks.keys[r.Key] = rec
				}
			}
		}
	}
	return nil
}

// CheckKey checks the candidate card key against the key store and returns the dealer decision.
// Stella Rule 3: Active or unknown attempts CANNOT be duplicated, including with --force.
func (ks *KeyStore) CheckKey(ck CardKey, force bool) (KeyDecision, *KeyRecord) {
	ks.mu.Lock()
	defer ks.mu.Unlock()

	kStr := ck.String()

	// 1. Active attempt: CANNOT be duplicated under any circumstances.
	if rec, ok := ks.keys[kStr]; ok && rec.Status == KeyStatusActive {
		return DecisionActive, &rec
	}

	// 2. Exact key DONE result
	if rec, ok := ks.keys[kStr]; ok && rec.Status == KeyStatusDone {
		if force {
			return DecisionForce, &rec
		}
		return DecisionCached, &rec
	}

	// 3. Checked for matching text hash at an older base
	if prevs, ok := ks.byHash[ck.TextHash]; ok {
		for _, p := range prevs {
			if p.Status == KeyStatusDone && p.Base != "" && p.Base != ck.BaseSHA {
				if force {
					return DecisionForce, &p
				}
				return DecisionOlderBase, &p
			}
		}
	}

	// 4. Never run
	return DecisionNeverRun, nil
}

// RecordActive records an active in-flight attempt in the key store.
func (ks *KeyStore) RecordActive(ck CardKey, card, attemptID string, at time.Time) error {
	ks.mu.Lock()
	defer ks.mu.Unlock()

	rec := KeyRecord{
		Key:       ck.String(),
		Status:    KeyStatusActive,
		Card:      card,
		Base:      ck.BaseSHA,
		AttemptID: attemptID,
		At:        at.UTC().Format(time.RFC3339),
	}
	ks.keys[rec.Key] = rec
	ks.byHash[ck.TextHash] = append(ks.byHash[ck.TextHash], rec)
	return ks.appendRow(rec)
}

// RecordDone records a verified completed DONE result in the key store.
func (ks *KeyStore) RecordDone(ck CardKey, card string, pr int, head string, at time.Time) error {
	ks.mu.Lock()
	defer ks.mu.Unlock()

	rec := KeyRecord{
		Key:    ck.String(),
		Status: KeyStatusDone,
		PR:     pr,
		Head:   head,
		Base:   ck.BaseSHA,
		Card:   card,
		At:     at.UTC().Format(time.RFC3339),
	}
	ks.keys[rec.Key] = rec
	ks.byHash[ck.TextHash] = append(ks.byHash[ck.TextHash], rec)
	return ks.appendRow(rec)
}

// RecordFailed records a failed attempt in the key store.
func (ks *KeyStore) RecordFailed(ck CardKey, card, reason string, at time.Time) error {
	ks.mu.Lock()
	defer ks.mu.Unlock()

	rec := KeyRecord{
		Key:    ck.String(),
		Status: KeyStatusFailed,
		Card:   card,
		Base:   ck.BaseSHA,
		At:     at.UTC().Format(time.RFC3339),
	}
	ks.keys[rec.Key] = rec
	return ks.appendRow(rec)
}

func (ks *KeyStore) appendRow(r KeyRecord) error {
	_ = os.MkdirAll(filepath.Dir(ks.path), 0o755)
	needsHeader := false
	if fi, err := os.Stat(ks.path); os.IsNotExist(err) || (err == nil && fi.Size() == 0) {
		needsHeader = true
	}
	f, err := os.OpenFile(ks.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()

	if needsHeader {
		if _, err := f.WriteString(KeysHeader); err != nil {
			return err
		}
	}
	prStr := "-"
	if r.PR > 0 {
		prStr = strconv.Itoa(r.PR)
	}
	head := r.Head
	if head == "" {
		head = "-"
	}
	base := r.Base
	if base == "" {
		base = "-"
	}
	card := r.Card
	if card == "" {
		card = "-"
	}
	attempt := r.AttemptID
	if attempt == "" {
		attempt = "-"
	}
	line := fmt.Sprintf("%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
		r.Key, r.Status, prStr, head, base, card, r.At, attempt)
	_, err = f.WriteString(line)
	return err
}

// ModelKeySummary holds grouped execution totals by model and card content key.
type ModelKeySummary struct {
	Model      string
	Key        string
	Runs       int
	Verified   int
	PRs        []int
	LatestBase string
}

// GroupByKey groups key records by model/route and content key.
func GroupByKey(records []KeyRecord) map[string]map[string]*ModelKeySummary {
	out := map[string]map[string]*ModelKeySummary{}
	for _, r := range records {
		model := r.Route
		if model == "" {
			model = "default"
		}
		if out[model] == nil {
			out[model] = map[string]*ModelKeySummary{}
		}
		sum := out[model][r.Key]
		if sum == nil {
			sum = &ModelKeySummary{
				Model:      model,
				Key:        r.Key,
				LatestBase: r.Base,
			}
			out[model][r.Key] = sum
		}
		sum.Runs++
		if r.Status == KeyStatusDone {
			sum.Verified++
			if r.PR > 0 {
				sum.PRs = append(sum.PRs, r.PR)
			}
		}
	}
	return out
}

// FormatPerModelKeyReport formats the per-model summary grouped by content key.
func FormatPerModelKeyReport(records []KeyRecord) string {
	grouped := GroupByKey(records)
	var b strings.Builder
	b.WriteString("# Per-Model Report Grouped by Content Key\n\n")

	models := make([]string, 0, len(grouped))
	for m := range grouped {
		models = append(models, m)
	}
	sort.Strings(models)

	for _, m := range models {
		b.WriteString(fmt.Sprintf("## Model: %s\n\n", m))
		b.WriteString("| Content Key | Runs | Verified | PRs | Base |\n")
		b.WriteString("|---|---|---|---|---|\n")
		keys := make([]string, 0, len(grouped[m]))
		for k := range grouped[m] {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			sum := grouped[m][k]
			prs := "-"
			if len(sum.PRs) > 0 {
				var prStrs []string
				for _, p := range sum.PRs {
					prStrs = append(prStrs, strconv.Itoa(p))
				}
				prs = strings.Join(prStrs, ",")
			}
			b.WriteString(fmt.Sprintf("| %s | %d | %d | %s | %s |\n",
				sum.Key, sum.Runs, sum.Verified, prs, sum.LatestBase))
		}
		b.WriteString("\n")
	}
	return b.String()
}
