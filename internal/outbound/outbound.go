/*
Package outbound is the outward gate of docs/SPEC-OUTBOUND.md: a tool drafts and
shows, and releases only on a bus receipt that is Glenn's, names the exact hash,
and is fresh. Slice 1 carries the core verbs against one fake in-process channel;
the real Bluesky, Ghost, email and Discord adapters arrive in later slices and
implement the same Channel interface. No test here opens a socket or reads a
credential.
*/
package outbound

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// ApprovalWindow is how long a receipt stays fresh. A receipt received at or
// before now-ApprovalWindow is stale: "under 24 hours" is the rule, so 24 hours
// on the dot is refused.
const ApprovalWindow = 24 * time.Hour

// approvalLine is the one body line an approval receipt must carry.
const approvalLine = "APPROVE nova-post sha256="

// Sender is the only participant whose receipt releases a send.
const Sender = "Glenn"

// Clock is the injected time source. There is deliberately no --now flag: a
// caller who can name the time can forge freshness.
type Clock interface{ Now() time.Time }

// SystemClock is the production clock.
type SystemClock struct{}

// Now returns the wall clock in UTC.
func (SystemClock) Now() time.Time { return time.Now().UTC() }

// Result is what a channel's Send returns: the provider's own identifiers, empty
// when the channel has neither (email).
type Result struct {
	ID  string
	URL string
}

// RenderRequest is the material draft renders a payload from.
type RenderRequest struct {
	Target string
	Body   []byte
	Title  string
	Link   string
}

// Channel is one outward provider. Draft renders the payload once, by a fixed
// deterministic function; Send transmits bytes it is handed, never freshly
// rendered ones.
type Channel interface {
	Name() string
	Render(RenderRequest) ([]byte, error)
	Send(target string, payload []byte) (Result, error)
}

// FakeChannel is the one in-process channel of slice 1. It records every send
// so a test can prove a refusal made zero requests, and its render is the body
// verbatim, which makes show/send byte-identity checkable without a provider.
type FakeChannel struct {
	mu       sync.Mutex
	requests [][]byte
	targets  []string
	result   Result
	err      error
}

// NewFakeChannel returns an empty fake channel.
func NewFakeChannel() *FakeChannel { return &FakeChannel{} }

// Name is the value --channel takes for this channel.
func (f *FakeChannel) Name() string { return "fake" }

// Render is the identity: the fake channel's payload is the body bytes.
func (f *FakeChannel) Render(req RenderRequest) ([]byte, error) { return req.Body, nil }

// Send records the call and returns the configured result.
func (f *FakeChannel) Send(target string, payload []byte) (Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return Result{}, f.err
	}
	cp := append([]byte(nil), payload...)
	f.requests = append(f.requests, cp)
	f.targets = append(f.targets, target)
	return f.result, nil
}

// Requests returns a copy of every payload sent, in order.
func (f *FakeChannel) Requests() [][]byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([][]byte, len(f.requests))
	for i, r := range f.requests {
		out[i] = append([]byte(nil), r...)
	}
	return out
}

// SetResult configures what Send returns.
func (f *FakeChannel) SetResult(r Result) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.result = r
}

// SetError makes Send fail with err.
func (f *FakeChannel) SetError(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err = err
}

// Refusal is a one-line refusal with the exit code the spec assigns it: 2 when
// the invocation could not run, 1 when the bus or a provider says NO.
type Refusal struct {
	Code   int
	Reason string
	Detail string
}

// Error lets a Refusal travel as an error.
func (r *Refusal) Error() string { return r.Detail }

// Refuse builds a Refusal.
func Refuse(code int, reason, detail string) *Refusal {
	return &Refusal{Code: code, Reason: reason, Detail: detail}
}

