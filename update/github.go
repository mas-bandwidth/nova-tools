package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// apiBase is the GitHub API; a test may point it elsewhere.
var apiBase = "https://api.github.com"

type ghRelease struct {
	TagName string `json:"tag_name"`
	Assets  []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
	} `json:"assets"`
	client *http.Client
}

func (r *ghRelease) Tag() string { return r.TagName }

func (r *ghRelease) Sums(ctx context.Context) ([]byte, error) {
	body, err := r.Open(ctx, sumsFile)
	if err != nil {
		return nil, err
	}
	defer func() { _ = body.Close() }() // ignored: read side
	return io.ReadAll(io.LimitReader(body, 1<<20))
}

func (r *ghRelease) Open(ctx context.Context, asset string) (io.ReadCloser, error) {
	for _, a := range r.Assets {
		if a.Name == asset {
			return get(ctx, r.client, a.URL, "")
		}
	}
	return nil, fmt.Errorf("the release %s has no asset %s", r.TagName, asset)
}

// githubForge reads releases from the GitHub API, with the token from GITHUB_TOKEN or
// GH_TOKEN when one is set. Downloads send no token.
func githubForge(getenv func(string) string) Forge {
	return func(ctx context.Context, repo, tag string) (Release, error) {
		client := &http.Client{Timeout: 10 * time.Minute}
		path := apiBase + "/repos/" + repo + "/releases/latest"
		if tag != "" {
			path = apiBase + "/repos/" + repo + "/releases/tags/" + url.PathEscape(tag)
		}
		token := getenv("GITHUB_TOKEN")
		if token == "" {
			token = getenv("GH_TOKEN")
		}
		body, err := get(ctx, client, path, token)
		if err != nil {
			return nil, err
		}
		defer func() { _ = body.Close() }() // ignored: read side
		rel := &ghRelease{client: client}
		if err := json.NewDecoder(io.LimitReader(body, 8<<20)).Decode(rel); err != nil {
			return nil, fmt.Errorf("the release of %s is not JSON: %w", repo, err)
		}
		if rel.TagName == "" {
			return nil, fmt.Errorf("the release of %s names no tag", repo)
		}
		return rel, nil
	}
}

func get(ctx context.Context, client *http.Client, u, token string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Accept", "application/vnd.github+json")
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close() // ignored: the status is the answer
		return nil, fmt.Errorf("GET %s: %s", req.URL.Redacted(), resp.Status)
	}
	return resp.Body, nil
}
