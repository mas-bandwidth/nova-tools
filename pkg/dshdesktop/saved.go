package dshdesktop

import (
	"context"
	"errors"
	"fmt"
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

// DeliverSaved attempts a single native delivery of a closed local notice. The
// saved JSON is external data; it is never copied into the native turn prompt.
func DeliverSaved(ctx context.Context, r Route, eventID, directory string) (string, error) {
	if _, err := SavedPath(directory, eventID); err != nil {
		return "", fmt.Errorf("%w: validate route: %v", ErrPreSend, err)
	}
	return Deliver(ctx, r, eventID, BusNotice)
}

// ValidateRoute checks configuration before a hook advertises its lease.
func ValidateRoute(r Route, directory string) error {
	if err := validateInputs(r, "route"); err != nil {
		return err
	}
	if err := ValidateDirectory(directory); err != nil {
		return err
	}
	if r.Cookie == "" {
		return errors.New("cookie is empty")
	}
	return nil
}
