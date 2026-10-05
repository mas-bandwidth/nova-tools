package main

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Ollama is the ollama adapter: one daemon serving many installed models. A model is a
// tag; serve makes a derived tag with the context, temperature 0 and the seed baked in
// (rule 6), because a /v1 caller sends no num_ctx and a model "configured" in place
// reloads at the daemon's small default.
type Ollama struct{}

// OllamaPort is the port the ollama daemon listens on unless its operator moved it.
const OllamaPort = "11434"

func (Ollama) Name() string        { return "ollama" }
func (Ollama) DefaultBase() string { return "http://127.0.0.1:" + OllamaPort + "/v1" }
func (Ollama) Provider() string    { return "ollama" }

// Health is GET /api/tags on the daemon's root.
func (Ollama) Health(ctx context.Context, c Client) (int, error) {
	return c.call(ctx, http.MethodGet, "/api/tags", nil, nil)
}

type ollamaModel struct {
	Name          string `json:"name"`
	Model         string `json:"model"`
	Size          int64  `json:"size"`
	Digest        string `json:"digest"`
	ContextLength int    `json:"context_length"`
}

type ollamaList struct {
	Models []ollamaModel `json:"models"`
}

func (m ollamaModel) ref() string {
	if m.Name != "" {
		return m.Name
	}
	return m.Model
}

// Models is /api/tags, with /api/ps saying which are loaded and at what context (rule
// 5: num_ctx is the context_length a loaded model reports, nothing otherwise).
func (Ollama) Models(ctx context.Context, c Client) ([]Model, error) {
	var tags, ps ollamaList
	if _, err := c.call(ctx, http.MethodGet, "/api/tags", nil, &tags); err != nil {
		return nil, err
	}
	if _, err := c.call(ctx, http.MethodGet, "/api/ps", nil, &ps); err != nil {
		return nil, err
	}
	loaded := map[string]int{}
	for _, m := range ps.Models {
		loaded[m.ref()] = m.ContextLength
	}
	out := make([]Model, 0, len(tags.Models))
	for _, m := range tags.Models {
		n, on := loaded[m.ref()]
		out = append(out, Model{Ref: m.ref(), Digest: digestOf(m.Digest), Weights: m.Size, Loaded: on, NumCtx: n})
	}
	return out, nil
}

// digestOf is the manifest digest as printed: sha256:<hex>, "" when none was reported.
func digestOf(d string) string {
	if d == "" || strings.Contains(d, ":") {
		return d
	}
	return "sha256:" + d
}

type ollamaShow struct {
	Modelfile  string `json:"modelfile"`
	Parameters string `json:"parameters"`
	Details    struct {
		ParentModel string `json:"parent_model"`
	} `json:"details"`
}

func (Ollama) show(ctx context.Context, c Client, model string) (ollamaShow, error) {
	var s ollamaShow
	_, err := c.call(ctx, http.MethodPost, "/api/show", map[string]string{"model": model}, &s)
	return s, err
}

// params is /api/show's parameters text: one name and value per line.
func (s ollamaShow) params() map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(s.Parameters, "\n") {
		if f := strings.Fields(line); len(f) >= 2 {
			out[f[0]] = strings.Trim(strings.Join(f[1:], " "), `"`)
		}
	}
	return out
}

// DerivedTag is the tag serve makes for ref at numCtx (rule 6): the reference with its
// :<tag> and any registry prefix stripped, then -<numCtx/1024>k, so a shared name derives
// one tag and a collision is caught by the parent, not the name.
func DerivedTag(ref string, numCtx int) string {
	name := ref
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	if i := strings.Index(name, ":"); i >= 0 {
		name = name[:i]
	}
	return fmt.Sprintf("%s-%dk", name, numCtx/1024)
}

// ContextProblem is why numCtx cannot name a derived tag, "" when it can: a tag whose
// name rounds lies about the context it holds, so a remainder names the two nearest
// multiples of 1024.
func ContextProblem(numCtx int) string {
	switch {
	case numCtx <= 0:
		return fmt.Sprintf("--num-ctx wants the context in tokens, above 0 (got %d); refusing to guess", numCtx)
	case numCtx%1024 != 0:
		lo := numCtx / 1024 * 1024
		return fmt.Sprintf("--num-ctx %d is not a multiple of 1024, so the derived tag's name would round; use %d or %d", numCtx, lo, lo+1024)
	}
	return ""
}

