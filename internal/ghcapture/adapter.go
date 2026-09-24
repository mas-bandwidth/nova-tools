package ghcapture

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

// Limits bounds what Open reads. Every read is bounded before it happens; a
// body over MaxBodyBytes is kept as an explicit truncated record, the other
// bounds refuse the capture.
type Limits struct {
	MaxBundleBytes int64 // manifest plus every original
	MaxIssueBytes  int64 // one original
	MaxBodyBytes   int   // one body or comment text kept in full
	MaxIssues      int
}

// DefaultLimits are generous for a real repository and still finite.
func DefaultLimits() Limits {
	return Limits{MaxBundleBytes: 256 << 20, MaxIssueBytes: 8 << 20, MaxBodyBytes: 256 << 10, MaxIssues: 100000}
}

// RefusalError is why a capture was refused. Code is one of: bound, path,
// manifest-total, page-gap, incomplete, digest, identity, revision, parse.
type RefusalError struct {
	Code   string
	Detail string
}

func (e *RefusalError) Error() string { return "ghcapture: refused " + e.Code + ": " + e.Detail }

func refuse(code, format string, a ...any) error {
	return &RefusalError{Code: code, Detail: fmt.Sprintf(format, a...)}
}

// Identity is an issue's stable identity: provider, repository, number, and
// the provider's own node id.
type Identity struct {
	Provider string
	Owner    string
	Repo     string
	Number   int
	NodeID   string
}

// Key is the identity as one string: github:owner/repo#N.
func (id Identity) Key() string {
	return fmt.Sprintf("%s:%s/%s#%d", id.Provider, id.Owner, id.Repo, id.Number)
}

// Text is remote text kept as data: the text (whole, or cut at the bound),
// the full length and digest, and whether it was cut.
type Text struct {
	Text      string
	Bytes     int
	SHA256    string
	Truncated bool
}

// Comment is one issue comment.
type Comment struct {
	ID        string
	URL       string
	Author    string
	CreatedAt string
	UpdatedAt string
	Body      Text
}

// Reference is a cross-reference to this issue from an issue or pull request.
type Reference struct {
	Kind       string // Issue or PullRequest
	Repository string // owner/name
	Number     int
	URL        string
}

// Attachment is an attachment URL found in the body or a comment. The adapter
// never fetches it; it is recorded, never dropped.
type Attachment struct {
	URL     string
	Where   string // "body" or the comment id
	Fetched bool
	Status  string
}

// Gap is a field the capture does not hold in full, kept explicit.
type Gap struct {
	Field  string
	Reason string
}

// Issue is one captured issue as data.
type Issue struct {
	Identity        Identity
	Revision        string
	URL             string
	Title           string
	State           string
	Author          string
	CreatedAt       string
	Body            Text
	Comments        []Comment
	CommentsTotal   int
	Labels          []string
	LabelsTotal     int
	References      []Reference
	ReferencesTotal int
	Attachments     []Attachment
	Gaps            []Gap
	Page            int
	OriginalSHA256  string
}

// Capture is an opened bundle. It has readers only.
type Capture struct {
	man    Manifest
	issues []Issue
	byKey  map[string]int
}

func (c *Capture) Provider() string   { return c.man.Provider }
func (c *Capture) Repository() string { return c.man.Owner + "/" + c.man.Repo }
func (c *Capture) FetchedAt() string  { return c.man.FetchedAt }
func (c *Capture) Pages() int         { return len(c.man.Pages) }

// Issues returns a copy of every issue in page order.
func (c *Capture) Issues() []Issue { return append([]Issue(nil), c.issues...) }

// Issue returns the issue with number n.
func (c *Capture) Issue(n int) (Issue, bool) {
	return c.Lookup(Identity{Provider: c.man.Provider, Owner: c.man.Owner, Repo: c.man.Repo, Number: n}.Key())
}

// Lookup returns the issue with the given identity key.
func (c *Capture) Lookup(key string) (Issue, bool) {
	i, ok := c.byKey[key]
	if !ok {
		return Issue{}, false
	}
	return c.issues[i], true
}

// rawIssue is the pinned field list Record asks for.
type rawIssue struct {
	ID        string  `json:"id"`
	Number    int     `json:"number"`
	URL       string  `json:"url"`
	Title     string  `json:"title"`
	State     string  `json:"state"`
	CreatedAt string  `json:"createdAt"`
	UpdatedAt string  `json:"updatedAt"`
	Body      string  `json:"body"`
	Author    *author `json:"author"`
	Labels    struct {
		TotalCount int `json:"totalCount"`
		Nodes      []struct {
			Name string `json:"name"`
		} `json:"nodes"`
	} `json:"labels"`
	Comments struct {
		TotalCount int `json:"totalCount"`
		Nodes      []struct {
			ID        string  `json:"id"`
			URL       string  `json:"url"`
			Body      string  `json:"body"`
			CreatedAt string  `json:"createdAt"`
			UpdatedAt string  `json:"updatedAt"`
			Author    *author `json:"author"`
		} `json:"nodes"`
	} `json:"comments"`
	TimelineItems struct {
		TotalCount int `json:"totalCount"`
		Nodes      []struct {
			Source struct {
				Typename   string `json:"__typename"`
				URL        string `json:"url"`
				Number     int    `json:"number"`
				Repository struct {
					NameWithOwner string `json:"nameWithOwner"`
				} `json:"repository"`
			} `json:"source"`
		} `json:"nodes"`
	} `json:"timelineItems"`
}

