package update

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
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
type snapshot struct {
	Observed  map[string]observed `json:"observed"`
	Delivered map[string]delivery `json:"delivered"`
}

// foldKey renders a key the way the decoder will match it. ASCII letters fold by
// case; anything outside ASCII is refused, because the decoder's fold reaches
// beyond ASCII and a key this tool supports never needs to.
func foldKey(name string) (string, error) {
	var b strings.Builder
	b.Grow(len(name))
	for i := 0; i < len(name); i++ {
		c := name[i]
		if c >= 0x80 {
			return "", fmt.Errorf("a key outside ASCII")
		}
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		b.WriteByte(c)
	}
	return b.String(), nil
}

// maxJSONDepth bounds the reader the same way every other reader here is
// bounded. The shapes this tool decodes are three deep; anything far past that
// is not a snapshot or an artifact, and is refused rather than descended.
const maxJSONDepth = 32

// validateSnapshot walks the snapshot's known shape and refuses exactly the two
// ambiguities a snapshot reader must refuse, without reaching into data it did
// not choose. A typed object -- the snapshot itself, an observed value, a
// and a delivered value --
// names its schema members, so a member must be spelled exactly and a member
// holding a byte outside ASCII is refused (the decoder's fold would otherwise
// let "ſha256" name the digest field), and two members that fold to one name are
// two values for one field. A data map -- `observed` and `delivered`
// -- is keyed by a manifest name or a delivery scope, content this tool did not
// choose: "Tool" and "tool" are distinct tools and "outil-é" is a legal name, so
// there only an exact duplicate key is ambiguity and a lone upper-case or
// Unicode name is preserved, not refused.
func validateSnapshot(raw []byte) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	return walkSnapshot(d)
}

func walkSnapshot(d *json.Decoder) error {
	return walkTyped(d, map[string]func(*json.Decoder) error{
		"observed":  func(d *json.Decoder) error { return walkDataMap(d, walkObservedValue) },
		"delivered": func(d *json.Decoder) error { return walkDataMap(d, walkDeliveryValue) },
	})
}

func walkObservedValue(d *json.Decoder) error {
	return walkTyped(d, map[string]func(*json.Decoder) error{
		"raw":    skipValue,
		"status": skipValue,
		"at":     skipValue,
	})
}

func walkDeliveryValue(d *json.Decoder) error {
	return walkTyped(d, map[string]func(*json.Decoder) error{
		"observed": func(d *json.Decoder) error { return walkDataMap(d, walkObservedValue) },
		"id":       skipValue,
		"at":       skipValue,
	})
}

// walkTyped consumes one object whose members must be spelled exactly as the
// names in dispatch. A member that is not one of those names is refused even
// when it has no duplicate (a lone "Raw" or a Unicode alias is not a second
// spelling of "raw"), and two members that fold to one name are refused.
func walkTyped(d *json.Decoder, dispatch map[string]func(*json.Decoder) error) error {
	open, err := d.Token()
	if err != nil {
		return fmt.Errorf("malformed JSON")
	}
	if delim, ok := open.(json.Delim); !ok || delim != '{' {
		return fmt.Errorf("a schema object is not an object")
	}
	seen := map[string]bool{}
	for {
		k, err := d.Token()
		if err != nil {
			return fmt.Errorf("malformed JSON")
		}
		if end, ok := k.(json.Delim); ok && end == '}' {
			return nil
		}
		name, ok := k.(string)
		if !ok {
			return fmt.Errorf("malformed JSON")
		}
		folded, err := foldKey(name)
		if err != nil {
			return err
		}
		if seen[folded] {
			return fmt.Errorf("two keys naming one field")
		}
		seen[folded] = true
		walk, ok := dispatch[name]
		if !ok {
			return fmt.Errorf("a field is missing or spelled otherwise")
		}
		if err := walk(d); err != nil {
			return err
		}
	}
}

