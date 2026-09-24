// Package ghcapture is nova-work's read-only GitHub issue adapter (E09-F01,
// nova-tools#2080).
//
// It is two halves on either side of a file boundary. Record (outside the
// kernel) reads GitHub with queries only and writes a capture bundle: one
// original per issue, byte-exact, plus a manifest. Open (the adapter) ingests a
// bundle offline, never a socket: it checks the manifest against the bundle's
// contents, refuses a page gap or a tampered digest, bounds every read, and
// exposes each issue's stable identity, revision, URL, body, comments, labels,
// cross-references, attachments and explicit gaps as data. The adapter has no
// write method; remote text is kept as data and never interpreted.
package ghcapture

// Manifest is the bundle's index, written by Record and checked by Open.
type Manifest struct {
	Provider    string          `json:"provider"`
	Owner       string          `json:"owner"`
	Repo        string          `json:"repo"`
	FetchedAt   string          `json:"fetched_at"`
	PageSize    int             `json:"page_size"`
	TotalIssues int             `json:"total_issues"`
	Pages       []Page          `json:"pages"`
	Issues      []ManifestIssue `json:"issues"`
}

// Page is one fetched page: the cursor it was asked after, the cursor it ended
// at, whether GitHub said more pages follow, and the issue numbers it held.
type Page struct {
	Page      int    `json:"page"`
	After     string `json:"after"`
	EndCursor string `json:"end_cursor"`
	HasNext   bool   `json:"has_next"`
	Issues    []int  `json:"issues"`
}

// ManifestIssue is one issue's entry: its revision (GitHub updatedAt), the
// file holding its original bytes, and their digest and length.
type ManifestIssue struct {
	Number   int    `json:"number"`
	Revision string `json:"revision"`
	File     string `json:"file"`
	SHA256   string `json:"sha256"`
	Bytes    int    `json:"bytes"`
}
