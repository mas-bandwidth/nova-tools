package bus

// This file holds the state machine behind bounded inbox bodies.  The command layer owns
// roster resolution and output; this layer owns the part where a bad continuation could
// otherwise lose a note.  In particular, a token identifies an immutable git snapshot and
// never an entry in the working tree.

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"
)

const (
	bodyTokenVersion = 1
	bodyTokenLimit   = 8 << 10
	// Snapshot Git responses are protocol data, not a stream for arbitrary repository
	// contents. SplitDraft folds CRLF to LF, so a legal one-MiB body can occupy two MiB in
	// Git; the bounded header allowance covers the parser's heading and whitespace
	// separator forms without inventing a second header grammar here.
	bodySnapshotHeaderLimit = 64 << 10
	bodySnapshotGitLimit    = (2 * 1 << 20) + bodySnapshotHeaderLimit
)

// BodySnapshot is the immutable identity captured by a first bodies call.  Base and Head
// are full commit ids; Reader and Selector bind a token to the same roster spelling and
// inbox selection that made it.
type BodySnapshot struct {
	Base, Head       string
	Reader, Selector string
}

// BodyItem is one eligible listing item as it existed in Snapshot.Head.  Offset makes an
// appended receipt record distinct from another record in the same path; ordinary notes
// use offset zero.  Body is deliberately bytes, rather than a path to re-open later.
type BodyItem struct {
	Commit string
	Path   string
	Offset int
	Entry  OpenEntry
	Body   []byte
	// Bytes is the source body size when Body is deliberately absent for an object that
	// is already larger than the hard page ceiling.  Zero means len(Body), including a
	// real empty body.  It keeps an oversize item explicit without reading it unboundedly.
	Bytes int64
}

func (i BodyItem) key() bodyItemKey {
	return bodyItemKey{Commit: i.Commit, Path: i.Path, Offset: i.Offset}
}

type bodyItemKey struct {
	Commit string `json:"c"`
	Path   string `json:"p"`
	Offset int    `json:"o"`
}

func (k bodyItemKey) equal(other bodyItemKey) bool {
	return k.Commit == other.Commit && k.Path == other.Path && k.Offset == other.Offset
}

// BodyGap is the first-class record of an item too large for a whole call budget.  It is
// never an instruction to skip the note: a later fresh snapshot can deliver it.
type BodyGap struct {
	ID     string
	Commit string
	Path   string
	Offset int
	Bytes  int64
}

// BodyPageRequest is one page turn.  ExpectedCursor is read from disk immediately before
// the turn; a continuation refuses when another reader changed it.
type BodyPageRequest struct {
	Snapshot       BodySnapshot
	ExpectedCursor string
	Advance        bool
	Token          string
	MaxNotes       int
	MaxBytes       int64
}

// BodyPage is selection only.  The caller prints Items and Gaps, then may advance only to
// SafeFrontier after the corresponding bytes reached stdout.
type BodyPage struct {
	Emissions    []BodyEmission
	Items        []BodyItem
	Gaps         []BodyGap
	EarliestGap  *BodyGap
	PrintedBytes int64
	Frames       int
	GapCount     int
	Drained      bool
	Complete     bool
	Next         string
	SafeFrontier string
}

// BodyEmission preserves snapshot order for the command writer. Exactly one of Item and
// Gap is non-nil; the parallel Items/Gaps slices are retained for counts and callers that
// need a particular class without re-parsing output.
type BodyEmission struct {
	Item *BodyItem
	Gap  *BodyGap
}

type bodyToken struct {
	Version  int          `json:"v"`
	Base     string       `json:"b"`
	Head     string       `json:"h"`
	Reader   string       `json:"r"`
	Selector string       `json:"s"`
	Last     *bodyItemKey `json:"l,omitempty"`
	Gap      *bodyItemKey `json:"g,omitempty"`
	Gaps     int          `json:"n"`
	Frontier string       `json:"f"`
	Expected string       `json:"e"`
}

