package dshdesktop

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/mas-bandwidth/nova-tools/pkg/atomicfile"
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

//go:embed receiver.mjs
var receiverSource []byte

//go:embed plugin.mjs
var pluginSource []byte

// Install writes a native module and its Cordis include configuration. It does
// not mount the plugin or mutate a running harness profile. The operator must
// include HooksFile in that profile before claiming native delivery.
func Install(r Route) error {
	if err := ValidateRoute(r); err != nil {
		return err
	}
	directory := filepath.Dir(r.HooksFile)
	core := filepath.Join(directory, "nova-hook-dsh-receiver.mjs")
	entry := filepath.Join(directory, "nova-hook-dsh-plugin.mjs")
	for path, data := range map[string][]byte{core: receiverSource, entry: pluginSource} {
		if err := atomicfile.Write(path, data, 0600); err != nil {
			return fmt.Errorf("write native module: %w", err)
		}
	}
	cfg := []map[string]any{{"id": "nova-hook-dsh", "name": entry, "config": map[string]string{"eventsDir": r.EventsDir, "sessionId": r.SessionID}}}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.Write(r.HooksFile, data, 0600)
}

// ErrPreSend means no notification was published by this attempt.
var ErrPreSend = errors.New("dsh desktop delivery failed before notice publication")

// ErrRejected denotes an explicit native rejection; queue acceptance alone
// cannot establish native admission or rejection.
var ErrRejected = errors.New("dsh desktop delivery rejected")

// ErrOutcomeUnknown means publication may already have become visible. Inspect
// saved, queued and claimed records before retry; never infer model proof.
var ErrOutcomeUnknown = errors.New("dsh desktop delivery outcome unknown")