// withTag is a reference with :latest when it names no tag, the spelling ollama reports.
func withTag(ref string) string {
	if strings.Contains(ref[strings.LastIndex(ref, "/")+1:], ":") {
		return ref
	}
	return ref + ":latest"
}

// Serve makes the derived tag (or uses the one there when its parent, num_ctx,
// temperature and seed match: only those four, because /api/show also reports inherited
// fields this tool never set), then sends one warm-up with keep_alive, timed on now (rule 7).
func (o Ollama) Serve(ctx context.Context, c Client, req ServeRequest, now func() time.Time) (Served, error) {
	tag := DerivedTag(req.Model, req.NumCtx)
	models, err := o.Models(ctx, c)
	if err != nil {
		return Served{}, err
	}
	var parent *Model
	exists := false
	for i, m := range models {
		if withTag(m.Ref) == withTag(req.Model) {
			parent = &models[i]
		}
		exists = exists || withTag(m.Ref) == withTag(tag)
	}
	if parent == nil {
		return Served{}, &Refusal{Code: 1, Why: fmt.Sprintf("the engine ollama does not have %s", req.Model), Remedy: "ollama pull " + req.Model}
	}
	s := Served{ServeAs: tag, Digest: parent.Digest, Temperature: "0", Seed: req.Seed}
	if exists {
		show, err := o.show(ctx, c, tag)
		if err != nil {
			return Served{}, err
		}
		p := show.params()
		have := map[string]string{"parent": show.Details.ParentModel, "num_ctx": p["num_ctx"], "temperature": p["temperature"], "seed": p["seed"]}
		want := map[string]string{"parent": req.Model, "num_ctx": strconv.Itoa(req.NumCtx), "temperature": "0", "seed": req.Seed}
		for _, k := range []string{"parent", "num_ctx", "temperature", "seed"} {
			h, w := have[k], want[k]
			if k == "parent" {
				h, w = withTag(h), withTag(w)
			}
			if h != w {
				return Served{}, &Refusal{Code: 1, Why: fmt.Sprintf("the tag %s exists with %s %s, and this serve asks %s", tag, k, orUnset(have[k]), orUnset(want[k])), Remedy: "ollama rm " + tag}
			}
		}
	}
	if req.DryRun {
		s.Created = !exists
		return s, nil
	}
	if !exists {
		params := map[string]any{"num_ctx": req.NumCtx, "temperature": 0}
		if req.Seed != "" {
			n, _ := strconv.Atoi(req.Seed) // ignored: serve's Check refused a seed that is no whole number
			params["seed"] = n
		}
		if _, err := c.call(ctx, http.MethodPost, "/api/create", map[string]any{"model": tag, "from": req.Model, "parameters": params, "stream": false}, nil); err != nil {
			return Served{}, err
		}
		s.Created = true
	}
	start := now()
	if _, err := c.call(ctx, http.MethodPost, "/api/generate", map[string]any{"model": tag, "prompt": "", "keep_alive": req.KeepAlive, "stream": false}, nil); err != nil {
		return Served{}, err
	}
	s.Load = now().Sub(start)
	return s, nil
}

func orUnset(v string) string {
	if v == "" {
		return "unset"
	}
	return v
}

// Stop sends one request with keep_alive 0; the tag stays on disk. A model /api/ps does
// not list is already stopped.
func (o Ollama) Stop(ctx context.Context, c Client, model string) (bool, error) {
	models, err := o.Models(ctx, c)
	if err != nil {
		return false, err
	}
	for _, m := range models {
		if withTag(m.Ref) == withTag(model) && m.Loaded {
			_, err := c.call(ctx, http.MethodPost, "/api/generate", map[string]any{"model": model, "keep_alive": 0, "stream": false}, nil)
			return false, err
		}
	}
	return true, nil
}

// Store is the parent of the blobs directory /api/show's FROM line names (rule 15).
func (o Ollama) Store(ctx context.Context, c Client, model string) (string, string) {
	if model == "" {
		return "", "no tag to show"
	}
	s, err := o.show(ctx, c, model)
	if err != nil {
		return "", err.Error()
	}
	for _, line := range strings.Split(s.Modelfile, "\n") {
		from, ok := strings.CutPrefix(strings.TrimSpace(line), "FROM ")
		if !ok {
			continue
		}
		from = strings.TrimSpace(from)
		if dir := filepath.Dir(from); filepath.IsAbs(from) && filepath.Base(dir) == "blobs" {
			return filepath.Dir(dir), ""
		}
	}
	return "", "the modelfile names no blob path"
}