// BodyPageFor selects a page from one canonical snapshot order.  Items must already be
// sorted by first-parent commit, bytewise path, then record offset; we validate that rather
// than sorting a caller's accidental display order into a silently different continuation.
func BodyPageFor(items []BodyItem, request BodyPageRequest) (BodyPage, error) {
	if err := validBodyRequest(request); err != nil {
		return BodyPage{}, err
	}
	if err := validBodyItems(items, request.Snapshot); err != nil {
		return BodyPage{}, err
	}

	token := bodyToken{Version: bodyTokenVersion, Base: request.Snapshot.Base, Head: request.Snapshot.Head, Reader: request.Snapshot.Reader, Selector: request.Snapshot.Selector, Expected: request.ExpectedCursor}
	start := 0
	if request.Token != "" {
		var err error
		token, err = decodeBodyToken(request.Token)
		if err != nil {
			return BodyPage{}, err
		}
		if token.Base != request.Snapshot.Base || token.Head != request.Snapshot.Head || token.Reader != request.Snapshot.Reader || token.Selector != request.Snapshot.Selector {
			return BodyPage{}, fmt.Errorf("continuation belongs to another reader, selector, or snapshot; start a fresh bodies read")
		}
		if token.Expected != request.ExpectedCursor {
			return BodyPage{}, fmt.Errorf("the persisted cursor changed since this continuation; start a fresh bodies read")
		}
		if token.Last == nil {
			return BodyPage{}, fmt.Errorf("continuation has no last accounted item")
		}
		var found bool
		for i, item := range items {
			if item.key().equal(*token.Last) {
				start, found = i+1, true
				break
			}
		}
		if !found {
			return BodyPage{}, fmt.Errorf("continuation names no item in its snapshot")
		}
		if err := validateTokenAccounting(items, token, start); err != nil {
			return BodyPage{}, err
		}
	}

	page := BodyPage{GapCount: token.Gaps, SafeFrontier: token.Frontier}
	last := token.Last
	earliest := token.Gap
	if earliest != nil {
		for _, item := range items {
			if item.key().equal(*earliest) {
				page.EarliestGap = &BodyGap{ID: item.Entry.ID, Commit: item.Commit, Path: item.Path, Offset: item.Offset, Bytes: bodyItemBytes(item)}
				break
			}
		}
	}
	for i := start; i < len(items); i++ {
		item := items[i]
		// The note cap includes a gap.  Nothing at this position was accounted, so it
		// remains the first candidate of the next page.
		if len(page.Items)+len(page.Gaps) >= request.MaxNotes {
			break
		}
		size := bodyItemBytes(item)
		if !bodyFrameItem(item) {
			size = 0
		}
		if size > request.MaxBytes {
			gap := BodyGap{ID: item.Entry.ID, Commit: item.Commit, Path: item.Path, Offset: item.Offset, Bytes: size}
			page.Gaps = append(page.Gaps, gap)
			page.Emissions = append(page.Emissions, BodyEmission{Gap: &page.Gaps[len(page.Gaps)-1]})
			page.GapCount++
			if earliest == nil {
				key := item.key()
				earliest = &key
				page.EarliestGap = &page.Gaps[len(page.Gaps)-1]
			}
			key := item.key()
			last = &key
			continue
		}
		// A body that fits a whole call but not its remaining bytes is deferred intact.
		if size > request.MaxBytes-page.PrintedBytes {
			break
		}
		page.Items = append(page.Items, item)
		page.Emissions = append(page.Emissions, BodyEmission{Item: &page.Items[len(page.Items)-1]})
		page.PrintedBytes += size
		if bodyFrameItem(item) {
			page.Frames++
		}
		key := item.key()
		last = &key
	}

	accounted := start - 1
	if last != nil {
		for i := start; i < len(items); i++ {
			if items[i].key().equal(*last) {
				accounted = i
				break
			}
		}
	}
	page.Drained = accounted == len(items)-1
	page.Complete = page.Drained && earliest == nil
	page.SafeFrontier = safeBodyFrontier(items, accounted, earliest)
	if !page.Drained {
		expected := request.ExpectedCursor
		if request.Advance && page.SafeFrontier != "" {
			expected = page.SafeFrontier
		}
		next := bodyToken{Version: bodyTokenVersion, Base: request.Snapshot.Base, Head: request.Snapshot.Head, Reader: request.Snapshot.Reader, Selector: request.Snapshot.Selector, Last: last, Gap: earliest, Gaps: page.GapCount, Frontier: page.SafeFrontier, Expected: expected}
		encoded, err := encodeBodyToken(next)
		if err != nil {
			return BodyPage{}, err
		}
		page.Next = encoded
	}
	return page, nil
}

func bodyFrameItem(item BodyItem) bool {
	return item.Entry.Kind != OpenReceipt && !item.Entry.Heard
}

func bodyItemBytes(item BodyItem) int64 {
	if item.Bytes > 0 {
		return item.Bytes
	}
	return int64(len(item.Body))
}

