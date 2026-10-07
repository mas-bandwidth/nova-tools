package workgh

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"sync"
)

// Replay is a Query over a recorded conversation: dir holds call-NN.json
// files, each {"vars": ..., "reply": ...}, answered in order. A call whose
// variables differ from the next record's, or a call past the last, is an
// error. It is the fixture seam of the tests; it reaches no network.
func Replay(dir string) (Query, error) {
	names, err := filepath.Glob(filepath.Join(dir, "call-*.json"))
	if err != nil {
		return nil, err
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("workgh: no call-*.json under %s", dir)
	}
	sort.Strings(names)
	type rec struct {
		Vars  map[string]any  `json:"vars"`
		Reply json.RawMessage `json:"reply"`
	}
	var recs []rec
	for _, n := range names {
		b, err := os.ReadFile(n)
		if err != nil {
			return nil, err
		}
		var r rec
		if err := json.Unmarshal(b, &r); err != nil {
			return nil, fmt.Errorf("workgh: %s: %v", n, err)
		}
		recs = append(recs, r)
	}
	var mu sync.Mutex
	next := 0
	return func(ctx context.Context, doc string, vars map[string]any) ([]byte, error) {
		if err := RefuseMutation(doc); err != nil {
			return nil, err
		}
		mu.Lock()
		defer mu.Unlock()
		if next >= len(recs) {
			return nil, fmt.Errorf("workgh: replay: call %d past the %d recorded", next+1, len(recs))
		}
		// Compare through JSON so an int and the float a recording decodes
		// to are the same variable.
		var got map[string]any
		b, _ := json.Marshal(vars)
		// ignored: a round trip of a value just marshalled from a map; the comparison below judges it
		_ = json.Unmarshal(b, &got)
		if !reflect.DeepEqual(got, recs[next].Vars) {
			return nil, fmt.Errorf("workgh: replay: call %d asked %v, the recording asked %v", next+1, got, recs[next].Vars)
		}
		r := recs[next].Reply
		next++
		return r, nil
	}, nil
}
