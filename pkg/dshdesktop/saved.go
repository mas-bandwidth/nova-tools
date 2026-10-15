package dshdesktop

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mas-bandwidth/nova-tools/pkg/atomicfile"
)

//go:embed receiver.mjs
var receiverSource []byte

//go:embed plugin.mjs
var pluginSource []byte

// Install writes a native module and its Cordis include configuration, and
// points the receiver at r.SessionID through the follow file. It reports
// whether anything the harness loads at start changed (the include or the
// modules): a running harness must then be restarted to load it. Moving the
// follow file to another session needs no restart. Install does not mount the
// include; Mount does.
func Install(r Route) (restart bool, err error) {
	if err := ValidateRoute(r); err != nil {
		return false, err
	}
	directory := filepath.Dir(r.HooksFile)
	core := filepath.Join(directory, "nova-hook-dsh-receiver.mjs")
	entry := filepath.Join(directory, "nova-hook-dsh-plugin.mjs")
	// The include names no session: the receiver follows session.json, so a
	// new session never changes what the harness loaded.
	cfg := []map[string]any{{"id": "nova-hook-dsh", "name": entry, "config": map[string]string{"eventsDir": r.EventsDir}}}
	include, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return false, err
	}
	for _, f := range []struct {
		path string
		data []byte
	}{{core, receiverSource}, {entry, pluginSource}, {r.HooksFile, include}} {
		if old, err := os.ReadFile(f.path); err == nil && bytes.Equal(old, f.data) {
			continue
		}
		restart = true
		if err := writePrivate(f.path, f.data); err != nil {
			return false, fmt.Errorf("write native module: %w", err)
		}
	}
	if _, err := WriteTarget(r.EventsDir, r.SessionID); err != nil {
		return false, fmt.Errorf("write session target: %w", err)
	}
	return restart, nil
}

func writePrivate(path string, data []byte) error { return atomicWrite(path, data, 0600) }

func atomicWrite(path string, data []byte, mode os.FileMode) error {
	return atomicfile.Write(path, data, mode)
}

// ErrPreSend means no notification was published by this attempt.
var ErrPreSend = errors.New("dsh desktop delivery failed before notice publication")

// ErrRejected denotes an explicit native rejection; queue acceptance alone
// cannot establish native admission or rejection.
var ErrRejected = errors.New("dsh desktop delivery rejected")

// ErrOutcomeUnknown means publication may already have become visible. Inspect
// saved, queued and claimed records before retry; never infer model proof.
var ErrOutcomeUnknown = errors.New("dsh desktop delivery outcome unknown")
