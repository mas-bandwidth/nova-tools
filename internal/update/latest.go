package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const HTTPCap = 256 * 1024

func defaultClient() *http.Client {
	return &http.Client{CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) > 3 {
			return fmt.Errorf("redirect limit")
		}
		return nil
	}}
}

// Latest reads only the declared endpoint, without credentials or persistent cache.
func Latest(ctx context.Context, e Entry, timeout time.Duration, client *http.Client) Read {
	r := Read{Source: e.Latest, Remedy: "check the declared latest source or ask again when it answers"}
	if ctx.Err() != nil {
		r.Reason = "budget"
		r.Remedy = "increase --budget"
		return r
	}
	child, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	scheme, loc, _ := strings.Cut(e.Latest, ":")
	if scheme == "local" {
		a, _ := argv(loc)
		p := process(child, a, nil, ChildCap)
		raw := p.Stdout
		if raw == "" {
			raw = p.Stderr
		}
		r = identity(e, raw, false)
		r.Source = e.Latest
		r.Path = p.Path
		if p.Reason != "" {
			r.Reason = p.Reason
			r.Remedy = "repair the declared local version command"
			if p.Reason == "output_not_closed" {
				r.Remedy = leakRemedy
			}
		}
		return r
	}
	var endpoint string
	switch scheme {
	case "github":
		parts := strings.Split(loc, "/")
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			r.Reason = "invalid github locator"
			return r
		}
		endpoint = "https://api.github.com/repos/" + url.PathEscape(parts[0]) + "/" + url.PathEscape(parts[1]) + "/releases/latest"
	case "npm":
		endpoint = "https://registry.npmjs.org/" + url.PathEscape(loc) + "/latest"
	case "brew":
		endpoint = "https://formulae.brew.sh/api/formula/" + url.PathEscape(loc) + ".json"
	case "ollama":
		model, tag, ok := strings.Cut(loc, ":")
		if !ok || model == "" || tag == "" {
			r.Reason = "model tag required"
			r.Remedy = "name ollama:model:tag"
			return r
		}
		endpoint = "https://registry.ollama.ai/v2/library/" + url.PathEscape(model) + "/manifests/" + url.PathEscape(tag)
	default:
		r.Reason = "unsupported source"
		return r
	}
	if client == nil {
		client = defaultClient()
	}
	fetch := func(endpoint string) ([]byte, int, error) {
		req, err := http.NewRequestWithContext(child, http.MethodGet, endpoint, nil)
		if err != nil {
			return nil, 0, err
		}
		req.Header.Set("Accept", "application/json")
		response, err := client.Do(req)
		if err != nil {
			return nil, 0, err
		}
		defer response.Body.Close()
		if response.StatusCode != 200 {
			if response.StatusCode == 403 || response.StatusCode == 429 {
				r.Remedy = "ask again at x-ratelimit-reset=" + response.Header.Get("x-ratelimit-reset")
			}
			return nil, response.StatusCode, nil
		}
		b, err := io.ReadAll(io.LimitReader(response.Body, HTTPCap))
		if len(b) >= HTTPCap {
			return nil, 200, fmt.Errorf("output")
		}
		return b, 200, err
	}
	b, status, err := fetch(endpoint)
	tags := false
	if scheme == "github" && status == 404 && err == nil {
		endpoint = strings.TrimSuffix(endpoint, "releases/latest") + "tags?per_page=1"
		b, status, err = fetch(endpoint)
		tags = true
		r.Source = e.Latest + "@" + endpoint
	}
	if err != nil {
		r.Reason = "network"
		if child.Err() != nil {
			r.Reason = "timeout"
		} else if err.Error() == "output" {
			r.Reason = "output"
		}
		return r
	}
	if status != 200 {
		r.Reason = fmt.Sprintf("HTTP %d", status)
		if scheme == "ollama" && status == 404 {
			r.Reason = "tag_not_found"
			model, _, _ := strings.Cut(loc, ":")
			r.Remedy = "check https://ollama.com/library/" + model + "/tags"
		}
		return r
	}
	var v string
	if tags {
		var a []struct {
			Name string `json:"name"`
		}
		if json.Unmarshal(b, &a) != nil {
			r.Reason = "shape"
			return r
		}
		if len(a) == 0 {
			r.Reason = "no release and no tag"
			return r
		}
		v = a[0].Name
	} else {
		var a map[string]json.RawMessage
		if json.Unmarshal(b, &a) != nil {
			r.Reason = "shape"
			return r
		}
		switch scheme {
		case "github":
			_ = json.Unmarshal(a["tag_name"], &v)
		case "npm":
			_ = json.Unmarshal(a["version"], &v)
		case "brew":
			var versions map[string]string
			_ = json.Unmarshal(a["versions"], &versions)
			v = versions["stable"]
		case "ollama":
			var schema int
			var layers []json.RawMessage
			if json.Unmarshal(a["schemaVersion"], &schema) != nil || schema <= 0 || json.Unmarshal(a["layers"], &layers) != nil || layers == nil {
				r.Reason = "shape"
				return r
			}
			sum := sha256.Sum256(b)
			r.Version = hex.EncodeToString(sum[:])[:12]
			return r
		}
	}
	key, err := versionKey(v)
	if err != nil {
		r.Reason = "shape"
		return r
	}
	r.Version = key
	return r
}
