package release

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// A friend's daemon follows its row's release (docs/SPEC-FRIEND.md, "The row is followed";
// internal/friend/follow.go): it fetches one tool of one release from the repository's
// GitHub release, the way a person downloads one, and verifies it the way install verifies
// a directory: the release's SHA256SUMS names the asset and its digest, and the bytes
// fetched must hash to it. adopt checks SHA256SUMS itself against a digest that did not
// travel with the bits, the tag object's `sums=` line (cut.go); the follow does the same
// when the tag carries one, and says when it does not (a release cut before the tags were
// annotated), since a daemon on a friend's machine has no other channel to the digest: the
// release's own checksum file over TLS is then the whole of the check, which is what every
// reader of a GitHub release has. No gh, no token, no ssh: a friend's host carries no
// credential, and a public release needs none.

// Source is the edge between the follow and the release's host: one asset of one release
// by name, and the message of the release's tag object ("" for a lightweight tag). A test
// fakes it; GitHubSource is the production one (Source is the build's edge to the tree, another thing).
type AssetSource interface {
	Asset(ctx context.Context, repo, version, name string) ([]byte, error)
	TagMessage(ctx context.Context, repo, version string) (string, error)
}

// AssetName is the file one tool ships as on a GitHub release (tools/ghrelease attach:
// <tool>_<version>_<goos>_<goarch>, .exe on windows), the name SHA256SUMS lists it by.
func AssetName(tool, version, goos, goarch string) string {
	return tool + "_" + version + "_" + goos + "_" + goarch + ExeSuffix(goos)
}

// AssetCap bounds one fetched asset: a tool's binary is tens of megabytes, never more
// than this; a checksum file is kilobytes.
const AssetCap = 256 << 20

// GitHubSource fetches a release's assets and its tag's message from GitHub over HTTPS,
// without a credential: a public release serves both. Base is the site, API its API,
// each defaulting to GitHub's; Client defaults to one with a bounded timeout.
type GitHubSource struct {
	Base, API string
	Client    *http.Client
}

func (g GitHubSource) client() *http.Client {
	if g.Client != nil {
		return g.Client
	}
	return &http.Client{Timeout: 5 * time.Minute}
}

func (g GitHubSource) base() string {
	if g.Base != "" {
		return strings.TrimSuffix(g.Base, "/")
	}
	return "https://github.com"
}

func (g GitHubSource) api() string {
	if g.API != "" {
		return strings.TrimSuffix(g.API, "/")
	}
	return "https://api.github.com"
}

// Asset is GET <base>/<repo>/releases/download/<version>/<name>, the bytes whole, under
// AssetCap.
func (g GitHubSource) Asset(ctx context.Context, repo, version, name string) ([]byte, error) {
	return g.get(ctx, g.base()+"/"+repo+"/releases/download/"+url.PathEscape(version)+"/"+url.PathEscape(name), AssetCap)
}

// TagMessage is the tag object's message, read as GH.TagMessage reads it (edges.go): the
// ref, then the object it points at; a lightweight tag (one pointing straight at a
// commit) answers "" and no error, the follow then having no digest to hold SHA256SUMS to.
func (g GitHubSource) TagMessage(ctx context.Context, repo, version string) (string, error) {
	raw, err := g.get(ctx, g.api()+"/repos/"+repo+"/git/ref/tags/"+url.PathEscape(version), 1<<20)
	if err != nil {
		return "", err
	}
	var ref struct {
		Object struct {
			Type, SHA string `json:",omitempty"`
		} `json:"object"`
	}
	if err := json.Unmarshal(raw, &ref); err != nil {
		return "", fmt.Errorf("the tag ref of %s in %s cannot be read: %w", version, repo, err)
	}
	if ref.Object.Type != "tag" {
		return "", nil
	}
	raw, err = g.get(ctx, g.api()+"/repos/"+repo+"/git/tags/"+url.PathEscape(ref.Object.SHA), 1<<20)
	if err != nil {
		return "", err
	}
	var obj struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(raw, &obj); err != nil {
		return "", fmt.Errorf("the tag object of %s in %s cannot be read: %w", version, repo, err)
	}
	return obj.Message, nil
}

func (g GitHubSource) get(ctx context.Context, at string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, at, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/octet-stream, application/vnd.github+json")
	resp, err := g.client().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close() // ignored: a read-only body
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	switch {
	case err != nil:
		return nil, fmt.Errorf("GET %s: the body was cut: %w", at, err)
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("GET %s: %s", at, resp.Status)
	case int64(len(body)) > limit:
		return nil, fmt.Errorf("GET %s: the body is over %d bytes", at, limit)
	}
	return body, nil
}