type author struct {
	Login string `json:"login"`
}

func (a *author) login() string {
	if a == nil {
		return ""
	}
	return a.Login
}

// attachmentRe finds GitHub-hosted attachment URLs in remote text.
var attachmentRe = regexp.MustCompile(`https://(?:github\.com/user-attachments/[^\s)"'<>\]]+|(?:user-images|private-user-images|cloud)\.githubusercontent\.com/[^\s)"'<>\]]+|github\.com/[\w.-]+/[\w.-]+/files/[^\s)"'<>\]]+)`)

// Open ingests the capture bundle in dir: it never touches the network. The
// manifest is checked against the bundle's contents before any issue is
// returned; any disagreement refuses the whole capture.
func Open(dir string, lim Limits) (*Capture, error) {
	var budget int64 = lim.MaxBundleBytes
	manBytes, err := readBounded(filepath.Join(dir, "manifest.json"), lim.MaxIssueBytes, &budget)
	if err != nil {
		return nil, err
	}
	var man Manifest
	if err := json.Unmarshal(manBytes, &man); err != nil {
		return nil, refuse("parse", "manifest: %v", err)
	}
	if man.Provider != "github" || man.Owner == "" || man.Repo == "" {
		return nil, refuse("identity", "manifest names provider %q repository %q/%q; this adapter reads github captures", man.Provider, man.Owner, man.Repo)
	}
	if len(man.Issues) > lim.MaxIssues || man.TotalIssues > lim.MaxIssues {
		return nil, refuse("bound", "%d issues over the bound of %d", max(len(man.Issues), man.TotalIssues), lim.MaxIssues)
	}

	// The page chain: numbered from 1, each asked after the cursor the
	// previous one ended at, the last saying no more follow.
	if len(man.Pages) == 0 {
		return nil, refuse("incomplete", "no pages")
	}
	pageOf := map[int]int{}
	prevEnd := ""
	for i, p := range man.Pages {
		if p.Page != i+1 || p.After != prevEnd {
			return nil, refuse("page-gap", "page %d (entry %d) asked after %q, previous page ended at %q", p.Page, i+1, p.After, prevEnd)
		}
		if i < len(man.Pages)-1 && !p.HasNext {
			return nil, refuse("page-gap", "page %d says it is the last but %d pages follow", p.Page, len(man.Pages)-1-i)
		}
		for _, n := range p.Issues {
			if _, dup := pageOf[n]; dup {
				return nil, refuse("manifest-total", "#%d is on more than one page", n)
			}
			pageOf[n] = p.Page
		}
		prevEnd = p.EndCursor
	}
	if man.Pages[len(man.Pages)-1].HasNext {
		return nil, refuse("incomplete", "the last page captured (%d) says more pages follow", len(man.Pages))
	}

	// Totals: what GitHub reported, what the pages hold and what the issue
	// list holds must be one number.
	if len(pageOf) != man.TotalIssues || len(man.Issues) != man.TotalIssues {
		return nil, refuse("manifest-total", "GitHub reported %d issues, pages hold %d, manifest lists %d", man.TotalIssues, len(pageOf), len(man.Issues))
	}

	c := &Capture{man: man, byKey: map[string]int{}}
	for _, mi := range man.Issues {
		page, ok := pageOf[mi.Number]
		if !ok {
			return nil, refuse("manifest-total", "#%d is listed but on no page", mi.Number)
		}
		if want := fmt.Sprintf("issues/%d.json", mi.Number); path.Clean(mi.File) != want {
			return nil, refuse("path", "#%d file %q, want %q", mi.Number, mi.File, want)
		}
		b, err := readBounded(filepath.Join(dir, filepath.FromSlash(mi.File)), lim.MaxIssueBytes, &budget)
		if err != nil {
			return nil, err
		}
		sum := sha256.Sum256(b)
		if hex.EncodeToString(sum[:]) != mi.SHA256 || len(b) != mi.Bytes {
			return nil, refuse("digest", "#%d original does not match its manifest digest", mi.Number)
		}
		is, err := decodeIssue(man, mi, b, lim)
		if err != nil {
			return nil, err
		}
		is.Page = page
		is.OriginalSHA256 = mi.SHA256
		key := is.Identity.Key()
		if _, dup := c.byKey[key]; dup {
			return nil, refuse("manifest-total", "%s listed twice", key)
		}
		c.byKey[key] = len(c.issues)
		c.issues = append(c.issues, is)
	}
	return c, nil
}

