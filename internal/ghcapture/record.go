package ghcapture

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// QueryFunc runs one read-only GitHub GraphQL query with its variables and
// returns the raw response body. It is the recorder's only door to GitHub; the
// adapter (Open) never calls it.
type QueryFunc func(query string, vars map[string]string) ([]byte, error)

// pageQuery is the pinned field list, one page of issues per call. Everything
// the bundle keeps comes from here; a field not asked for is not captured.
const pageQuery = `query($owner:String!,$repo:String!,$n:Int!,$after:String){
  repository(owner:$owner,name:$repo){
    issues(first:$n,after:$after,orderBy:{field:CREATED_AT,direction:ASC},states:[OPEN,CLOSED]){
      totalCount
      pageInfo{hasNextPage endCursor}
      nodes{
        id number url title state createdAt updatedAt body
        author{login}
        labels(first:50){totalCount nodes{name}}
        comments(first:100){totalCount nodes{id url body createdAt updatedAt author{login}}}
        timelineItems(first:50,itemTypes:[CROSS_REFERENCED_EVENT]){totalCount nodes{
          ... on CrossReferencedEvent{source{__typename
            ... on Issue{url number repository{nameWithOwner}}
            ... on PullRequest{url number repository{nameWithOwner}}}}}}
      }
    }
  }
}`

// GhQuery is the production QueryFunc: `gh api graphql`, read-only. It refuses
// any document that is not a plain query, so the recorder cannot mutate.
func GhQuery(query string, vars map[string]string) ([]byte, error) {
	if err := refuseMutation(query); err != nil {
		return nil, err
	}
	args := []string{"api", "graphql", "-f", "query=" + query}
	for k, v := range vars {
		if _, err := strconv.Atoi(v); err == nil {
			args = append(args, "-F", k+"="+v)
		} else {
			args = append(args, "-f", k+"="+v)
		}
	}
	out, err := exec.Command("gh", args...).Output()
	if err != nil {
		return nil, fmt.Errorf("gh api graphql: %w", err)
	}
	return out, nil
}

func refuseMutation(query string) error {
	q := strings.TrimSpace(query)
	if !strings.HasPrefix(q, "query") && !strings.HasPrefix(q, "{") {
		return fmt.Errorf("ghcapture: refused a GraphQL document that is not a query")
	}
	if strings.Contains(q, "mutation") || strings.Contains(q, "subscription") {
		return fmt.Errorf("ghcapture: refused a GraphQL document naming a mutation or subscription")
	}
	return nil
}

type pageResponse struct {
	Data struct {
		Repository *struct {
			Issues struct {
				TotalCount int `json:"totalCount"`
				PageInfo   struct {
					HasNextPage bool   `json:"hasNextPage"`
					EndCursor   string `json:"endCursor"`
				} `json:"pageInfo"`
				Nodes []json.RawMessage `json:"nodes"`
			} `json:"issues"`
		} `json:"repository"`
	} `json:"data"`
	Errors []json.RawMessage `json:"errors"`
}