// Hash is the lower-case SHA-256 of the payload bytes.
func Hash(payload []byte) string {
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

// Draft is one stored draft. The payload bytes are the draft store's .post file;
// everything else is the .meta line.
type Draft struct {
	Hash    string
	Channel string
	Target  string
	Title   string
	Link    string
	Created time.Time
	Bytes   int
}

// PostPath is the payload file for a hash.
func PostPath(draftsDir, hash string) string { return filepath.Join(draftsDir, hash+".post") }

// MetaPath is the metadata file for a hash.
func MetaPath(draftsDir, hash string) string { return filepath.Join(draftsDir, hash+".meta") }

// SentPath is the send record for a hash.
func SentPath(draftsDir, hash string) string { return filepath.Join(draftsDir, hash+".sent") }

// Save writes the payload bytes verbatim as <hash>.post and a one-line
// <hash>.meta holding channel, target, title, link, created and bytes. It
// creates no directory: --drafts must already exist.
func Save(draftsDir string, d Draft, payload []byte) error {
	if err := os.WriteFile(PostPath(draftsDir, d.Hash), payload, 0o644); err != nil {
		return err
	}
	meta := fmt.Sprintf("channel=%s target=%s title=%s link=%s created=%s bytes=%d\n",
		oneline.Field(d.Channel),
		oneline.Field(d.Target),
		oneline.Field(d.Title),
		oneline.Field(d.Link),
		d.Created.UTC().Format(time.RFC3339),
		len(payload),
	)
	return os.WriteFile(MetaPath(draftsDir, d.Hash), []byte(meta), 0o644)
}

// Load reads a draft's meta and payload. A missing payload or meta is a Refusal
// with exit 2: the invocation could not run.
func Load(draftsDir, hash string) (Draft, []byte, error) {
	raw, err := os.ReadFile(MetaPath(draftsDir, hash))
	if err != nil {
		return Draft{}, nil, Refuse(2, "bad-draft", fmt.Sprintf("no draft %s in %s; run draft first", oneline.Field(hash), oneline.Field(draftsDir)))
	}
	payload, err := os.ReadFile(PostPath(draftsDir, hash))
	if err != nil {
		return Draft{}, nil, Refuse(2, "bad-draft", fmt.Sprintf("draft %s has no payload in %s; run draft again", oneline.Field(hash), oneline.Field(draftsDir)))
	}
	d := Draft{Hash: hash, Bytes: len(payload)}
	for _, field := range strings.Fields(string(raw)) {
		k, v, ok := strings.Cut(field, "=")
		if !ok {
			continue
		}
		switch k {
		case "channel":
			d.Channel = v
		case "target":
			d.Target = v
		case "title":
			d.Title = v
		case "link":
			d.Link = v
		case "created":
			if t, perr := time.Parse(time.RFC3339, v); perr == nil {
				d.Created = t
			}
		}
	}
	return d, payload, nil
}

// Allowed reads an allowlist of one `channel<TAB>target` per line and refuses a
// channel/target pair not named in it. A missing allowlist file is a refusal
// naming the file to add.
func Allowed(allowlistPath, channel, target string) error {
	raw, err := os.ReadFile(allowlistPath)
	if err != nil {
		return Refuse(1, "target-not-allowed", fmt.Sprintf("allowlist %s is absent; create it with one channel<TAB>target per line", oneline.Field(allowlistPath)))
	}
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		if fields[0] == channel && fields[1] == target {
			return nil
		}
	}
	return Refuse(1, "target-not-allowed", fmt.Sprintf("target %s is not in %s; add %q", oneline.Field(target), oneline.Field(allowlistPath), channel+"\t"+target))
}

// Receipt is a bus-shaped approval receipt: a note with From/Date header lines
// and a body whose one line is `APPROVE nova-post sha256=<hash>`.
type Receipt struct {
	ID     string
	Sender string
	Body   string
	Time   time.Time
}

// ReadReceipt finds the receipt named by id under busDir, searching lanes (files
// directly under busDir and in immediate subdirectories). The file's basename
// and its Id header both name it, so a receipt is addressable either way.
func ReadReceipt(busDir, id string) (Receipt, error) {
	if id == "" {
		return Receipt{}, fmt.Errorf("no receipt id given")
	}
	var paths []string
	err := filepath.WalkDir(busDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		paths = append(paths, path)
		return nil
	})
	if err != nil {
		return Receipt{}, err
	}
	sort.Strings(paths)
	for _, path := range paths {
		raw, rerr := os.ReadFile(path)
		if rerr != nil {
			continue
		}
		r, perr := parseReceipt(filepath.Base(path), string(raw))
		if perr != nil {
			continue
		}
		if filepath.Base(path) == id || r.ID == id {
			return r, nil
		}
	}
	return Receipt{}, fmt.Errorf("receipt %q not found in %s", id, busDir)
}

