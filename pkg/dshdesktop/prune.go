package dshdesktop

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/atomicfile"
	"github.com/mas-bandwidth/nova-tools/pkg/codexdesktop"
)

// MaxResolvedNotices bounds the resolved notice-*.json files retained in a
// friend's events directory (and any claimed-*.json records in .notices)
// after consumption or proof (docs/SPEC-HOOK.md). Keeping the 16 newest
// resolved notices preserves more than two default down-after windows of
// recent deliveries for post-turn inspection while bounding disk growth.
const MaxResolvedNotices = 16

const pendingMarkerFile = ".pending"

type noticeStamp struct {
	SavedAt   string `json:"saved_at"`
	QueuedAt  string `json:"queuedAt"`
	Messages  int    `json:"messages"`
	Challenge *struct {
		Subject string `json:"subject"`
	} `json:"challenge"`
}

type resolvedCandidate struct {
	name     string
	fullPath string
	hasStamp bool
	stamp    time.Time
	queuedAt string
	modTime  time.Time
}

// DeliverAt publishes an immutable notification with the caller's timestamp
// and prunes resolved notice files beyond MaxResolvedNotices while keeping
// eventID as the active pending notice.
func DeliverAt(r Route, eventID, notice string, now time.Time) (string, error) {
	if err := ValidateRoute(r); err != nil {
		return "", fmt.Errorf("%w: %v", ErrPreSend, err)
	}
	if notice != BusNotice && notice != ChallengeNotice {
		return "", fmt.Errorf("%w: wants a fixed package notice", ErrPreSend)
	}
	path, err := PendingPath(r.EventsDir, eventID)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrPreSend, err)
	}
	if err := os.Mkdir(filepath.Dir(path), 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return "", fmt.Errorf("%w: create queue: %v", ErrPreSend, err)
	}
	data, err := json.Marshal(notification{
		QueuedAt:  fmt.Sprintf("%020d", now.UnixNano()),
		Version:   1,
		EventID:   eventID,
		Challenge: notice == ChallengeNotice,
	})
	if err == nil {
		err = atomicfile.Write(path, data, 0600, atomicfile.NoReplace())
	}
	if err != nil {
		kind := ErrOutcomeUnknown
		if errors.Is(err, os.ErrExist) {
			kind = ErrPreSend
		}
		// Atomic publication may already have succeeded when cleanup reports an
		// error. The receiver can have observed it; never promise it was unsent.
		return "", fmt.Errorf("%w: publish notice: %v", kind, err)
	}
	if err := MarkPending(r.EventsDir, eventID); err != nil {
		return "", fmt.Errorf("%w: mark pending notice: %v", ErrOutcomeUnknown, err)
	}
	if _, err := PruneResolvedNotices(r.EventsDir, eventID); err != nil {
		return "", fmt.Errorf("%w: %v", ErrOutcomeUnknown, err)
	}
	saved, _ := codexdesktop.SavedPath(r.EventsDir, eventID)
	return notice + " Saved external event JSON: " + saved, nil
}

// MarkPending records eventID as the active pending notice in directory so it
// is never removed before consumption or proof.
func MarkPending(directory, eventID string) error {
	if err := codexdesktop.ValidateDirectory(directory); err != nil {
		return err
	}
	key := noticeKey(eventID)
	if key == "" || !safeID(key) {
		return errors.New("event id is invalid")
	}
	marker := filepath.Join(directory, pendingMarkerFile)
	return atomicfile.Write(marker, []byte("notice-"+key+".json\n"), 0600)
}