// Record fetches every issue of owner/repo, pageSize per page, and writes a
// capture bundle into dir: issues/<n>.json holds each issue's original bytes
// exactly as GitHub sent them, manifest.json the provider, repository, fetch
// time, per-issue revision and digest, the page chain and GitHub's totals.
// maxPages bounds the fetch; stopping early leaves the last page's has_next
// true, which Open refuses as an incomplete capture.
func Record(q QueryFunc, owner, repo string, pageSize, maxPages int, fetchedAt time.Time, dir string) (*Manifest, error) {
	if pageSize < 1 || pageSize > 100 || maxPages < 1 {
		return nil, fmt.Errorf("ghcapture: page size 1..100 and at least one page, got %d/%d", pageSize, maxPages)
	}
	m := &Manifest{Provider: "github", Owner: owner, Repo: repo, FetchedAt: fetchedAt.UTC().Format(time.RFC3339), PageSize: pageSize}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	if err := makeBundleSubdir(filepath.Join(dir, "issues")); err != nil {
		return nil, err
	}
	after := ""
	for page := 1; page <= maxPages; page++ {
		vars := map[string]string{"owner": owner, "repo": repo, "n": strconv.Itoa(pageSize)}
		if after != "" {
			vars["after"] = after
		}
		raw, err := q(pageQuery, vars)
		if err != nil {
			return nil, err
		}
		var pr pageResponse
		if err := json.Unmarshal(raw, &pr); err != nil {
			return nil, fmt.Errorf("ghcapture: page %d: %w", page, err)
		}
		if len(pr.Errors) > 0 || pr.Data.Repository == nil {
			return nil, fmt.Errorf("ghcapture: page %d: GitHub answered with errors or no repository", page)
		}
		is := pr.Data.Repository.Issues
		m.TotalIssues = is.TotalCount
		p := Page{Page: page, After: after, EndCursor: is.PageInfo.EndCursor, HasNext: is.PageInfo.HasNextPage}
		for _, node := range is.Nodes {
			var head struct {
				Number    int    `json:"number"`
				UpdatedAt string `json:"updatedAt"`
			}
			if err := json.Unmarshal(node, &head); err != nil || head.Number == 0 {
				return nil, fmt.Errorf("ghcapture: page %d: an issue without a number", page)
			}
			rel := fmt.Sprintf("issues/%d.json", head.Number)
			if err := writeBundleFile(dir, rel, node); err != nil {
				return nil, err
			}
			sum := sha256.Sum256(node)
			m.Issues = append(m.Issues, ManifestIssue{Number: head.Number, Revision: head.UpdatedAt, File: rel, SHA256: hex.EncodeToString(sum[:]), Bytes: len(node)})
			p.Issues = append(p.Issues, head.Number)
		}
		m.Pages = append(m.Pages, p)
		if !p.HasNext {
			break
		}
		after = p.EndCursor
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	if err := enc.Encode(m); err != nil {
		return nil, err
	}
	if err := writeBundleFile(dir, "manifest.json", buf.Bytes()); err != nil {
		return nil, err
	}
	if last := m.Pages[len(m.Pages)-1]; last.HasNext {
		return m, errors.New("ghcapture: stopped at the page bound with pages left; the bundle is incomplete and Open will refuse it")
	}
	return m, nil
}

// makeBundleSubdir makes p as a real directory, or accepts it if it already is one.
// A symlink (or any other non-directory) at p is refused: following it would
// put the bundle's writes outside the directory the caller chose.
func makeBundleSubdir(p string) error {
	fi, err := os.Lstat(p)
	if errors.Is(err, os.ErrNotExist) {
		return os.Mkdir(p, 0o755)
	}
	if err != nil {
		return err
	}
	if !fi.IsDir() {
		return fmt.Errorf("ghcapture: %s exists and is not a regular directory (%s); refusing to write through it", p, fi.Mode().Type())
	}
	return nil
}

// writeBundleFile writes data to dir/rel without ever following a link at
// the destination. A pre-existing non-regular file (a symlink, a directory, a
// device) is refused. The bytes go to a fresh temporary file in the same
// directory (created exclusively, so it cannot be a link), which is then
// renamed over the destination; rename replaces a link itself rather than its
// target, so a link planted after the check still cannot redirect the write.
func writeBundleFile(dir, rel string, data []byte) error {
	p := filepath.Join(dir, rel)
	if fi, err := os.Lstat(p); err == nil {
		if !fi.Mode().IsRegular() {
			return fmt.Errorf("ghcapture: %s exists and is not a regular file (%s); refusing to write through it", rel, fi.Mode().Type())
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(p), ".ghcapture-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	_, werr := f.Write(data)
	cerr := f.Close()
	if werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = os.Chmod(tmp, 0o644)
	}
	if werr == nil {
		werr = os.Rename(tmp, p)
	}
	if werr != nil {
		os.Remove(tmp)
		return werr
	}
	return nil
}