func validBodyRequest(r BodyPageRequest) error {
	if r.MaxNotes < 1 || r.MaxNotes > 1000 {
		return fmt.Errorf("--max-notes must be between 1 and 1000")
	}
	if r.MaxBytes < 1 || r.MaxBytes > 1048576 {
		return fmt.Errorf("--max-bytes must be between 1 and 1048576")
	}
	for name, commit := range map[string]string{"base cursor": r.Snapshot.Base, "snapshot head": r.Snapshot.Head} {
		if commit != "" {
			if err := ValidCommitHex(commit); err != nil {
				return fmt.Errorf("%s: %w", name, err)
			}
		}
	}
	if r.Snapshot.Head == "" || r.Snapshot.Reader == "" || r.Snapshot.Selector == "" {
		return fmt.Errorf("snapshot is missing its head, reader, or selector")
	}
	if r.ExpectedCursor != "" {
		if err := ValidCommitHex(r.ExpectedCursor); err != nil {
			return fmt.Errorf("expected cursor: %w", err)
		}
	}
	return nil
}

func validBodyItems(items []BodyItem, snapshot BodySnapshot) error {
	seen := make(map[bodyItemKey]bool, len(items))
	for i, item := range items {
		if err := ValidCommitHex(item.Commit); err != nil {
			return fmt.Errorf("snapshot item %d: %w", i, err)
		}
		if !validSnapshotPath(item.Path) || item.Offset < 0 || item.Bytes < 0 {
			return fmt.Errorf("snapshot item %d has an invalid path or record offset", i)
		}
		key := item.key()
		if seen[key] {
			return fmt.Errorf("snapshot repeats %s", item.Path)
		}
		seen[key] = true
		if i > 0 {
			previous := items[i-1]
			if item.Commit == previous.Commit {
				if item.Path < previous.Path || (item.Path == previous.Path && item.Offset <= previous.Offset) {
					return fmt.Errorf("snapshot items are not in first-parent commit, path, record-offset order")
				}
			} else {
				for j := 0; j < i-1; j++ {
					if items[j].Commit == item.Commit {
						return fmt.Errorf("snapshot items split one first-parent commit")
					}
				}
			}
		}
	}
	return nil
}

func validSnapshotPath(p string) bool {
	return p != "" && !strings.HasPrefix(p, "/") && path.Clean(p) == p && p != "." && !strings.HasPrefix(p, "../")
}

func validateTokenAccounting(items []BodyItem, token bodyToken, start int) error {
	if token.Gaps < 0 {
		return fmt.Errorf("continuation has a negative gap count")
	}
	var gap *bodyItemKey
	count := 0
	for i := 0; i < start; i++ {
		// We cannot infer gaps from a body alone, but a token may never claim a frontier
		// after its earliest stated gap, and its key must be inside the accounted prefix.
		if token.Gap != nil && items[i].key().equal(*token.Gap) {
			gap = token.Gap
		}
	}
	if token.Gap != nil && gap == nil {
		return fmt.Errorf("continuation names a gap outside its accounted prefix")
	}
	if token.Gap == nil && token.Gaps != 0 {
		return fmt.Errorf("continuation has gaps but no earliest gap")
	}
	if token.Gap != nil {
		count = token.Gaps
		if count < 1 {
			return fmt.Errorf("continuation names a gap with zero count")
		}
	}
	_ = count
	want := safeBodyFrontier(items, start-1, token.Gap)
	if token.Frontier != want {
		return fmt.Errorf("continuation safe frontier is inconsistent with its accounted prefix")
	}
	return nil
}

func safeBodyFrontier(items []BodyItem, accounted int, gap *bodyItemKey) string {
	if accounted < 0 {
		return ""
	}
	limit := accounted
	if gap != nil {
		gapIndex := -1
		for i := 0; i <= accounted; i++ {
			if items[i].key().equal(*gap) {
				gapIndex = i
				break
			}
		}
		if gapIndex < 0 {
			return ""
		}
		// A gap's own commit is never safe, even when its other items happened to
		// print.  The greatest safe prefix ends at the preceding whole commit.
		limit = gapIndex - 1
	}
	frontier := ""
	for i := 0; i <= limit; {
		commit := items[i].Commit
		end := i
		for end+1 < len(items) && items[end+1].Commit == commit {
			end++
		}
		if end > limit {
			break
		}
		frontier = commit
		i = end + 1
	}
	return frontier
}

