package dshdesktop

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ValidateDirectory checks the pathname grammar used in the closed saved-event
// notice. Filesystem ownership is checked by the saving client, as it is for
// the sibling Codex receiver.
func ValidateDirectory(directory string) error {
	if !filepath.IsAbs(directory) || filepath.Clean(directory) != directory || len(directory) > 1024 {
		return fmt.Errorf("event directory wants a clean absolute path of at most 1024 bytes")
	}
	for _, c := range directory {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '/' || c == '-' || c == '_' || c == '.' || c == ' ' {
			continue
		}
		return fmt.Errorf("event directory wants only letters, digits, spaces, slash, dot, underscore and hyphen")
	}
	return nil
}

// SavedPath is the only local file a saved-event notice may name. No message
// content, subject, nonce, or caller-authored instruction enters native text.
func SavedPath(directory, eventID string) (string, error) {
	if err := ValidateDirectory(directory); err != nil {
		return "", err
	}
	if !safeID(eventID) {
		return "", fmt.Errorf("event id is invalid")
	}
	return filepath.Join(directory, eventID+".json"), nil
}

// hookConfig is the Claude Code hook shape the harness's bridge reads. The
// PostToolUse hook surfaces the pending notice after every tool call; the Stop
// hook forces another step when a notice is still pending at the turn boundary.
type hookConfig struct {
	Hooks struct {
		PostToolUse []struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Type    string `json:"type"`
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"PostToolUse"`
		Stop []struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Type    string `json:"type"`
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"Stop"`
	} `json:"hooks"`
}

// Install writes the harness-side hook config that points the PostToolUse and
// Stop command hooks at the pending notice. It writes nothing else.
func Install(r Route) error {
	if err := ValidateRoute(r); err != nil {
		return err
	}
	pendingPath, err := PendingPath(r.EventsDir)
	if err != nil {
		return err
	}
	cfg := hookConfig{}
	cfg.Hooks.PostToolUse = []struct {
		Matcher string `json:"matcher"`
		Hooks   []struct {
			Type    string `json:"type"`
			Command string `json:"command"`
		} `json:"hooks"`
	}{
		{Matcher: "*", Hooks: []struct {
			Type    string `json:"type"`
			Command string `json:"command"`
		}{{Type: "command", Command: "cat " + pendingPath}}},
	}
	stopCmd := "test -s " + pendingPath + " && printf '{\"continue\":false,\"stopReason\":\"pending bus messages\"}'"
	cfg.Hooks.Stop = []struct {
		Matcher string `json:"matcher"`
		Hooks   []struct {
			Type    string `json:"type"`
			Command string `json:"command"`
		} `json:"hooks"`
	}{
		{Matcher: "", Hooks: []struct {
			Type    string `json:"type"`
			Command string `json:"command"`
		}{{Type: "command", Command: stopCmd}}},
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(r.HooksFile, data, 0600); err != nil {
		return fmt.Errorf("write hook config: %w", err)
	}
	return nil
}

// ErrPreSend means no pending notice was written.
var ErrPreSend = errors.New("dsh desktop delivery failed before notice write")

// ErrRejected is kept so the hook route and the Codex route share the one
// delivery error taxonomy; a file write has no matched rejection.
var ErrRejected = errors.New("dsh desktop delivery rejected")

// ErrOutcomeUnknown is kept so the hook route and the Codex route share the one
// delivery error taxonomy; a file write has no ambiguous outcome.
var ErrOutcomeUnknown = errors.New("dsh desktop delivery outcome unknown")
