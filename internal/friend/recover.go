package friend

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
)

// RecoverMax is the maximum number of recoveries an hour before the session
// stays broken and the coordinator is told it needs a person.
const DefaultRecoverMax = 3

// RecoverAfter is how long to wait before trying to recover a broken session.
const DefaultRecoverAfter = 5 * time.Minute

// RecoverStuckAfter is how long a turn may run with output but no tool progress
// before being considered stuck (--stuck-after).
const DefaultRecoverStuckAfter = 45 * time.Minute

// RecoverCompactionTurns is how many turns in a row the harness may compact
// with no other output before triggering recovery.
const DefaultRecoverCompactionTurns = 3

// contextLimitPatterns are error patterns that indicate a context limit.
var contextLimitPatterns = []string{
	"context-length",
	"prompt-too-long",
	"context length",
	"maximum context",
}

// compactionPattern is the output pattern that indicates compaction.
var compactionPattern = "compacting"

// SessionRecovery tracks recovery state for a broken session.
type SessionRecovery struct {
	OldSessionID   string
	NewSessionID   string
	Reason         string
	At             time.Time
	RecoveryCount  int
	LastRecovery   time.Time
	HandoffWritten bool
}

// canRecover checks if we're within the recovery limit.
func canRecover(r *SessionRecovery, max int) bool {
	if r == nil {
		return true
	}
	if r.LastRecovery.IsZero() {
		return true
	}
	// Count recoveries within the last hour
	windowStart := time.Now().Add(-time.Hour)
	if r.LastRecovery.After(windowStart) {
		return r.RecoveryCount < max
	}
	return true
}

// detectContextLimit checks if an error message indicates a context limit.
func detectContextLimit(err string) bool {
	errLower := strings.ToLower(err)
	for _, pattern := range contextLimitPatterns {
		if strings.Contains(errLower, pattern) {
			return true
		}
	}
	return false
}

// detectCompaction checks if output indicates a compaction loop.
func detectCompaction(output string) bool {
	return strings.Contains(strings.ToLower(output), compactionPattern)
}

// detectStuckTurn checks if a turn is stuck (running past stuckAfter with output but no progress).
func detectStuckTurn(duration time.Duration, hasToolProgress bool) bool {
	return !hasToolProgress && duration >= DefaultRecoverStuckAfter
}

// handoffMessage creates the handoff turn text.
func handoffMessage(queueJSON string, cairnContent string, pongCommand string, pending []bus.Message, reason string, oldID, newID string) string {
	var b strings.Builder
	
	b.WriteString("nova-friend: session recovered\n")
	b.WriteString(fmt.Sprintf("Reason: %s\n", reason))
	b.WriteString(fmt.Sprintf("Old session: %s\n", oldID))
	b.WriteString(fmt.Sprintf("New session: %s\n", newID))
	b.WriteString("\n")
	
	if pongCommand != "" {
		b.WriteString(fmt.Sprintf("Run this now, first, exactly as written: %s\n", pongCommand))
		b.WriteString("Then read on.\n\n")
	}
	
	b.WriteString("=== QUEUE ===\n")
	b.WriteString(queueJSON)
	b.WriteString("\n\n")
	
	if cairnContent != "" {
		b.WriteString("=== STATE ===\n")
		b.WriteString(cairnContent)
		b.WriteString("\n\n")
	}
	
	if len(pending) > 0 {
		b.WriteString(fmt.Sprintf("%d pending message(s) for you, oldest first:\n", len(pending)))
		for i, m := range pending {
			b.WriteString(fmt.Sprintf("\n=== message %d of %d: id=%s from=%s subject=%q ===\n", i+1, len(pending), m.ID, m.From, m.Subject))
			b.WriteString(Text(m))
		}
	}
	
	return b.String()
}

// findNewestCairn finds the newest cairn or status file in the directory.
func findNewestCairn(dir string) (path string, content string, err error) {
	var newest os.FileInfo
	var newestPath string
	
	err = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		name := info.Name()
		if strings.HasPrefix(name, "cairn") && strings.HasSuffix(name, ".md") {
			if newest == nil || info.ModTime().After(newest.ModTime()) {
				newest = info
				newestPath = path
			}
		}
		if name == "STATUS.md" {
			if newest == nil || info.ModTime().After(newest.ModTime()) {
				newest = info
				newestPath = path
			}
		}
		return nil
	})
	
	if err != nil || newestPath == "" {
		return "", "", nil
	}
	
	// Check size limit (32 KiB)
	if newest.Size() > 32*1024 {
		return "", "", fmt.Errorf("file too large: %d bytes", newest.Size())
	}
	
	contentBytes, err := os.ReadFile(newestPath)
	if err != nil {
		return "", "", err
	}
	content = string(contentBytes)
	
	return newestPath, string(content), nil
}

// readQueue reads the queue file contents.
func readQueue(dir string) (string, error) {
	path := filepath.Join(dir, QueueFile)
	content, err := os.ReadFile(path)
	if err != nil {
		return "", nil // Queue not found is okay
	}
	return string(content), nil
}

// writeRecoveryStatus writes recovery information to status.
func writeRecoveryStatus(s *Status, recovery *SessionRecovery) {
	if recovery == nil {
		return
	}
	s.SessionRecovery = "recovered"
	s.SessionRecoveryFrom = recovery.OldSessionID
	s.SessionRecoveryTo = recovery.NewSessionID
	s.SessionRecoveryReason = recovery.Reason
	s.SessionRecoveryAt = recovery.At
}

// tellRecovery sends the coordinator one message about session recovery.
func tellRecovery(ctx context.Context, b *bus.Bus, friend, seat, oldID, newID, reason string) error {
	to := seat
	if to == "" {
		to = "coordinator" // fallback
	}
	
	subject := fmt.Sprintf("friend %s: session %s replaced by %s: %s", friend, oldID, newID, reason)
	body := subject + "\n"
	
	_, err := b.Send(ctx, bus.Message{From: friend, To: []string{to}, Subject: subject, Body: body})
	return err
}

// Adapter interface extension for recovery detection.
type RecoveryDetector interface {
	DetectRecovery(output string, turns int) (reason string, shouldRecover bool)
}
