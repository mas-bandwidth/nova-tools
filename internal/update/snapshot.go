package update

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type observed struct {
	Raw    string `json:"raw"`
	Status string `json:"status"`
	At     string `json:"at"`
}
type delivery struct {
	Observed map[string]observed `json:"observed"`
	ID       string              `json:"id"`
	At       string              `json:"at"`
}
type pending struct {
	Artifact json.RawMessage     `json:"artifact"`
	Observed map[string]observed `json:"observed"`
	ID       string              `json:"id"`
}
type snapshot struct {
	Observed  map[string]observed `json:"observed"`
	Delivered map[string]delivery `json:"delivered"`
	Pending   map[string]pending  `json:"pending"`
}

func emptySnapshot() *snapshot {
	return &snapshot{map[string]observed{}, map[string]delivery{}, map[string]pending{}}
}
func readSnapshot(path string) (*snapshot, error) {
	s := emptySnapshot()
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return nil, fmt.Errorf("cannot read snapshot (supply a readable --snapshot)")
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err = d.Decode(s); err != nil {
		return nil, fmt.Errorf("invalid snapshot (preserve it and select a valid --snapshot)")
	}
	if d.Decode(new(any)) != io.EOF {
		return nil, fmt.Errorf("trailing snapshot data (preserve it and select a valid --snapshot)")
	}
	if s.Observed == nil || s.Delivered == nil {
		return nil, fmt.Errorf("incomplete snapshot (preserve it and select a valid --snapshot)")
	}
	if s.Pending == nil {
		s.Pending = map[string]pending{}
	}
	return s, nil
}
func writeSnapshot(path string, s *snapshot) error {
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	f, err := os.CreateTemp(filepath.Dir(path), ".nova-version-snapshot-*")
	if err != nil {
		return fmt.Errorf("cannot create snapshot temporary file (create its parent directory)")
	}
	temp := f.Name()
	defer os.Remove(temp)
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(b)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return fmt.Errorf("cannot write snapshot (check space and permissions)")
	}
	if err = os.Rename(temp, path); err != nil {
		return fmt.Errorf("cannot replace snapshot atomically (check destination permissions)")
	}
	return nil
}
func sameObserved(a, b map[string]observed) bool {
	if len(a) != len(b) {
		return false
	}
	for k, x := range a {
		y, ok := b[k]
		if !ok || x.Raw != y.Raw || x.Status != y.Status {
			return false
		}
	}
	return true
}
func snapshotScope(o options) string {
	to := strings.Split(o.to, ",")
	for i := range to {
		to[i] = strings.TrimSpace(to[i])
	}
	sort.Strings(to)
	bus, _ := filepath.Abs(o.bus)
	b, _ := json.Marshal([]string{o.as, strings.Join(to, ","), bus, o.remote, o.branch, o.host})
	return string(b)
}
func lockSnapshot(ctx context.Context, path string) (func(), error) {
	// A stable sibling inode is required because the JSON itself is replaced by
	// rename. The empty lock file survives; only its kernel lock means ownership.
	f, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("cannot open snapshot lock (create the parent directory and check permissions)")
	}
	for {
		ok, err := trySnapshotLock(f)
		if err != nil {
			f.Close()
			return nil, fmt.Errorf("cannot lock snapshot (use a filesystem supporting file locks)")
		}
		if ok {
			return func() { unlockSnapshot(f); f.Close() }, nil
		}
		t := time.NewTimer(10 * time.Millisecond)
		select {
		case <-ctx.Done():
			t.Stop()
			f.Close()
			return nil, fmt.Errorf("snapshot is busy (wait for the current report or increase --budget)")
		case <-t.C:
		}
	}
}

// validatePrepared checks the bus JSON without relying on stdout as a permission
// grant. The bus must additionally validate the artifact before it can mutate.
func validatePrepared(raw []byte) (string, error) {
	var a struct{ Schema, ID, Path, Note, SHA256 string }
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&a) != nil || a.Schema != "nova.bus.prepared/1" || a.ID == "" || a.Path == "" || !strings.HasSuffix(a.Note, "\n") || len(a.SHA256) != 64 {
		return "", fmt.Errorf("bus prepare returned an invalid artifact")
	}
	if d.Decode(new(any)) != io.EOF {
		return "", fmt.Errorf("bus prepare returned trailing data")
	}
	if shaText(a.Note) != a.SHA256 {
		return "", fmt.Errorf("bus prepare digest mismatch")
	}
	return a.ID, nil
}
func confirmed(line, id string) bool {
	f := strings.Fields(line)
	if len(f) < 3 || f[0] != "SEND" || f[1] != "OK" {
		return false
	}
	haveID, pushed := false, false
	for _, v := range f[2:] {
		if v == "id="+id {
			haveID = true
		}
		if v == "pushed=true" {
			pushed = true
		}
	}
	return haveID && pushed
}
func cloneObserved(m map[string]observed) map[string]observed {
	n := map[string]observed{}
	for k, v := range m {
		n[k] = v
	}
	return n
}
