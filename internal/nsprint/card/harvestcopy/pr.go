package harvestcopy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// TitleMax is how much of a DONE-WHEN a title made from one carries
// (the retired card harvest's TitleMax, kept here).
const TitleMax = 70

// pull is the part of GitHub's pull object the harvest reads.
type pull struct {
	Number  int    `json:"number"`
	HTMLURL string `json:"html_url"`
	State   string `json:"state"`
	Head    struct {
		SHA string `json:"sha"`
		Ref string `json:"ref"`
	} `json:"head"`
	open string
}

// openPR finds the open PR whose head is owner:branch, else opens one
// (POST /repos/<full>/pulls). GitHub's 422 "A pull request already exists"
// after a lost reply is one more lookup, never a second PR.
func openPR(ctx context.Context, req Request, full, owner string) (pull, error) {
	if p, ok, err := findOpenPR(ctx, req, full, owner); err != nil {
		return pull{}, err
	} else if ok {
		p.open = "already"
		return p, nil
	}
	body := map[string]string{"title": prTitle(req), "head": req.Branch, "base": req.Base, "body": PRBody(req)}
	var p pull
	code, err := rest(ctx, req, http.MethodPost, "/repos/"+full+"/pulls", body, &p)
	if err != nil {
		if code == http.StatusUnprocessableEntity && strings.Contains(strings.ToLower(err.Error()), "already exists") {
			if p, ok, err2 := findOpenPR(ctx, req, full, owner); err2 == nil && ok {
				p.open = "already"
				return p, nil
			}
		}
		return pull{}, fmt.Errorf("%w: %v", ErrPRRefused, err)
	}
	if p.Number == 0 {
		return pull{}, fmt.Errorf("%w: open PR on %s: the reply names no number", ErrPRRefused, req.Branch)
	}
	p.open = "opened"
	return p, nil
}

func findOpenPR(ctx context.Context, req Request, full, owner string) (pull, bool, error) {
	q := url.Values{"state": {"open"}, "head": {owner + ":" + req.Branch}}
	var pulls []pull
	if _, err := rest(ctx, req, http.MethodGet, "/repos/"+full+"/pulls?"+q.Encode(), nil, &pulls); err != nil {
		return pull{}, false, fmt.Errorf("%w: %v", ErrPRRefused, err)
	}
	for _, p := range pulls {
		if p.Head.Ref == req.Branch {
			return p, true, nil
		}
	}
	return pull{}, false, nil
}

// prTitle is the primary's title, else its DONE-WHEN's first TitleMax
// characters, else the branch.
func prTitle(req Request) string {
	if t := oneLine(req.Title); t != "" {
		return t
	}
	s := oneLine(req.DoneWhen)
	if r := []rune(s); len(r) > TitleMax {
		s = strings.TrimSpace(string(r[:TitleMax]))
	}
	if s != "" {
		return s
	}
	return req.Branch
}

// PRBody is the PR's body: the typed lines the lander and the readers read
// (STREAM, ORIGIN, DONE-WHEN) and the Claude Code line last.
func PRBody(req Request) string {
	var b strings.Builder
	line := func(k, v string) {
		if v = oneLine(v); v != "" {
			fmt.Fprintf(&b, "%s: %s\n", k, v)
		}
	}
	line("STREAM", req.Stream)
	line("ORIGIN", req.Origin)
	line("DONE-WHEN", req.DoneWhen)
	b.WriteString("\n" + ClaudeLine + "\n")
	return b.String()
}

// rest is one REST call: JSON in, JSON out, a bearer token, the GitHub
// headers. A non-2xx reply is an error carrying the status and GitHub's
// message; the status is returned beside it.
func rest(ctx context.Context, req Request, method, path string, body any, out any) (int, error) {
	api := strings.TrimRight(req.API, "/")
	if api == "" {
		api = DefaultAPI
	}
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, err
		}
		rd = bytes.NewReader(b)
	}
	r, err := http.NewRequestWithContext(ctx, method, api+path, rd)
	if err != nil {
		return 0, err
	}
	r.Header.Set("Accept", "application/vnd.github+json")
	r.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	r.Header.Set("Authorization", "Bearer "+req.Token)
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	hc := req.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := hc.Do(r)
	if err != nil {
		return 0, fmt.Errorf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := oneLine(string(b))
		var e struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(b, &e) == nil && e.Message != "" {
			msg = e.Message
		}
		return resp.StatusCode, fmt.Errorf("%s %s: HTTP %d: %s", method, path, resp.StatusCode, msg)
	}
	if out != nil && len(bytes.TrimSpace(b)) > 0 {
		if err := json.Unmarshal(b, out); err != nil {
			return resp.StatusCode, fmt.Errorf("%s %s: reply: %v", method, path, err)
		}
	}
	return resp.StatusCode, nil
}
