// Package line is the typed-line record machinery that is not the parser:
// the diff digest a read is taken at, and the carry of a typed line across
// an identical-diff head move (nova-tools #3630). A rebase or update-branch
// that leaves `git diff base...head` byte-identical changed nothing a
// reader read, so the line carries as a record with a carried_from receipt;
// only a changed diff queues a re-read.
package line

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strings"

	"github.com/redis/go-redis/v9"
)

// Digest is the identity of one head's diff against its base: the merge
// base of the two normalises the base merge, so a head moved by rebase or
// update-branch with the same change has the same SHA256. Files carries one
// digest per `diff --git` section, so a refusal can name what changed.
type Digest struct {
	SHA256 string
	Files  map[string]string
}

// Unit hash fields the digest is recorded in (s:<S>:u:<unit>).
const (
	FieldSHA   = "diff_sha256"
	FieldHead  = "diff_head"
	FieldFiles = "diff_files"
)

// Read record fields a carry writes (s:<S>:read:<unit>:<who>).
const (
	FieldCarriedFrom = "carried_from"
	FieldCarriedAt   = "carried_at"
)

var sectionMark = []byte("diff --git ")

// DiffDigest runs `git diff <merge-base(base, head)> <head>` in dir (a bench
// mirror or a clone; base is a ref the dir resolves, head a sha) and digests
// the sections. An empty diff is a Digest of no files with the digest of
// the empty string, never an error: an empty PR is identical to itself.
func DiffDigest(ctx context.Context, dir, base, head string) (Digest, error) {
	mb, err := gitOut(ctx, dir, "merge-base", base, head)
	if err != nil {
		return Digest{}, err
	}
	mb = strings.TrimSpace(mb)
	if mb == "" {
		return Digest{}, fmt.Errorf("no merge base of %s and %s in %s", base, head, dir)
	}
	diff, err := gitOut(ctx, dir, "diff", "--no-color", "--no-ext-diff", "--full-index", mb, head)
	if err != nil {
		return Digest{}, err
	}
	return DigestOf([]byte(diff)), nil
}

// DigestOf splits a unified diff into `diff --git` sections and digests
// each and the whole.
func DigestOf(diff []byte) Digest {
	d := Digest{Files: map[string]string{}}
	whole := sha256.New()
	rest := diff
	for len(rest) > 0 {
		i := bytes.Index(rest, sectionMark)
		if i < 0 {
			break
		}
		rest = rest[i:]
		end := bytes.Index(rest[len(sectionMark):], []byte("\n"+string(sectionMark)))
		var section []byte
		if end < 0 {
			section, rest = rest, nil
		} else {
			section, rest = rest[:len(sectionMark)+end+1], rest[len(sectionMark)+end+1:]
		}
		name := sectionFile(section)
		sum := sha256.Sum256(section)
		d.Files[name] = hex.EncodeToString(sum[:])
		whole.Write(section)
	}
	d.SHA256 = hex.EncodeToString(whole.Sum(nil))
	return d
}

// sectionFile reads the b/ path of a `diff --git a/x b/x` header.
func sectionFile(section []byte) string {
	line := section
	if i := bytes.IndexByte(line, '\n'); i >= 0 {
		line = line[:i]
	}
	s := strings.TrimPrefix(string(line), string(sectionMark))
	if i := strings.LastIndex(s, " b/"); i >= 0 {
		return s[i+3:]
	}
	return s
}

// Changed lists the files whose section differs between a and b, sorted.
func Changed(a, b Digest) []string {
	seen := map[string]bool{}
	var out []string
	for f, h := range a.Files {
		if b.Files[f] != h {
			seen[f] = true
		}
	}
	for f, h := range b.Files {
		if a.Files[f] != h {
			seen[f] = true
		}
	}
	for f := range seen {
		out = append(out, f)
	}
	sort.Strings(out)
	return out
}

// Record writes the digest onto the unit at read time: one HSET.
func Record(ctx context.Context, c redis.Cmdable, unitKey, head string, d Digest) error {
	files, err := json.Marshal(d.Files)
	if err != nil {
		return err
	}
	return c.HSet(ctx, unitKey, FieldSHA, d.SHA256, FieldHead, head, FieldFiles, string(files)).Err()
}

// recorded reads a Digest back from the unit fields; ok is false when no
// digest was recorded.
func recorded(u map[string]string) (head string, d Digest, ok bool) {
	head, d.SHA256 = u[FieldHead], u[FieldSHA]
	if head == "" || d.SHA256 == "" {
		return "", Digest{}, false
	}
	d.Files = map[string]string{}
	if raw := u[FieldFiles]; raw != "" {
		_ = json.Unmarshal([]byte(raw), &d.Files)
	}
	return head, d, true
}

func gitOut(ctx context.Context, dir string, args ...string) (string, error) {
	args = append([]string{"-C", dir}, args...)
	cmd := exec.CommandContext(ctx, "git", args...)
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(errOut.String()))
	}
	return out.String(), nil
}

// ErrNoDigest is a carry asked for a unit whose read-time digest was never
// recorded; the remedy is `read digest` at the old head, then carry.
var ErrNoDigest = errors.New("no diff digest recorded at read time")
