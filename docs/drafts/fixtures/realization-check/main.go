// realization-check is a finite proposal witness, not a profile validator or launcher.
// Run from the repo root: go run ./docs/drafts/fixtures/realization-check
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/records"
)

type pair struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}
type paths struct {
	Slot     string `json:"slot"`
	Job      string `json:"job"`
	DataHome string `json:"data_home"`
}
type context struct {
	Kind string `json:"kind"`
	Root string `json:"root"`
}
type fixture struct {
	Schema string `json:"schema"`
	Scope  string `json:"scope"`
	Input  struct {
		Context   context `json:"context"`
		JobID     string  `json:"job_id"`
		Execution struct {
			Path []string `json:"path"`
			Env  []pair   `json:"env"`
		} `json:"execution"`
		SecretName string `json:"secret_env_var"`
	} `json:"input"`
	Vectors []struct {
		Slot  string `json:"slot"`
		Nonce string `json:"reservation_nonce"`
		Paths paths  `json:"paths"`
		Env   []pair `json:"env"`
		Hash  string `json:"env_hash"`
	} `json:"vectors"`
	Control json.RawMessage `json:"control_manifest_shape"`
}

func object(kv ...any) *records.Object {
	o := records.NewObject()
	for i := 0; i < len(kv); i += 2 {
		if err := o.Set(kv[i].(string), kv[i+1]); err != nil {
			panic(err)
		}
	}
	return o
}
func digest(v records.Value) string {
	b, err := records.Canonicalize(v)
	if err != nil {
		panic(err)
	}
	h := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(h[:])
}
func equal(a, b any) bool { x, _ := json.Marshal(a); y, _ := json.Marshal(b); return bytes.Equal(x, y) }
func main() {
	raw, err := os.ReadFile("docs/drafts/fixtures/swarm-realization-vectors.json")
	if err != nil {
		panic(err)
	}
	var f fixture
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err = dec.Decode(&f); err != nil {
		panic(err)
	}
	if f.Schema != "nova.swarm.realization.vectors/1" || len(f.Vectors) != 2 {
		panic("unexpected fixture")
	}
	hashes := map[string]bool{}
	for i, v := range f.Vectors {
		slot := f.Input.Context.Root + "/slots/" + v.Slot + "/worker"
		job := slot + "/jobs/" + f.Input.JobID
		p := paths{slot, job, job + "/data"}
		env := []pair{{"HOME", p.DataHome}, {"XDG_DATA_HOME", p.DataHome}, {"NOVA_SWARM_JOB", p.Job}, {"PATH", strings.Join(f.Input.Execution.Path, ":")}}
		env = append(env, f.Input.Execution.Env...)
		sort.Slice(env, func(i, j int) bool { return env[i].Name < env[j].Name })
		if !equal(p, v.Paths) || !equal(env, v.Env) {
			panic("path/environment vector mismatch")
		}
		entries := make([]records.Value, 0, len(env))
		for _, e := range env {
			entries = append(entries, object("name", e.Name, "value", e.Value))
		}
		preimage := object("schema", "nova.swarm.realization/1", "context", object("kind", f.Input.Context.Kind, "root", f.Input.Context.Root), "job_id", f.Input.JobID, "slot", v.Slot, "reservation_nonce", v.Nonce, "paths", object("slot", p.Slot, "job", p.Job, "data_home", p.DataHome), "env", entries, "secret_env_var", f.Input.SecretName)
		got := digest(preimage)
		if got != v.Hash || hashes[got] {
			panic("hash vector mismatch")
		}
		hashes[got] = true
		fmt.Printf("VECTOR OK index=%d env_hash=%s\n", i, got)
	}
	fmt.Println("LIMIT finite path/environment encoding checks only; no filesystem, admission, reservation, secret, worker, provider or accounting verification")
}
