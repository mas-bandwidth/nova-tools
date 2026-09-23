package ghcapture

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

// bundleDir is a capture recorded from a real repository with
// `go run ./internal/ghcapture/cmd/ghcapture-record -owner mas-bandwidth -repo netcode -page 25`:
// 68 issues over three pages (25, 25, 18), comments up to 89 on one issue,
// labels, cross-references from issues and pull requests, and attachments.
const bundleDir = "testdata/netcode"

func openBundle(t *testing.T, dir string, lim Limits) *Capture {
	t.Helper()
	c, err := Open(dir, lim)
	if err != nil {
		t.Fatalf("Open(%s): %v", dir, err)
	}
	return c
}

// original decodes one issue file independently of the adapter, so the tests
// compare the adapter's view with the bytes GitHub sent, not with itself.
func original(t *testing.T, dir string, n int) map[string]any {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "issues", strconv.Itoa(n)+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func readManifest(t *testing.T, dir string) Manifest {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// copyBundle copies the recorded bundle into a fresh directory for a test that
// tampers with it.
func copyBundle(t *testing.T) string {
	t.Helper()
	dst := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dst, "issues"), 0o755); err != nil {
		t.Fatal(err)
	}
	src, err := filepath.Abs(bundleDir)
	if err != nil {
		t.Fatal(err)
	}
	// Originals are linked, not copied (rewriteIssue replaces a link with a
	// file before writing, so the recording is never touched); the manifest
	// is copied because most tamper tests edit it.
	for _, rel := range listBundle(t, bundleDir)[1:] {
		if os.Symlink(filepath.Join(src, rel), filepath.Join(dst, rel)) == nil {
			continue
		}
		// No symlinks (an unprivileged Windows runner): copy instead.
		b, err := os.ReadFile(filepath.Join(src, rel))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dst, rel), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	b, err := os.ReadFile(filepath.Join(bundleDir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dst, "manifest.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
	return dst
}

func listBundle(t *testing.T, dir string) []string {
	t.Helper()
	out := []string{"manifest.json"}
	ents, err := os.ReadDir(filepath.Join(dir, "issues"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ents {
		out = append(out, "issues/"+e.Name())
	}
	return out
}

func writeManifest(t *testing.T, dir string, edit func(*Manifest)) {
	t.Helper()
	m := readManifest(t, dir)
	edit(&m)
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
}

// rewriteIssue edits one original and, when redigest is true, updates the
// manifest's digest and length so only the edit itself is under test.
func rewriteIssue(t *testing.T, dir string, n int, redigest bool, edit func(map[string]any)) {
	t.Helper()
	m := original(t, dir, n)
	edit(m)
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "issues", strconv.Itoa(n)+".json")
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
	if !redigest {
		return
	}
	sum := sha256.Sum256(b)
	writeManifest(t, dir, func(man *Manifest) {
		for i := range man.Issues {
			if man.Issues[i].Number == n {
				man.Issues[i].SHA256 = hex.EncodeToString(sum[:])
				man.Issues[i].Bytes = len(b)
			}
		}
	})
}

func wantRefusal(t *testing.T, err error, code string) {
	t.Helper()
	var r *RefusalError
	if !errors.As(err, &r) {
		t.Fatalf("got %v, want a refusal %q", err, code)
	}
	if r.Code != code {
		t.Fatalf("refusal code %q (%v), want %q", r.Code, err, code)
	}
}

func sha(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// TestStableIdentityRevisionAndURL is E09-F01-01 (roadmap test name
// stable-identity-revision-and-url): every captured issue carries a stable
// provider/repository/issue identity, its revision and its URL, the same on
// every read of the same capture wherever it sits, and an original that
// contradicts its manifest on any of them is refused.
func TestStableIdentityRevisionAndURL(t *testing.T) {
	c := openBundle(t, bundleDir, DefaultLimits())
	man := readManifest(t, bundleDir)

	if got := c.Provider(); got != "github" {
		t.Fatalf("provider %q, want github", got)
	}
	if got := c.Repository(); got != "mas-bandwidth/netcode" {
		t.Fatalf("repository %q, want mas-bandwidth/netcode", got)
	}
	if got := c.FetchedAt(); got != man.FetchedAt {
		t.Fatalf("fetched-at %q, want the manifest's %q", got, man.FetchedAt)
	}
	issues := c.Issues()
	if len(issues) != 68 {
		t.Fatalf("%d issues, want the 68 the capture holds", len(issues))
	}
	revs := map[int]string{}
	for _, mi := range man.Issues {
		revs[mi.Number] = mi.Revision
	}
	nodeIDs := map[string]int{}
	for _, is := range issues {
		n := is.Identity.Number
		o := original(t, bundleDir, n)
		if want := fmt.Sprintf("github:mas-bandwidth/netcode#%d", n); is.Identity.Key() != want {
			t.Errorf("#%d: key %q, want %q", n, is.Identity.Key(), want)
		}
		if is.Identity.Provider != "github" || is.Identity.Owner != "mas-bandwidth" || is.Identity.Repo != "netcode" {
			t.Errorf("#%d: identity %+v, want github mas-bandwidth/netcode", n, is.Identity)
		}
		if is.Identity.NodeID == "" || is.Identity.NodeID != o["id"] {
			t.Errorf("#%d: node id %q, want the original's %v", n, is.Identity.NodeID, o["id"])
		}
		if prev, dup := nodeIDs[is.Identity.NodeID]; dup {
			t.Errorf("#%d and #%d share node id %q", n, prev, is.Identity.NodeID)
		}
		nodeIDs[is.Identity.NodeID] = n
		if is.Revision == "" || is.Revision != o["updatedAt"] || is.Revision != revs[n] {
			t.Errorf("#%d: revision %q, want original %v and manifest %q", n, is.Revision, o["updatedAt"], revs[n])
		}
		if _, err := time.Parse(time.RFC3339, is.Revision); err != nil {
			t.Errorf("#%d: revision %q is not a timestamp: %v", n, is.Revision, err)
		}
		if want := fmt.Sprintf("https://github.com/mas-bandwidth/netcode/issues/%d", n); is.URL != want {
			t.Errorf("#%d: url %q, want %q", n, is.URL, want)
		}
		if got, ok := c.Lookup(is.Identity.Key()); !ok || got.Identity != is.Identity {
			t.Errorf("#%d: Lookup(%q) = %v, %v", n, is.Identity.Key(), got.Identity, ok)
		}
	}

	// Stable: a second read of the same capture from another directory gives
	// the same identities and revisions in the same order.
	again := openBundle(t, copyBundle(t), DefaultLimits()).Issues()
	for i := range issues {
		if again[i].Identity != issues[i].Identity || again[i].Revision != issues[i].Revision || again[i].URL != issues[i].URL {
			t.Fatalf("second read differs at %d: %+v vs %+v", i, again[i].Identity, issues[i].Identity)
		}
	}

	// An original naming another repository is refused, not re-keyed.
	dir := copyBundle(t)
	rewriteIssue(t, dir, 17, true, func(m map[string]any) {
		m["url"] = strings.Replace(m["url"].(string), "mas-bandwidth/netcode", "someone/else", 1)
	})
	_, err := Open(dir, DefaultLimits())
	wantRefusal(t, err, "identity")

	// An original whose number disagrees with its manifest entry is refused.
	dir = copyBundle(t)
	rewriteIssue(t, dir, 17, true, func(m map[string]any) { m["number"] = 18.0 })
	_, err = Open(dir, DefaultLimits())
	wantRefusal(t, err, "identity")

	// A revision the original does not carry is refused.
	dir = copyBundle(t)
	writeManifest(t, dir, func(m *Manifest) { m.Issues[3].Revision = "2001-01-01T00:00:00Z" })
	_, err = Open(dir, DefaultLimits())
	wantRefusal(t, err, "revision")
}

// TestBodyCommentsLabelsRelationshipsAndPagination is E09-F01-02 (roadmap
// test name body-comments-labels-relationships-and-pagination): the body,
// every comment, every label, every cross-reference and every attachment of
// every issue is preserved from the original, and the page chain is whole.
func TestBodyCommentsLabelsRelationshipsAndPagination(t *testing.T) {
	c := openBundle(t, bundleDir, DefaultLimits())
	man := readManifest(t, bundleDir)

	// Pagination: three pages of 25, 25 and 18, every issue on exactly one,
	// and every issue knows its page.
	if c.Pages() != 3 {
		t.Fatalf("%d pages, want 3", c.Pages())
	}
	perPage := map[int]int{}
	seen := map[int]bool{}
	for _, is := range c.Issues() {
		perPage[is.Page]++
		if seen[is.Identity.Number] {
			t.Fatalf("#%d captured twice", is.Identity.Number)
		}
		seen[is.Identity.Number] = true
	}
	if perPage[1] != 25 || perPage[2] != 25 || perPage[3] != 18 {
		t.Fatalf("issues per page %v, want 25/25/18", perPage)
	}
	if len(seen) != man.TotalIssues {
		t.Fatalf("%d distinct issues, manifest reports %d", len(seen), man.TotalIssues)
	}

	var comments, labelled, referenced, attached int
	for _, is := range c.Issues() {
		n := is.Identity.Number
		o := original(t, bundleDir, n)

		// Body: byte-for-byte, with its digest.
		body, _ := o["body"].(string)
		if is.Body.Text != body || is.Body.Bytes != len(body) || is.Body.SHA256 != sha(body) || is.Body.Truncated {
			t.Errorf("#%d: body not preserved (len %d vs %d)", n, len(is.Body.Text), len(body))
		}
		if is.Title != o["title"] || is.State != o["state"] {
			t.Errorf("#%d: title/state %q/%q, want %v/%v", n, is.Title, is.State, o["title"], o["state"])
		}

		// Comments: all of them, in order, with ids, URLs, authors and bodies.
		oc := o["comments"].(map[string]any)
		nodes := oc["nodes"].([]any)
		if is.CommentsTotal != int(oc["totalCount"].(float64)) || len(is.Comments) != len(nodes) {
			t.Errorf("#%d: %d/%d comments, want %d/%v", n, len(is.Comments), is.CommentsTotal, len(nodes), oc["totalCount"])
			continue
		}
		for i, raw := range nodes {
			cm := raw.(map[string]any)
			got := is.Comments[i]
			if got.ID != cm["id"] || got.URL != cm["url"] || got.Body.Text != cm["body"] || got.CreatedAt != cm["createdAt"] {
				t.Errorf("#%d comment %d: %+v does not match the original", n, i, got.ID)
			}
			if a, ok := cm["author"].(map[string]any); ok && got.Author != a["login"] {
				t.Errorf("#%d comment %d: author %q, want %v", n, i, got.Author, a["login"])
			}
		}
		comments += len(is.Comments)

		// Labels.
		ol := o["labels"].(map[string]any)
		var want []string
		for _, l := range ol["nodes"].([]any) {
			want = append(want, l.(map[string]any)["name"].(string))
		}
		if !reflect.DeepEqual(is.Labels, want) && !(len(is.Labels) == 0 && len(want) == 0) {
			t.Errorf("#%d: labels %v, want %v", n, is.Labels, want)
		}
		if is.LabelsTotal != int(ol["totalCount"].(float64)) {
			t.Errorf("#%d: labels total %d, want %v", n, is.LabelsTotal, ol["totalCount"])
		}
		if len(is.Labels) > 0 {
			labelled++
		}

		// Relationships: every cross-reference, with its kind, repository,
		// number and URL.
		ot := o["timelineItems"].(map[string]any)
		tn := ot["nodes"].([]any)
		if len(is.References) != len(tn) || is.ReferencesTotal != int(ot["totalCount"].(float64)) {
			t.Errorf("#%d: %d/%d references, want %d/%v", n, len(is.References), is.ReferencesTotal, len(tn), ot["totalCount"])
			continue
		}
		for i, raw := range tn {
			src := raw.(map[string]any)["source"].(map[string]any)
			r := is.References[i]
			repo := src["repository"].(map[string]any)["nameWithOwner"]
			if r.Kind != src["__typename"] || r.URL != src["url"] || r.Repository != repo || float64(r.Number) != src["number"] {
				t.Errorf("#%d reference %d: %+v, want %v", n, i, r, src)
			}
		}
		if len(is.References) > 0 {
			referenced++
		}

		// Attachments: every attachment URL in the body or a comment, kept
		// as a URL marked not fetched, never dropped.
		for _, a := range is.Attachments {
			if a.Fetched || a.Status != "not fetched" || a.URL == "" {
				t.Errorf("#%d: attachment %+v, want a URL marked not fetched", n, a)
			}
			if !strings.Contains(body, a.URL) && !commentsContain(nodes, a.URL) {
				t.Errorf("#%d: attachment %q is in neither body nor comments", n, a.URL)
			}
		}
		if len(is.Attachments) > 0 {
			attached++
		}
		if len(is.Gaps) != 0 {
			t.Errorf("#%d: gaps %v on a complete capture", n, is.Gaps)
		}
	}
	if comments < 300 || labelled < 3 || referenced < 5 || attached < 3 {
		t.Fatalf("capture coverage comments=%d labelled=%d referenced=%d attached=%d; the recorded bundle should exercise every field", comments, labelled, referenced, attached)
	}
	is8, _ := c.Issue(8)
	if len(is8.Comments) != 89 {
		t.Fatalf("#8 has %d comments, want 89", len(is8.Comments))
	}
	is2, _ := c.Issue(2)
	if len(is2.References) != 3 {
		t.Fatalf("#2 has %d references, want 3", len(is2.References))
	}
	is119, _ := c.Issue(119)
	if len(is119.Labels) != 2 {
		t.Fatalf("#119 labels %v, want 2", is119.Labels)
	}

	// More comments, labels or references than the capture holds is an
	// explicit gap, never a silent short list (E09-F01-03 stays held).
	dir := copyBundle(t)
	rewriteIssue(t, dir, 8, true, func(m map[string]any) {
		m["comments"].(map[string]any)["totalCount"] = 140.0
	})
	c2 := openBundle(t, dir, DefaultLimits())
	g8, _ := c2.Issue(8)
	if len(g8.Gaps) != 1 || g8.Gaps[0].Field != "comments" || !strings.Contains(g8.Gaps[0].Reason, "89 of 140") {
		t.Fatalf("#8 gaps %v, want one explicit comments gap 89 of 140", g8.Gaps)
	}
}

func commentsContain(nodes []any, s string) bool {
	for _, raw := range nodes {
		if b, _ := raw.(map[string]any)["body"].(string); strings.Contains(b, s) {
			return true
		}
	}
	return false
}

// TestTamperedManifestIsRefused: a manifest whose totals or entries disagree
// with the bundle's contents, or an original whose bytes do not match its
// digest, refuses the whole capture.
func TestTamperedManifestIsRefused(t *testing.T) {
	dir := copyBundle(t)
	writeManifest(t, dir, func(m *Manifest) { m.TotalIssues = 69 })
	_, err := Open(dir, DefaultLimits())
	wantRefusal(t, err, "manifest-total")

	dir = copyBundle(t)
	writeManifest(t, dir, func(m *Manifest) { m.Issues = m.Issues[1:] })
	_, err = Open(dir, DefaultLimits())
	wantRefusal(t, err, "manifest-total")

	dir = copyBundle(t)
	rewriteIssue(t, dir, 33, false, func(m map[string]any) { m["body"] = "rewritten after capture" })
	_, err = Open(dir, DefaultLimits())
	wantRefusal(t, err, "digest")

	dir = copyBundle(t)
	writeManifest(t, dir, func(m *Manifest) { m.Provider = "gitlab" })
	_, err = Open(dir, DefaultLimits())
	wantRefusal(t, err, "identity")

	dir = copyBundle(t)
	writeManifest(t, dir, func(m *Manifest) { m.Issues[0].File = "../manifest.json" })
	_, err = Open(dir, DefaultLimits())
	wantRefusal(t, err, "path")
}

// TestPageGapIsRefused: a missing page, a page asked after a cursor the
// previous page did not end at, or a last page that says more follow, refuses
// the capture.
func TestPageGapIsRefused(t *testing.T) {
	dir := copyBundle(t)
	writeManifest(t, dir, func(m *Manifest) {
		gone := map[int]bool{}
		for _, n := range m.Pages[1].Issues {
			gone[n] = true
		}
		m.Pages = append(m.Pages[:1], m.Pages[2:]...)
		var keep []ManifestIssue
		for _, mi := range m.Issues {
			if !gone[mi.Number] {
				keep = append(keep, mi)
			}
		}
		m.Issues = keep
		m.TotalIssues = len(keep)
	})
	_, err := Open(dir, DefaultLimits())
	wantRefusal(t, err, "page-gap")

	dir = copyBundle(t)
	writeManifest(t, dir, func(m *Manifest) { m.Pages[2].After = m.Pages[0].EndCursor })
	_, err = Open(dir, DefaultLimits())
	wantRefusal(t, err, "page-gap")

	dir = copyBundle(t)
	writeManifest(t, dir, func(m *Manifest) { m.Pages[2].HasNext = true })
	_, err = Open(dir, DefaultLimits())
	wantRefusal(t, err, "incomplete")

	dir = copyBundle(t)
	writeManifest(t, dir, func(m *Manifest) { m.Pages[1].Issues = append(m.Pages[1].Issues, m.Pages[0].Issues[0]) })
	_, err = Open(dir, DefaultLimits())
	wantRefusal(t, err, "manifest-total")
}

// TestOversizeBodyIsTruncatedWithHash: a body over the bound is stored as an
// explicit truncated record carrying the full length and digest, never cut
// silently; the bundle and per-issue byte bounds refuse.
func TestOversizeBodyIsTruncatedWithHash(t *testing.T) {
	lim := DefaultLimits()
	lim.MaxBodyBytes = 1000
	c := openBundle(t, bundleDir, lim)
	var cut int
	for _, is := range c.Issues() {
		body, _ := original(t, bundleDir, is.Identity.Number)["body"].(string)
		if len(body) <= 1000 {
			if is.Body.Truncated {
				t.Errorf("#%d: %d-byte body marked truncated", is.Identity.Number, len(body))
			}
			continue
		}
		cut++
		if !is.Body.Truncated || len(is.Body.Text) > 1000 || is.Body.Bytes != len(body) || is.Body.SHA256 != sha(body) || !strings.HasPrefix(body, is.Body.Text) {
			t.Errorf("#%d: oversize body %+v not an explicit truncated-with-hash record", is.Identity.Number, is.Body.Bytes)
		}
		if !hasGap(is.Gaps, "body") {
			t.Errorf("#%d: truncated body without an explicit body gap: %v", is.Identity.Number, is.Gaps)
		}
	}
	if cut == 0 {
		t.Fatal("no body in the capture exceeded 1000 bytes; the bound was not exercised")
	}

	lim = DefaultLimits()
	lim.MaxBundleBytes = 64 << 10
	_, err := Open(bundleDir, lim)
	wantRefusal(t, err, "bound")

	lim = DefaultLimits()
	lim.MaxIssues = 10
	_, err = Open(bundleDir, lim)
	wantRefusal(t, err, "bound")
}

func hasGap(gs []Gap, field string) bool {
	for _, g := range gs {
		if g.Field == field {
			return true
		}
	}
	return false
}

// TestAdapterHasNoWriteMethod: the adapter is read-only by construction: the
// capture exposes readers only, and the adapter's own source imports nothing
// that reaches a host or runs a program.
func TestAdapterHasNoWriteMethod(t *testing.T) {
	allowed := map[string]bool{"Provider": true, "Repository": true, "FetchedAt": true, "Pages": true, "Issues": true, "Issue": true, "Lookup": true}
	typ := reflect.TypeOf(&Capture{})
	var names []string
	for i := 0; i < typ.NumMethod(); i++ {
		names = append(names, typ.Method(i).Name)
		if !allowed[typ.Method(i).Name] {
			t.Errorf("Capture has method %s; the adapter exposes readers only", typ.Method(i).Name)
		}
	}
	sort.Strings(names)
	if len(names) != len(allowed) {
		t.Errorf("Capture methods %v, want exactly %d readers", names, len(allowed))
	}
	fset := token.NewFileSet()
	for _, f := range []string{"adapter.go", "manifest.go"} {
		af, err := parser.ParseFile(fset, f, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, im := range af.Imports {
			p, _ := strconv.Unquote(im.Path.Value)
			if p == "os/exec" || p == "net" || strings.HasPrefix(p, "net/") {
				t.Errorf("%s imports %s; the adapter ingests a capture file, never a socket or a program", f, p)
			}
		}
	}
}

// TestRecordReplaysTheCaptureAndRefusesMutation: Record, fed the recorded
// pages back through a fake query, writes the same originals and page chain;
// the production query refuses any document that is not a query.
func TestRecordReplaysTheCaptureAndRefusesMutation(t *testing.T) {
	man := readManifest(t, bundleDir)
	byAfter := map[string]Page{}
	for _, p := range man.Pages {
		byAfter[p.After] = p
	}
	calls := 0
	fake := func(query string, vars map[string]string) ([]byte, error) {
		calls++
		if err := refuseMutation(query); err != nil {
			return nil, err
		}
		if vars["owner"] != "mas-bandwidth" || vars["repo"] != "netcode" || vars["n"] != "25" {
			return nil, fmt.Errorf("unexpected vars %v", vars)
		}
		p, ok := byAfter[vars["after"]]
		if !ok {
			return nil, fmt.Errorf("no page after %q", vars["after"])
		}
		var nodes []string
		for _, n := range p.Issues {
			b, err := os.ReadFile(filepath.Join(bundleDir, "issues", strconv.Itoa(n)+".json"))
			if err != nil {
				return nil, err
			}
			nodes = append(nodes, string(b))
		}
		return []byte(fmt.Sprintf(`{"data":{"repository":{"issues":{"totalCount":%d,"pageInfo":{"hasNextPage":%t,"endCursor":%q},"nodes":[%s]}}}}`,
			man.TotalIssues, p.HasNext, p.EndCursor, strings.Join(nodes, ","))), nil
	}
	out := t.TempDir()
	fetched, _ := time.Parse(time.RFC3339, man.FetchedAt)
	got, err := Record(fake, "mas-bandwidth", "netcode", 25, 10, fetched, out)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 3 || !reflect.DeepEqual(got.Pages, man.Pages) || !reflect.DeepEqual(got.Issues, man.Issues) {
		t.Fatalf("replayed record differs: %d calls, pages %d vs %d", calls, len(got.Pages), len(man.Pages))
	}
	for _, rel := range listBundle(t, bundleDir)[1:] {
		a, _ := os.ReadFile(filepath.Join(bundleDir, rel))
		b, _ := os.ReadFile(filepath.Join(out, rel))
		if string(a) != string(b) {
			t.Fatalf("%s differs after replay", rel)
		}
	}
	if _, err := Open(out, DefaultLimits()); err != nil {
		t.Fatalf("replayed bundle refused: %v", err)
	}

	// A page bound hit with pages left leaves an incomplete bundle Open refuses.
	short := t.TempDir()
	if _, err := Record(fake, "mas-bandwidth", "netcode", 25, 2, fetched, short); err == nil {
		t.Fatal("Record stopped at the page bound without saying the bundle is incomplete")
	}
	_, err = Open(short, DefaultLimits())
	wantRefusal(t, err, "incomplete")

	for _, doc := range []string{"mutation { closeIssue(input:{issueId:\"x\"}) { clientMutationId } }", "query { a } mutation { b }", "subscription { x }"} {
		if _, err := GhQuery(doc, nil); err == nil || !regexp.MustCompile(`refused`).MatchString(err.Error()) {
			t.Errorf("GhQuery(%q) = %v, want a refusal before any program runs", doc, err)
		}
	}
}

// TestRecordDoesNotFollowSymlinksOutOfTheBundle: Record never writes through a
// pre-existing symlink (an issue file, manifest.json or the issues directory)
// into a file outside the bundle; it refuses, and the outside target keeps its
// bytes (stella, nova-tools#3242).
func TestRecordDoesNotFollowSymlinksOutOfTheBundle(t *testing.T) {
	man := readManifest(t, bundleDir)
	first := man.Pages[0]
	var nodes []string
	for _, n := range first.Issues {
		b, err := os.ReadFile(filepath.Join(bundleDir, "issues", strconv.Itoa(n)+".json"))
		if err != nil {
			t.Fatal(err)
		}
		nodes = append(nodes, string(b))
	}
	onePage := func(query string, vars map[string]string) ([]byte, error) {
		return []byte(fmt.Sprintf(`{"data":{"repository":{"issues":{"totalCount":%d,"pageInfo":{"hasNextPage":false,"endCursor":%q},"nodes":[%s]}}}}`,
			len(first.Issues), first.EndCursor, strings.Join(nodes, ","))), nil
	}
	fetched, _ := time.Parse(time.RFC3339, man.FetchedAt)
	const keep = "outside the bundle, must not change\n"
	for _, tc := range []struct{ name, link string }{
		{"issue file", filepath.Join("issues", strconv.Itoa(first.Issues[0])+".json")},
		{"manifest", "manifest.json"},
		{"issues directory", "issues"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			outside := t.TempDir()
			target := filepath.Join(outside, "victim")
			if tc.link == "issues" {
				if err := os.Mkdir(target, 0o755); err != nil {
					t.Fatal(err)
				}
				target = filepath.Join(target, strconv.Itoa(first.Issues[0])+".json")
			}
			if err := os.WriteFile(target, []byte(keep), 0o644); err != nil {
				t.Fatal(err)
			}
			out := t.TempDir()
			if err := os.MkdirAll(filepath.Join(out, "issues"), 0o755); err != nil {
				t.Fatal(err)
			}
			linkTo := target
			if tc.link == "issues" {
				linkTo = filepath.Dir(target)
				if err := os.Remove(filepath.Join(out, "issues")); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.Symlink(linkTo, filepath.Join(out, tc.link)); err != nil {
				t.Skipf("symlinks unavailable: %v", err)
			}
			_, err := Record(onePage, "mas-bandwidth", "netcode", 25, 1, fetched, out)
			if err == nil || !strings.Contains(err.Error(), "not a regular") {
				t.Errorf("Record through a %s symlink = %v, want a not-a-regular refusal", tc.name, err)
			}
			if got, _ := os.ReadFile(target); string(got) != keep {
				t.Fatalf("Record wrote through the %s symlink: target now %d bytes", tc.name, len(got))
			}
		})
	}
}