// parseReceipt parses a bus-shaped note: header lines until the first blank
// line, then the body. Date accepts RFC 3339 or the bus's own Date layout.
func parseReceipt(name, text string) (Receipt, error) {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	lines := strings.Split(text, "\n")
	r := Receipt{ID: name}
	i := 0
	for ; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			i++
			break
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			return Receipt{}, fmt.Errorf("receipt %s has a header line that is not Key: value", name)
		}
		value = strings.TrimSpace(value)
		switch strings.TrimSpace(key) {
		case "From":
			r.Sender = value
		case "Id":
			if value != "" {
				r.ID = value
			}
		case "Date":
			if t, ok := parseStamp(value); ok {
				r.Time = t
			}
		}
	}
	r.Body = strings.Join(lines[i:], "\n")
	return r, nil
}

// parseStamp reads either the bus's Date line or an RFC 3339 timestamp.
func parseStamp(value string) (time.Time, bool) {
	for _, layout := range []string{time.RFC3339, "Mon Jan  2 15:04:05 UTC 2006", "Mon Jan 2 15:04:05 UTC 2006"} {
		if t, err := time.Parse(layout, value); err == nil {
			return t.UTC(), true
		}
	}
	return time.Time{}, false
}

// namesHash reports whether the body carries the one approval line for hash.
func namesHash(body, hash string) bool {
	want := approvalLine + hash
	for _, line := range strings.Split(body, "\n") {
		if strings.TrimSpace(line) == want {
			return true
		}
	}
	return false
}

// CheckApproval applies the four-part gate in the order the spec lists, naming
// the one that failed: the receipt exists, its sender is Glenn, its body names
// this hash, and it is under 24 hours old at send time.
func CheckApproval(busDir, id, hash string, now time.Time) error {
	r, err := ReadReceipt(busDir, id)
	if err != nil {
		return Refuse(1, "no-approval", fmt.Sprintf("no receipt %s in %s; Glenn must record one on the bus, then send again", oneline.Field(id), oneline.Field(busDir)))
	}
	if r.Sender != Sender {
		return Refuse(1, "approval-sender", fmt.Sprintf("receipt %s is from %s, not %s; only %s may approve an outward send", oneline.Field(id), oneline.Field(r.Sender), Sender, Sender))
	}
	if !namesHash(r.Body, hash) {
		return Refuse(1, "approval-hash", fmt.Sprintf("receipt %s does not name hash %s; approve this exact draft", oneline.Field(id), oneline.Field(hash)))
	}
	if now.Sub(r.Time) >= ApprovalWindow {
		return Refuse(1, "approval-stale", fmt.Sprintf("receipt %s is 24 hours or older; ask %s for a fresh approval", oneline.Field(id), Sender))
	}
	return nil
}

// Send applies the gate, then transmits the stored payload and writes the
// <hash>.sent record. A sent hash is a no-op: the recorded result is returned
// and no channel call is made.
func Send(draftsDir, busDir, approval, hash string, now time.Time, ch Channel) (line string, alreadySent bool, err error) {
	d, payload, err := Load(draftsDir, hash)
	if err != nil {
		return "", false, err
	}
	if raw, rerr := os.ReadFile(SentPath(draftsDir, hash)); rerr == nil {
		return strings.TrimRight(string(raw), "\n"), true, nil
	}
	if err := CheckApproval(busDir, approval, hash, now); err != nil {
		return "", false, err
	}
	res, serr := ch.Send(d.Target, payload)
	if serr != nil {
		return "", false, Refuse(1, "provider-error", oneline.Cap("channel "+ch.Name()+" refused: "+serr.Error()+"; nothing was retried", oneline.TailBytes))
	}
	id, url := res.ID, res.URL
	if id == "" {
		id = "-"
	}
	if url == "" {
		url = "-"
	}
	line = fmt.Sprintf("POST OK channel=%s id=%s url=%s hash=%s approval=%s bytes=%d",
		oneline.Field(d.Channel), oneline.Field(id), oneline.Field(url), oneline.Field(hash), oneline.Field(approval), len(payload))
	if werr := os.WriteFile(SentPath(draftsDir, hash), []byte(line+"\n"), 0o644); werr != nil {
		return "", false, Refuse(2, "bad-drafts", fmt.Sprintf("cannot write send record in %s: %s", oneline.Field(draftsDir), oneline.Err(werr)))
	}
	return line, false, nil
}
