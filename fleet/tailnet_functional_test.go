//go:build functional

package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The real client against a local stand-in of the Tailscale API: every request with the API access token as Bearer auth, the policy read and written as HuJSON (its comments kept) with If-Match, a key minted.
func TestTailnetHTTP(t *testing.T) {
	var log []string
	doc, etag := "// the human's own note\n{\"acls\": [{\"action\": \"accept\", \"src\": [\"*\"], \"dst\": [\"*:*\"]}], // allow all\n}\n", `"v1"`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		log = append(log, r.Method+" "+r.URL.Path)
		if r.Header.Get("Authorization") != "Bearer tskey-api-k-S" {
			w.WriteHeader(401)
			return
		}
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v2/tailnet/-/acl":
			if r.Header.Get("Accept") != "application/hujson" {
				w.WriteHeader(406)
				return
			}
			w.Header().Set("ETag", etag)
			_, _ = io.WriteString(w, doc)
		case "POST /api/v2/tailnet/-/acl":
			if r.Header.Get("If-Match") != etag || r.Header.Get("Content-Type") != "application/hujson" {
				w.WriteHeader(412)
				return
			}
			doc, etag = string(body), `"v2"`
			_, _ = w.Write(body)
		case "POST /api/v2/tailnet/-/keys":
			var req struct {
				Capabilities struct {
					Devices struct{ Create struct{ Tags []string } }
				}
			}
			_ = json.Unmarshal(body, &req)
			_, _ = io.WriteString(w, `{"id":"k1","key":"tskey-auth-k1-`+strings.Join(req.Capabilities.Devices.Create.Tags, ",")+`"}`)
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	old := tsBase
	t.Cleanup(func() { tsBase = old })
	tsBase = srv.URL
	api := newTailnet("tskey-api-k-S")
	if got, err := policy(api, false); err != nil || got != "merged" {
		t.Fatalf("policy = %q %v (%v)", got, err, log)
	}
	if got, err := policy(api, false); err != nil || got != "unchanged" {
		t.Fatalf("second policy = %q %v", got, err)
	}
	if !strings.HasPrefix(doc, "// the human's own note\n{\"acls\": [{\"action\": \"accept\", \"src\": [\"*\"], \"dst\": [\"*:*\"]}], // allow all\n") || !strings.Contains(doc, `"tag:control":["autogroup:admin"]`) {
		t.Errorf("policy written: %s", doc)
	}
	if k, err := mintKey(api, []string{"tag:nova"}); err != nil || k != "tskey-auth-k1-tag:nova" {
		t.Errorf("mintKey = %q %v", k, err)
	}
	if _, err := policy(newTailnet("tskey-api-wrong"), false); err == nil || !strings.Contains(err.Error(), "HTTP 401: TAILSCALE_API_KEY was refused") || strings.Contains(err.Error(), "wrong") {
		t.Errorf("a refused token: %v", err)
	}
}