// walkDataMap consumes one object whose keys are names this tool did not choose.
// Case-distinct and non-ASCII keys are legal; only an exact duplicate key is
// ambiguity about an identity.
func walkDataMap(d *json.Decoder, walkValue func(*json.Decoder) error) error {
	open, err := d.Token()
	if err != nil {
		return fmt.Errorf("malformed JSON")
	}
	if delim, ok := open.(json.Delim); !ok || delim != '{' {
		return fmt.Errorf("a data map is not an object")
	}
	seen := map[string]bool{}
	for {
		k, err := d.Token()
		if err != nil {
			return fmt.Errorf("malformed JSON")
		}
		if end, ok := k.(json.Delim); ok && end == '}' {
			return nil
		}
		name, ok := k.(string)
		if !ok {
			return fmt.Errorf("malformed JSON")
		}
		if seen[name] {
			return fmt.Errorf("two entries naming one name")
		}
		seen[name] = true
		if err := walkValue(d); err != nil {
			return err
		}
	}
}

// skipValue consumes one JSON value of any shape without judging its contents;
// the decoder that follows walks it against the typed struct and its depth
// bound is kept here so a maliciously deep value is refused before recursion.
func skipValue(d *json.Decoder) error {
	return skipValueAtDepth(d, 0)
}

func skipValueAtDepth(d *json.Decoder, depth int) error {
	t, err := d.Token()
	if err != nil {
		return fmt.Errorf("malformed JSON")
	}
	return skipAfter(d, t, depth)
}

func skipAfter(d *json.Decoder, t json.Token, depth int) error {
	if depth >= maxJSONDepth {
		return fmt.Errorf("JSON nested deeper than %d", maxJSONDepth)
	}
	delim, ok := t.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '[':
		for {
			v, err := d.Token()
			if err != nil {
				return fmt.Errorf("malformed JSON")
			}
			if end, ok := v.(json.Delim); ok && end == ']' {
				return nil
			}
			if err := skipAfter(d, v, depth+1); err != nil {
				return err
			}
		}
	case '{':
		for {
			k, err := d.Token()
			if err != nil {
				return fmt.Errorf("malformed JSON")
			}
			if end, ok := k.(json.Delim); ok && end == '}' {
				return nil
			}
			if _, ok := k.(string); !ok {
				return fmt.Errorf("malformed JSON")
			}
			v, err := d.Token()
			if err != nil {
				return fmt.Errorf("malformed JSON")
			}
			if err := skipAfter(d, v, depth+1); err != nil {
				return err
			}
		}
	}
	return nil
}

func emptySnapshot() *snapshot {
	return &snapshot{map[string]observed{}, map[string]delivery{}}
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
	if err = validateSnapshot(b); err != nil {
		return nil, fmt.Errorf("invalid snapshot: %s (preserve it and select a valid --snapshot)", err)
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
	return s, nil
}

// writeSnapshot commits with os.Rename, the atomic commit operation.
func writeSnapshot(path string, s *snapshot) error {
	return writeSnapshotWith(path, s, os.Rename)
}

// writeSnapshotWith commits with rename. The command passes Environment.Rename,
// which a helper process sets to hold the real writer before committing; shipped
// commands leave it nil and get os.Rename.
func writeSnapshotWith(path string, s *snapshot, rename func(oldPath, newPath string) error) error {
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	f, err := os.CreateTemp(filepath.Dir(path), snapshotTempPrefix+"*")
	if err != nil {
		return fmt.Errorf("cannot create snapshot temporary file (create its parent directory)")
	}
	temp := f.Name()
	defer func() { _ = os.Remove(temp) }() // ignored: temp file cleanup
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
		return fmt.Errorf("cannot write snapshot (check room and permissions)")
	}
	if err = rename(temp, path); err != nil {
		return fmt.Errorf("cannot replace snapshot atomically (check destination permissions)")
	}
	return nil
}

// snapshotTempPrefix is this tool's own name for its half-written snapshots.
const snapshotTempPrefix = ".nova-version-snapshot-"

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
	b, _ := json.Marshal([]string{o.as, strings.Join(to, ","), o.host})
	return string(b)
}

func cloneObserved(m map[string]observed) map[string]observed {
	n := map[string]observed{}
	for k, v := range m {
		n[k] = v
	}
	return n
}