func decodeIssue(man Manifest, mi ManifestIssue, b []byte, lim Limits) (Issue, error) {
	var r rawIssue
	if err := json.Unmarshal(b, &r); err != nil {
		return Issue{}, refuse("parse", "#%d: %v", mi.Number, err)
	}
	wantURL := fmt.Sprintf("https://github.com/%s/%s/issues/%d", man.Owner, man.Repo, mi.Number)
	if r.Number != mi.Number || r.URL != wantURL || r.ID == "" {
		return Issue{}, refuse("identity", "#%d original says number %d url %q node %q; want %s", mi.Number, r.Number, r.URL, r.ID, wantURL)
	}
	if r.UpdatedAt == "" || r.UpdatedAt != mi.Revision {
		return Issue{}, refuse("revision", "#%d manifest revision %q, original updatedAt %q", mi.Number, mi.Revision, r.UpdatedAt)
	}
	is := Issue{
		Identity:  Identity{Provider: man.Provider, Owner: man.Owner, Repo: man.Repo, Number: r.Number, NodeID: r.ID},
		Revision:  r.UpdatedAt,
		URL:       r.URL,
		Title:     r.Title,
		State:     r.State,
		Author:    r.Author.login(),
		CreatedAt: r.CreatedAt,
		Body:      boundText(r.Body, lim.MaxBodyBytes),
	}
	if is.Body.Truncated {
		is.Gaps = append(is.Gaps, Gap{Field: "body", Reason: fmt.Sprintf("truncated at %d of %d bytes; sha256 %s", len(is.Body.Text), is.Body.Bytes, is.Body.SHA256)})
	}
	is.Attachments = append(is.Attachments, findAttachments(r.Body, "body")...)

	is.CommentsTotal = r.Comments.TotalCount
	for _, cm := range r.Comments.Nodes {
		t := boundText(cm.Body, lim.MaxBodyBytes)
		if t.Truncated {
			is.Gaps = append(is.Gaps, Gap{Field: "comment " + cm.ID, Reason: fmt.Sprintf("truncated at %d of %d bytes; sha256 %s", len(t.Text), t.Bytes, t.SHA256)})
		}
		is.Comments = append(is.Comments, Comment{ID: cm.ID, URL: cm.URL, Author: cm.Author.login(), CreatedAt: cm.CreatedAt, UpdatedAt: cm.UpdatedAt, Body: t})
		is.Attachments = append(is.Attachments, findAttachments(cm.Body, cm.ID)...)
	}
	if len(r.Comments.Nodes) < r.Comments.TotalCount {
		is.Gaps = append(is.Gaps, Gap{Field: "comments", Reason: fmt.Sprintf("not fetched: captured %d of %d", len(r.Comments.Nodes), r.Comments.TotalCount)})
	}

	is.LabelsTotal = r.Labels.TotalCount
	for _, l := range r.Labels.Nodes {
		is.Labels = append(is.Labels, l.Name)
	}
	if len(r.Labels.Nodes) < r.Labels.TotalCount {
		is.Gaps = append(is.Gaps, Gap{Field: "labels", Reason: fmt.Sprintf("not fetched: captured %d of %d", len(r.Labels.Nodes), r.Labels.TotalCount)})
	}

	is.ReferencesTotal = r.TimelineItems.TotalCount
	for _, n := range r.TimelineItems.Nodes {
		s := n.Source
		is.References = append(is.References, Reference{Kind: s.Typename, Repository: s.Repository.NameWithOwner, Number: s.Number, URL: s.URL})
	}
	if len(r.TimelineItems.Nodes) < r.TimelineItems.TotalCount {
		is.Gaps = append(is.Gaps, Gap{Field: "references", Reason: fmt.Sprintf("not fetched: captured %d of %d", len(r.TimelineItems.Nodes), r.TimelineItems.TotalCount)})
	}
	return is, nil
}

// boundText keeps s whole when it fits, else its first max bytes (cut on a
// rune boundary), always with the full length and digest.
func boundText(s string, max int) Text {
	sum := sha256.Sum256([]byte(s))
	t := Text{Text: s, Bytes: len(s), SHA256: hex.EncodeToString(sum[:])}
	if max > 0 && len(s) > max {
		cut := max
		for cut > 0 && (s[cut]&0xC0) == 0x80 {
			cut--
		}
		t.Text, t.Truncated = s[:cut], true
	}
	return t
}

func findAttachments(text, where string) []Attachment {
	var out []Attachment
	for _, u := range attachmentRe.FindAllString(text, -1) {
		out = append(out, Attachment{URL: strings.TrimRight(u, ".,"), Where: where, Fetched: false, Status: "not fetched"})
	}
	return out
}

// readBounded reads one file, refusing it past perFile bytes or past what is
// left of the bundle budget, before reading more than the bound.
func readBounded(p string, perFile int64, budget *int64) ([]byte, error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, refuse("incomplete", "%v", err)
	}
	defer f.Close()
	limit := min(perFile, *budget)
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, refuse("parse", "%s: %v", p, err)
	}
	if int64(len(b)) > limit {
		return nil, refuse("bound", "%s exceeds the %d-byte bound (per file %d, bundle left %d)", filepath.Base(p), limit, perFile, *budget)
	}
	*budget -= int64(len(b))
	return b, nil
}