// ParseSums reads a SHA256SUMS body: one `<sha256>  <name>` line per artifact, in the
// format `sha256sum -c` reads, sorted by name. It is what ReadSums trusts about a
// directory, read here off bytes fetched instead of a file.
func ParseSums(body []byte) ([]Artifact, error) {
	var arts []Artifact
	for i, line := range strings.Split(strings.TrimSpace(string(body)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		sum, name, ok := strings.Cut(line, "  ")
		if !ok || sum == "" || name == "" {
			return nil, refuse("build the release again with `nova-update release build`",
				"%s line %d is not a sha256sum line: %q", SumsFile, i+1, line)
		}
		// A name with a separator in it would install outside --bin. The
		// build writes bare names; anything else is not this file's.
		if strings.ContainsAny(name, `/\`) || name == "." || name == ".." {
			return nil, refuse("build the release again with `nova-update release build`",
				"%s line %d names a path rather than a file: %q", SumsFile, i+1, name)
		}
		// The name is also written under --bin by install and interpolated into
		// the remote `rm -f` adopt composes, which the far shell parses after ssh
		// reassembles argv: held here to the narrowness pull already enforces
		// (remoteArtifactName), so every caller sees only safe names
		// (security#72 finding 1, artifact-name half).
		if !remoteArtifactName.MatchString(name) {
			return nil, refuse("build the release again with `nova-update release build`",
				"%s line %d names %q, which is not a file name this tool will install or delete", SumsFile, i+1, name)
		}
		arts = append(arts, Artifact{Name: name, Sum: sum})
	}
	if len(arts) == 0 {
		return nil, refuse("build the release again with `nova-update release build`",
			"%s lists no artifact", SumsFile)
	}
	sort.Slice(arts, func(i, j int) bool { return arts[i].Name < arts[j].Name })
	return arts, nil
}

// Fetched is what FetchTool answers: the verified binary staged in dir, and how the
// release's checksum file was held: to the tag's digest, or by the release alone.
type Fetched struct {
	Staged string
	// Digest is the tag object's `sums=` digest SHA256SUMS matched, "" when the tag
	// carries none: then the checksum file came from the same release as the bits and
	// vouches only that the fetch was whole.
	Digest string
}

// ErrNotShipped is the refusal of a release whose SHA256SUMS names no asset of the tool
// for the platform: nothing is fetched.
var ErrNotShipped = errors.New("the release ships no such asset")

// FetchTool fetches one tool of one release for one platform through src and stages it in
// dir as a private temporary file (writeTemp, mode 0755), verified: the release's
// SHA256SUMS is read first and held to the tag's `sums=` digest when the tag carries one;
// the asset's bytes must hash to the line SHA256SUMS gives them. Anything short of that
// stages nothing and says why. The staged file is the caller's to put in place (one
// rename) or to remove.
func FetchTool(ctx context.Context, src AssetSource, repo, version, tool, goos, goarch, dir string) (Fetched, error) {
	if err := ValidVersion(version); err != nil {
		return Fetched{}, err
	}
	if repo == "" || strings.Count(repo, "/") != 1 || strings.ContainsAny(repo, " \t\n") {
		return Fetched{}, fmt.Errorf("the release repository %q is not owner/name", repo)
	}
	sums, err := src.Asset(ctx, repo, version, SumsFile)
	if err != nil {
		return Fetched{}, fmt.Errorf("the %s of release %s of %s cannot be fetched: %w", SumsFile, version, repo, err)
	}
	arts, err := ParseSums(sums)
	if err != nil {
		return Fetched{}, fmt.Errorf("the %s of release %s of %s: %w", SumsFile, version, repo, err)
	}
	var out Fetched
	message, err := src.TagMessage(ctx, repo, version)
	if err != nil {
		return Fetched{}, fmt.Errorf("the tag %s of %s cannot be read for its digest: %w", version, repo, err)
	}
	if digest := SumsInAnnotation(message); digest != "" {
		got := sha256.Sum256(sums)
		if hex.EncodeToString(got[:]) != digest {
			return Fetched{}, refuse("do not follow this release; the checksum file on the release is not the one it was cut with",
				"the %s fetched for %s has digest %s, but the tag says the release was cut with %s", SumsFile, version, hex.EncodeToString(got[:]), digest)
		}
		out.Digest = digest
	}
	name := AssetName(tool, version, goos, goarch)
	want := ""
	for _, a := range arts {
		if a.Name == name {
			want = a.Sum
		}
	}
	if want == "" {
		return Fetched{}, fmt.Errorf("%w: %s names no %s", ErrNotShipped, SumsFile, name)
	}
	body, err := src.Asset(ctx, repo, version, name)
	if err != nil {
		return Fetched{}, fmt.Errorf("%s of release %s of %s cannot be fetched: %w", name, version, repo, err)
	}
	got := sha256.Sum256(body)
	if hex.EncodeToString(got[:]) != want {
		return Fetched{}, refuse("do not follow this release; the bytes fetched are not the bytes that were cut",
			"%s fetched for %s hashes to %s, and %s says %s", name, version, hex.EncodeToString(got[:]), SumsFile, want)
	}
	staged, err := writeTemp(dir, "."+ToolFile(tool, goos)+"."+version+".new.*", body, 0o755)
	if err != nil {
		return Fetched{}, fmt.Errorf("the fetched %s cannot be staged beside the running binary: %w", name, err)
	}
	out.Staged = staged
	return out, nil
}

// Swap puts a staged binary in place of target by one rename, the staged file's bytes
// already verified (FetchTool): the running process keeps the image it has, and the next
// start runs the new one. A target that is not a regular file is refused.
func Swap(staged, target string) error {
	if info, err := os.Lstat(target); err == nil && !info.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", target)
	}
	if filepath.Dir(staged) != filepath.Dir(target) {
		return fmt.Errorf("%s is not staged beside %s", staged, target)
	}
	return os.Rename(staged, target)
}