func encodeBodyToken(token bodyToken) (string, error) {
	raw, err := json.Marshal(token)
	if err != nil {
		return "", err
	}
	if len(raw) > bodyTokenLimit {
		return "", fmt.Errorf("continuation is too large")
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func decodeBodyToken(encoded string) (bodyToken, error) {
	if len(encoded) == 0 || len(encoded) > bodyTokenLimit*2 {
		return bodyToken{}, fmt.Errorf("continuation is malformed or over 8KiB")
	}
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || len(raw) == 0 || len(raw) > bodyTokenLimit {
		return bodyToken{}, fmt.Errorf("continuation is malformed or over 8KiB")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var token bodyToken
	if err := dec.Decode(&token); err != nil || dec.More() {
		return bodyToken{}, fmt.Errorf("continuation is malformed")
	}
	if token.Version != bodyTokenVersion {
		return bodyToken{}, fmt.Errorf("continuation version %d is not supported", token.Version)
	}
	if token.Head == "" || token.Reader == "" || token.Selector == "" {
		return bodyToken{}, fmt.Errorf("continuation is missing snapshot identity")
	}
	for name, commit := range map[string]string{"base cursor": token.Base, "snapshot head": token.Head, "frontier": token.Frontier, "expected cursor": token.Expected} {
		if commit != "" {
			if err := ValidCommitHex(commit); err != nil {
				return bodyToken{}, fmt.Errorf("continuation %s: %w", name, err)
			}
		}
	}
	return token, nil
}

// BodyContinuation exposes only the immutable identity and expected persisted cursor a
// command needs to rebuild the same snapshot.  The accounting fields remain private to the
// paginator so the command cannot accidentally make a second token grammar.
func BodyContinuation(encoded string) (BodySnapshot, string, error) {
	token, err := decodeBodyToken(encoded)
	if err != nil {
		return BodySnapshot{}, "", err
	}
	return BodySnapshot{Base: token.Base, Head: token.Head, Reader: token.Reader, Selector: token.Selector}, token.Expected, nil
}

// BodyItemsAtSnapshot reads bodies from the pinned commits, never the checkout.  Eligible
// is the result of the existing roster/path/receipt selector; this function preserves that
// decision and supplies stable commit/path/offset identities for its note entries.
func BodyItemsAtSnapshot(root string, snapshot BodySnapshot, eligible []OpenEntry) ([]BodyItem, error) {
	records := make([]BodyItem, 0, len(eligible))
	for _, entry := range eligible {
		records = append(records, BodyItem{Path: entry.Path, Entry: entry})
	}
	return BodyRecordsAtSnapshot(root, snapshot, records)
}

// BodyRecordsAtSnapshot is the record-aware source form.  Several eligible records may
// share a path (for example appended receipt records); Offset is therefore part of the
// identity and this function never collapses them through a path-keyed map.
func BodyRecordsAtSnapshot(root string, snapshot BodySnapshot, eligible []BodyItem) ([]BodyItem, error) {
	if err := validBodyRequest(BodyPageRequest{Snapshot: snapshot, MaxNotes: 1, MaxBytes: 1}); err != nil {
		return nil, err
	}
	resolvedHead, err := ResolveCommit(root, snapshot.Head)
	if err != nil || resolvedHead != snapshot.Head {
		return nil, fmt.Errorf("snapshot head is unavailable")
	}
	if snapshot.Base != "" {
		resolvedBase, baseErr := ResolveCommit(root, snapshot.Base)
		if baseErr != nil || resolvedBase != snapshot.Base {
			return nil, fmt.Errorf("snapshot base cursor is unavailable")
		}
		ancestor, ancestorErr := isAncestorOf(root, snapshot.Base, snapshot.Head)
		if ancestorErr != nil || !ancestor {
			return nil, fmt.Errorf("snapshot base cursor is not an ancestor of its head")
		}
	}
	commits, err := firstParentRange(root, snapshot.Base, snapshot.Head)
	if err != nil {
		return nil, err
	}
	wanted := make(map[string][]BodyItem, len(eligible))
	for _, entry := range eligible {
		if !validSnapshotPath(entry.Path) || entry.Offset < 0 || entry.Bytes < 0 {
			return nil, fmt.Errorf("eligible entry has invalid path or record offset %q", entry.Path)
		}
		wanted[entry.Path] = append(wanted[entry.Path], entry)
	}
	byKey := make(map[bodyItemKey]BodyItem, len(eligible))
	for _, commit := range commits {
		out, err := firstParentChangedPaths(root, commit)
		if err != nil {
			return nil, err
		}
		for _, p := range strings.Fields(out) {
			records, wantedHere := wanted[p]
			if !wantedHere {
				continue
			}
			needsBody := false
			for _, record := range records {
				needsBody = needsBody || bodyFrameItem(record)
			}
			body := []byte(nil)
			bodyBytes := int64(0)
			if needsBody {
				note, sourceBody, sourceBytes, err := snapshotNote(root, commit, p)
				if err != nil {
					return nil, fmt.Errorf("snapshot %s cannot parse %s: %w", commit, p, err)
				}
				_ = note
				body, bodyBytes = sourceBody, sourceBytes
			}
			for _, record := range records {
				record.Commit, record.Path, record.Body, record.Bytes = commit, p, body, bodyBytes
				byKey[record.key()] = record
			}
		}
	}
	items := make([]BodyItem, 0, len(byKey))
	for _, commit := range commits {
		var inCommit []BodyItem
		for _, item := range byKey {
			if item.Commit == commit {
				inCommit = append(inCommit, item)
			}
		}
		sort.Slice(inCommit, func(i, j int) bool {
			if inCommit[i].Path != inCommit[j].Path {
				return inCommit[i].Path < inCommit[j].Path
			}
			return inCommit[i].Offset < inCommit[j].Offset
		})
		items = append(items, inCommit...)
	}
	return items, nil
}

// BodyNewItemsAtSnapshot reconstructs the original NEW half from C0..H itself.  It does
// not consult the current OPEN file: a reply or receipt committed after H may legitimately
// remove an entry from today's OPEN, but must not erase that entry from an older token's
// immutable listing.  The caller has already resolved the reader against the roster; each
// note is still checked against that roster before it becomes a body item.
func BodyNewItemsAtSnapshot(root string, snapshot BodySnapshot, c *Config, me Participant, maxWords int, legacy LegacyLine) ([]BodyItem, error) {
	if err := validBodyRequest(BodyPageRequest{Snapshot: snapshot, MaxNotes: 1, MaxBytes: 1}); err != nil {
		return nil, err
	}
	if snapshot.Reader != me.Name || snapshot.Selector != "inbox-new" {
		return nil, fmt.Errorf("snapshot belongs to another reader or selector")
	}
	if c == nil || me.Lane == "" {
		return nil, fmt.Errorf("snapshot reader has no roster lane")
	}
	resolvedHead, err := ResolveCommit(root, snapshot.Head)
	if err != nil || resolvedHead != snapshot.Head {
		return nil, fmt.Errorf("snapshot head is unavailable")
	}
	if snapshot.Base != "" {
		resolvedBase, baseErr := ResolveCommit(root, snapshot.Base)
		if baseErr != nil || resolvedBase != snapshot.Base {
			return nil, fmt.Errorf("snapshot base cursor is unavailable")
		}
		if ancestor, ancestorErr := isAncestorOf(root, snapshot.Base, snapshot.Head); ancestorErr != nil || !ancestor {
			return nil, fmt.Errorf("snapshot base cursor is not an ancestor of its head")
		}
	}
	commits, err := firstParentRange(root, snapshot.Base, snapshot.Head)
	if err != nil {
		return nil, err
	}
	heard, err := receiptTargetsAtSnapshot(root, snapshot.Head, me.Lane)
	if err != nil {
		return nil, err
	}
	// ChangedSince reports one path at HEAD, not every historical revision in its range.
	// Mirror that selector against C0..H: a note edited twice is one item carrying H's
	// final body, and a reply whose Re: was later removed must not settle another note.
	latest := make(map[string]string)
	for _, commit := range commits {
		out, err := firstParentChangedPaths(root, commit)
		if err != nil {
			return nil, err
		}
		for _, p := range strings.Fields(out) {
			if isNotePath(p) {
				latest[p] = commit
			}
		}
	}
	answered := make(map[string]bool)
	type candidate struct {
		item BodyItem
		note Note
	}
	var candidates []candidate
	for _, commit := range commits {
		paths := make([]string, 0)
		for p, last := range latest {
			if last == commit {
				paths = append(paths, p)
			}
		}
		sort.Strings(paths)
		for _, p := range paths {
			note, body, bodyBytes, err := snapshotNote(root, snapshot.Head, p)
			if err != nil {
				var sourceErr *snapshotSourceError
				if errors.As(err, &sourceErr) {
					return nil, fmt.Errorf("snapshot %s cannot read %s: %w", commit, p, sourceErr)
				}
				continue
			}
			if note.Lane == me.Lane {
				for _, re := range note.Header.Re {
					answered[re] = true
				}
				continue
			}
			if !addressedTo(c, &note, me) || legacy.covers(note.legacyDay()) {
				continue
			}
			entry := openEntryFor(c, &note, me, heard[note.Header.ID] || heard[note.Path], maxWords)
			candidates = append(candidates, candidate{item: BodyItem{Commit: commit, Path: p, Entry: entry, Body: body, Bytes: bodyBytes}, note: note})
		}
	}
	items := make([]BodyItem, 0, len(candidates))
	for _, candidate := range candidates {
		if !answered[candidate.note.Header.ID] && !answered[candidate.note.Path] {
			items = append(items, candidate.item)
		}
	}
	return items, nil
}

func receiptTargetsAtSnapshot(root, head, lane string) (map[string]bool, error) {
	targets := map[string]bool{}
	path := lane + "/" + ReceiptsName
	tree, err := gitOutputAtMost(root, bodySnapshotGitLimit, "ls-tree", "-z", head, "--", path)
	if err != nil {
		return nil, fmt.Errorf("snapshot %s cannot inspect %s: %w", head, path, err)
	}
	if tree == "" {
		return targets, nil
	}
	raw, err := gitOutputAtMost(root, bodySnapshotGitLimit, "show", head+":"+path)
	if err != nil {
		return nil, fmt.Errorf("snapshot %s cannot read %s: %w", head, path, err)
	}
	for _, record := range records(raw) {
		if _, target, ok := strings.Cut(record.text, " "); ok {
			targets[strings.TrimSpace(target)] = true
		}
	}
	return targets, nil
}

// snapshotNote streams one pinned blob through ParseNoteBodyStream. Git stays under its
// existing timeout and the parser retains at most one legal body while still counting a
// larger one exactly, so a three-MiB body is named as three MiB rather than a fabricated
// lower bound.
type snapshotSourceError struct{ err error }

func (e *snapshotSourceError) Error() string { return e.err.Error() }
func (e *snapshotSourceError) Unwrap() error { return e.err }

func snapshotSource(err error) error { return &snapshotSourceError{err: err} }

func snapshotNote(root, commit, p string) (Note, []byte, int64, error) {
	var note Note
	var body []byte
	var size int64
	var parseErr error
	err := gitReadBounded(root, func(r io.Reader) error {
		note, body, size, parseErr = ParseNoteBodyStream(p, r, 1048576, bodySnapshotHeaderLimit)
		return parseErr
	}, "show", commit+":"+p)
	if err != nil {
		if parseErr != nil {
			var headerErr *noteHeaderLimitError
			if errors.As(parseErr, &headerErr) {
				return Note{}, nil, 0, snapshotSource(parseErr)
			}
			return Note{}, nil, 0, parseErr
		}
		return Note{}, nil, 0, snapshotSource(err)
	}
	return note, body, size, nil
}

func firstParentRange(root, base, head string) ([]string, error) {
	args := []string{"rev-list", "--reverse", "--first-parent"}
	if base != "" {
		args = append(args, base+".."+head)
	} else {
		args = append(args, "--root", head)
	}
	out, err := gitOutputAtMost(root, bodySnapshotGitLimit, args...)
	if err != nil {
		return nil, err
	}
	commits := strings.Fields(out)
	for _, commit := range commits {
		if err := ValidCommitHex(commit); err != nil {
			return nil, err
		}
	}
	return commits, nil
}

func firstParentChangedPaths(root, commit string) (string, error) {
	parents, err := gitOutputAtMost(root, bodySnapshotGitLimit, "rev-list", "--parents", "-n", "1", commit)
	if err != nil {
		return "", err
	}
	fields := strings.Fields(parents)
	if len(fields) == 0 || fields[0] != commit {
		return "", fmt.Errorf("cannot determine first parent of snapshot commit %s", commit)
	}
	if len(fields) == 1 {
		return gitOutputAtMost(root, bodySnapshotGitLimit, "diff-tree", "--root", "--no-commit-id", "--name-only", "--diff-filter=AM", "-r", commit)
	}
	return gitOutputAtMost(root, bodySnapshotGitLimit, "diff-tree", "--no-commit-id", "--name-only", "--diff-filter=AM", "-r", fields[1], commit)
}
