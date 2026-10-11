package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/testkit"
)

// fakeOllama is an ollama daemon in a function: /api/tags, /api/ps, /api/show,
// /api/create and /api/generate over maps, answered through an httptest recorder, so no
// test opens a socket. A warm-up advances the fake clock by warm, which is what serve's
// load= must print.
type fakeOllama struct {
	mu        sync.Mutex
	down      bool
	tags      []fakeTag
	loaded    map[string]int // tag -> context_length
	shows     map[string]fakeShow
	creates   []map[string]any
	generates []map[string]any
	clock     time.Time
	warm      time.Duration
	store     string // the blobs' parent every FROM names
}

type fakeTag struct {
	Name   string
	Size   int64
	Digest string
}

type fakeShow struct {
	Parent     string
	Parameters string
}

func newFake(store string) *fakeOllama {
	return &fakeOllama{
		tags:   []fakeTag{{Name: "gemma4:12b", Size: 8_149_190_253, Digest: "c0e0c3e5b4a1"}},
		loaded: map[string]int{}, shows: map[string]fakeShow{}, store: store,
		clock: time.Date(2026, 10, 4, 16, 0, 0, 0, time.UTC), warm: 3 * time.Second,
	}
}

func (f *fakeOllama) now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.clock
}

func (f *fakeOllama) do(req *http.Request) (*http.Response, error) {
	rec := httptest.NewRecorder()
	f.ServeHTTP(rec, req)
	return rec.Result(), nil
}

func (f *fakeOllama) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.down {
		w.WriteHeader(http.StatusBadGateway)
		return
	}
	var in map[string]any
	if r.Body != nil {
		raw, _ := io.ReadAll(r.Body) // ignored: the recorder's body is in memory
		_ = json.Unmarshal(raw, &in) // ignored: a GET has no body
	}
	reply := func(v any) { _ = json.NewEncoder(w).Encode(v) } // ignored: the recorder takes every write
	switch r.URL.Path {
	case "/api/tags":
		var ms []map[string]any
		for _, t := range f.tags {
			ms = append(ms, map[string]any{"name": t.Name, "model": t.Name, "size": t.Size, "digest": t.Digest})
		}
		reply(map[string]any{"models": ms})
	case "/api/ps":
		var ms []map[string]any
		for _, t := range f.tags {
			if n, ok := f.loaded[t.Name]; ok {
				ms = append(ms, map[string]any{"name": t.Name, "model": t.Name, "context_length": n})
			}
		}
		reply(map[string]any{"models": ms})
	case "/api/show":
		name := full(fmt.Sprint(in["model"]))
		s, ok := f.shows[name]
		if !ok && !f.has(name) {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		reply(map[string]any{"modelfile": "FROM " + f.store + "/blobs/sha256-c0e0c3e5b4a1\nPARAMETER num_ctx 4096\n",
			"parameters": s.Parameters, "details": map[string]any{"parent_model": s.Parent}})
	case "/api/create":
		f.creates = append(f.creates, in)
		tag := full(fmt.Sprint(in["model"]))
		params, _ := in["parameters"].(map[string]any)
		var lines []string
		for _, k := range []string{"num_ctx", "seed", "temperature"} {
			if v, ok := params[k]; ok {
				lines = append(lines, fmt.Sprintf("%-30s %v", k, v))
			}
		}
		lines = append(lines, "top_k                          64", "top_p                          0.95")
		f.shows[tag] = fakeShow{Parent: fmt.Sprint(in["from"]), Parameters: strings.Join(lines, "\n")}
		f.tags = append(f.tags, fakeTag{Name: tag, Size: f.tags[0].Size, Digest: "9a8b7c6d5e4f"})
		reply(map[string]any{"status": "success"})
	case "/api/generate":
		f.generates = append(f.generates, in)
		tag := full(fmt.Sprint(in["model"]))
		if ka, ok := in["keep_alive"].(float64); ok && ka == 0 {
			delete(f.loaded, tag)
		} else {
			f.clock = f.clock.Add(f.warm)
			f.loaded[tag] = 2048 // ollama's own default, for a tag made by hand
			for _, line := range strings.Split(f.shows[tag].Parameters, "\n") {
				if w := strings.Fields(line); len(w) == 2 && w[0] == "num_ctx" {
					f.loaded[tag], _ = strconv.Atoi(w[1]) // ignored: the fake wrote the number itself
				}
			}
		}
		reply(map[string]any{"done": true})
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (f *fakeOllama) has(name string) bool {
	for _, t := range f.tags {
		if t.Name == name {
			return true
		}
	}
	return false
}

// full is a tag as ollama lists it: :latest when it names none.
func full(ref string) string {
	if strings.Contains(ref, ":") {
		return ref
	}
	return ref + ":latest"
}

// fakeBox is the box the thresholds are tested against: load1 14.2, 61 GiB free of 128.
func fakeBox() Box {
	return Box{
		Load1:    func() (float64, bool) { return 14.2, true },
		Mem:      func() (uint64, uint64, bool) { return 61 << 30, 128 << 30, true },
		WiredCap: func() (uint64, bool) { return 0, false },
	}
}

// fakeLookup resolves the fleet's names: a tailnet machine and one that is not.
func fakeLookup(host string) ([]string, error) {
	switch host {
	case "gpu-box.test":
		return []string{"100.101.102.103"}, nil
	case "elsewhere.test":
		return []string{"10.0.0.5"}, nil
	}
	return nil, fmt.Errorf("no such host %s", host)
}

// rig is the tool over a fake engine, with the AI root given.
func rig(f *fakeOllama, aiRoot string) testkit.Main {
	w := world{do: f.do, box: fakeBox(), now: f.now, lookup: fakeLookup,
		getenv: func(k string) string { return map[string]string{AIRootEnv: aiRoot}[k] },
		home:   "", stat: os.Stat, adapters: Adapters()}
	return testkit.Main(localTool(w).Run)
}

// firstRunRoot is the AI root of the first run's fixture: a path that does not exist on
// the test machine, so its store resolves as spelled and the transcript reads the same
// everywhere.
const firstRunRoot = "/ai"

// cli is the tool over a fresh fake engine whose weights are in the shared store.
var cli = rig(newFake(firstRunRoot+"/shared/models/ollama"), firstRunRoot)