// ResolveNotice marks eventID resolved (removing its queue record if present
// and clearing the pending marker when it matches eventID) and prunes older
// resolved notice files beyond MaxResolvedNotices.
func ResolveNotice(directory, eventID string) (int, error) {
	if err := codexdesktop.ValidateDirectory(directory); err != nil {
		return 0, err
	}
	key := noticeKey(eventID)
	if key == "" || !safeID(key) {
		return 0, errors.New("event id is invalid")
	}
	queueDir := filepath.Join(directory, ".notices")
	for _, name := range []string{
		"notice-" + eventID + ".json",
		"notice-notice-" + key + ".json",
		"notice-" + key + ".json",
		"claimed-" + eventID + ".json",
		"claimed-notice-" + key + ".json",
		"claimed-" + key + ".json",
	} {
		if err := os.Remove(filepath.Join(queueDir, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return 0, fmt.Errorf("remove resolved queue notice %s: %w", name, err)
		}
	}
	marker := filepath.Join(directory, pendingMarkerFile)
	if raw, err := os.ReadFile(marker); err == nil {
		if noticeKey(strings.TrimSpace(string(raw))) == key {
			if err := os.Remove(marker); err != nil && !errors.Is(err, os.ErrNotExist) {
				return 0, fmt.Errorf("clear pending marker: %w", err)
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return 0, fmt.Errorf("read pending marker: %w", err)
	}
	return PruneResolvedNotices(directory, "")
}

// PruneOnProve resolves the active pending notice when its challenge matches
// nonce (and holds no unconsumed ordinary messages), then prunes resolved
// notices in directory to MaxResolvedNotices while preserving any still-pending
// notice.
func PruneOnProve(directory, nonce string) (int, error) {
	if err := codexdesktop.ValidateDirectory(directory); err != nil {
		return 0, err
	}
	if _, err := os.Stat(directory); errors.Is(err, os.ErrNotExist) {
		return 0, nil
	} else if err != nil {
		return 0, fmt.Errorf("stat notices directory %s: %w", directory, err)
	}

	stillPending := ""
	marker := filepath.Join(directory, pendingMarkerFile)
	if raw, err := os.ReadFile(marker); err == nil {
		pendingKey := noticeKey(strings.TrimSpace(string(raw)))
		if pendingKey != "" {
			pendingPath := filepath.Join(directory, "notice-"+pendingKey+".json")
			if resolvedByProve(pendingPath, nonce) {
				if err := clearQueuedForKey(directory, pendingKey); err != nil {
					return 0, err
				}
				if err := os.Remove(marker); err != nil && !errors.Is(err, os.ErrNotExist) {
					return 0, fmt.Errorf("clear pending marker: %w", err)
				}
			} else {
				stillPending = "notice-" + pendingKey
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return 0, fmt.Errorf("read pending marker: %w", err)
	}

	return PruneResolvedNotices(directory, stillPending)
}

func resolvedByProve(path, nonce string) bool {
	raw, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var st noticeStamp
	if err := json.Unmarshal(raw, &st); err != nil {
		return false
	}
	if st.Messages > 0 {
		return false
	}
	if st.Challenge == nil || st.Challenge.Subject == "" {
		return true
	}
	if nonce == "" {
		return false
	}
	return st.Challenge.Subject == "HOOK CHALLENGE nonce="+nonce
}

func clearQueuedForKey(directory, key string) error {
	queueDir := filepath.Join(directory, ".notices")
	for _, name := range []string{
		"notice-" + key + ".json",
		"notice-notice-" + key + ".json",
		"claimed-" + key + ".json",
		"claimed-notice-" + key + ".json",
	} {
		if err := os.Remove(filepath.Join(queueDir, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove proved queue notice %s: %w", name, err)
		}
	}
	return nil
}

// PruneResolvedNotices removes resolved notice-*.json files in directory (and
// claimed-*.json files in directory/.notices) beyond the newest
// MaxResolvedNotices. It never removes pendingID, the notice named by .pending,
// or any notice that still has an unclaimed notice-*.json record in .notices.
func PruneResolvedNotices(directory, pendingID string) (int, error) {
	if err := codexdesktop.ValidateDirectory(directory); err != nil {
		return 0, err
	}
	entries, err := os.ReadDir(directory)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("read notices directory %s: %w", directory, err)
	}

	unresolved := map[string]bool{}
	if key := noticeKey(pendingID); key != "" {
		unresolved[key] = true
	}
	marker := filepath.Join(directory, pendingMarkerFile)
	if raw, err := os.ReadFile(marker); err == nil {
		if key := noticeKey(strings.TrimSpace(string(raw))); key != "" {
			unresolved[key] = true
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return 0, fmt.Errorf("read pending marker: %w", err)
	}

	queueDir := filepath.Join(directory, ".notices")
	queueEntries, qErr := os.ReadDir(queueDir)
	if qErr != nil && !errors.Is(qErr, os.ErrNotExist) {
		return 0, fmt.Errorf("read notice queue %s: %w", queueDir, qErr)
	}
	var claimedCandidates []resolvedCandidate
	for _, qe := range queueEntries {
		qName := qe.Name()
		switch {
		case isPrefixedNotice(qName, "notice-"):
			if key := noticeKey(qName); key != "" {
				unresolved[key] = true
			}
		case isPrefixedNotice(qName, "claimed-"):
			key := noticeKey(qName)
			if key != "" && unresolved[key] {
				continue
			}
			c, err := inspectCandidate(queueDir, qe)
			if err != nil {
				return 0, err
			}
			claimedCandidates = append(claimedCandidates, c)
		}
	}

	var resolved []resolvedCandidate
	for _, e := range entries {
		name := e.Name()
		if !isPrefixedNotice(name, "notice-") {
			continue
		}
		key := noticeKey(name)
		if key != "" && unresolved[key] {
			continue
		}
		c, err := inspectCandidate(directory, e)
		if err != nil {
			return 0, err
		}
		resolved = append(resolved, c)
	}

	pruned, err := pruneCandidates(resolved, MaxResolvedNotices)
	if err != nil {
		return pruned, err
	}
	if _, err := pruneCandidates(claimedCandidates, MaxResolvedNotices); err != nil {
		return pruned, err
	}
	return pruned, nil
}

func inspectCandidate(dir string, e os.DirEntry) (resolvedCandidate, error) {
	full := filepath.Join(dir, e.Name())
	info, err := e.Info()
	if err != nil {
		return resolvedCandidate{}, fmt.Errorf("stat notice %s: %w", full, err)
	}
	c := resolvedCandidate{
		name:     e.Name(),
		fullPath: full,
		modTime:  info.ModTime(),
	}
	if !info.Mode().IsRegular() {
		return c, nil
	}
	raw, err := os.ReadFile(full)
	if err != nil {
		return resolvedCandidate{}, fmt.Errorf("read notice %s: %w", full, err)
	}
	var st noticeStamp
	if json.Unmarshal(bytes.TrimSpace(raw), &st) == nil {
		if st.SavedAt != "" {
			if parsed, perr := time.Parse(time.RFC3339Nano, st.SavedAt); perr == nil {
				c.hasStamp = true
				c.stamp = parsed
			}
		} else if st.QueuedAt != "" {
			c.hasStamp = true
			c.queuedAt = st.QueuedAt
		}
	}
	return c, nil
}

func pruneCandidates(candidates []resolvedCandidate, keep int) (int, error) {
	if len(candidates) <= keep {
		return 0, nil
	}
	sort.Slice(candidates, func(i, j int) bool {
		a, b := candidates[i], candidates[j]
		if a.hasStamp != b.hasStamp {
			return !a.hasStamp
		}
		if a.hasStamp {
			if !a.stamp.Equal(b.stamp) {
				return a.stamp.Before(b.stamp)
			}
			if a.queuedAt != b.queuedAt {
				return a.queuedAt < b.queuedAt
			}
		}
		if !a.modTime.Equal(b.modTime) {
			return a.modTime.Before(b.modTime)
		}
		return a.name < b.name
	})
	toRemove := candidates[:len(candidates)-keep]
	pruned := 0
	for _, c := range toRemove {
		if err := os.Remove(c.fullPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return pruned, fmt.Errorf("prune resolved notice %s: %w", c.fullPath, err)
		}
		pruned++
	}
	return pruned, nil
}

func isPrefixedNotice(name, prefix string) bool {
	if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, ".json") {
		return false
	}
	id := strings.TrimSuffix(strings.TrimPrefix(name, prefix), ".json")
	return safeID(id)
}

func noticeKey(value string) string {
	s := strings.TrimSpace(value)
	s = strings.TrimSuffix(s, ".json")
	s = strings.TrimPrefix(s, "claimed-")
	for strings.HasPrefix(s, "notice-") && len(s) > len("notice-") {
		s = strings.TrimPrefix(s, "notice-")
	}
	return s
}

// ListedNoticeFiles returns the sorted notice-*.json filenames in directory.
func ListedNoticeFiles(directory string) ([]string, error) {
	entries, err := os.ReadDir(directory)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if isPrefixedNotice(e.Name(), "notice-") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}
