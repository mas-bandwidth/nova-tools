package dshdesktop

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/mas-bandwidth/nova-tools/pkg/atomicfile"
)

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
