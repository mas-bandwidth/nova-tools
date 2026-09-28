package post

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// ghostWire is the Ghost Admin API post body.
type ghostWire struct {
	Posts []struct {
		Title  string   `json:"title"`
		HTML   string   `json:"html"`
		Status string   `json:"status"`
		Tags   []string `json:"tags,omitempty"`
	} `json:"posts"`
}

type bskyExternal struct {
	URI         string `json:"uri"`
	Title       string `json:"title"`
	Description string `json:"description"`
}

type bskyEmbed struct {
	Type     string       `json:"$type"`
	External bskyExternal `json:"external"`
}

type bskyRecord struct {
	Type      string     `json:"$type"`
	Text      string     `json:"text"`
	CreatedAt string     `json:"createdAt"`
	Embed     *bskyEmbed `json:"embed,omitempty"`
}

type bskyWire struct {
	Repo       string     `json:"repo"`
	Collection string     `json:"collection"`
	Record     bskyRecord `json:"record"`
}

type discordWire struct {
	Content string `json:"content"`
}

// Render builds the exact payload bytes a channel leaves, once, by a fixed
// deterministic function. Those bytes are everything that can leave.
func Render(c Channel, target, title, link, body string, at time.Time) ([]byte, error) {
	switch c {
	case Ghost:
		w := ghostWire{}
		p := struct {
			Title  string   `json:"title"`
			HTML   string   `json:"html"`
			Status string   `json:"status"`
			Tags   []string `json:"tags,omitempty"`
		}{Title: title, HTML: body, Status: "published"}
		if target != "" {
			p.Tags = []string{target}
		}
		w.Posts = append(w.Posts, p)
		return json.Marshal(w)
	case Bsky:
		rec := bskyRecord{
			Type:      "app.bsky.feed.post",
			Text:      body,
			CreatedAt: at.UTC().Format("2006-01-02T15:04:05.000Z"),
		}
		if link != "" {
			cardTitle := title
			if cardTitle == "" {
				cardTitle = heading(body)
			}
			rec.Embed = &bskyEmbed{
				Type: "app.bsky.embed.external",
				External: bskyExternal{
					URI:         link,
					Title:       cardTitle,
					Description: firstLine(body),
				},
			}
		}
		return json.Marshal(bskyWire{Repo: target, Collection: "app.bsky.feed.post", Record: rec})
	case Email:
		var b strings.Builder
		fmt.Fprintf(&b, "To: %s\n", target)
		fmt.Fprintf(&b, "Subject: %s\n", title)
		fmt.Fprintf(&b, "Date: %s\n", at.UTC().Format("Mon, 02 Jan 2006 15:04:05 -0700"))
		b.WriteString("MIME-Version: 1.0\n")
		b.WriteString("Content-Type: text/plain; charset=utf-8\n")
		b.WriteString("\n")
		b.WriteString(body)
		return []byte(b.String()), nil
	case Discord:
		return json.Marshal(discordWire{Content: body})
	}
	return nil, Refuse("bad-channel", "give --channel ghost, bsky, email or discord", 2)
}

// BuildDigest renders the day's cairn beats and fleet numbers for email digest
// mode. It is deterministic in its three inputs and nothing else.
func BuildDigest(date string, cairn, fleet []byte) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "# nova digest %s\n\n", date)
	b.WriteString("## cairn beats\n")
	writeBullets(&b, cairn)
	b.WriteString("\n## fleet\n")
	writeBullets(&b, fleet)
	return []byte(b.String())
}

// writeBullets writes each non-empty input line as a bullet. It is a listing
// and keeps the caller's order.
func writeBullets(b *strings.Builder, raw []byte) {
	lines := strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n")
	for _, line := range lines {
		t := strings.TrimSpace(line)
		if t == "" {
			continue
		}
		fmt.Fprintf(b, "- %s\n", t)
	}
}

// Draft renders and stores one draft. It performs no network call: the allowlist
// gate is local and the body is scanned for secrets before anything is written.
func Draft(o Options) (DraftResult, error) {
	if err := EnsureDrafts(o.Drafts); err != nil {
		return DraftResult{}, err
	}
	if err := RequireAllowlisted(o.Allowlist, o.Channel, o.Target); err != nil {
		return DraftResult{}, err
	}

	var body string
	title := o.Title
	if o.Digest != "" {
		if o.Channel != Email {
			return DraftResult{}, Refuse("digest-not-email", "--digest is email-only; use --channel email or drop --digest", 2)
		}
		cairn, err := os.ReadFile(o.Cairn)
		if err != nil {
			return DraftResult{}, Refuse("bad-cairn", fmt.Sprintf("--cairn %s is unreadable: %s", oneline.Escape(o.Cairn), oneline.Err(err)), 2)
		}
		fleet, err := os.ReadFile(o.Fleet)
		if err != nil {
			return DraftResult{}, Refuse("bad-fleet", fmt.Sprintf("--fleet %s is unreadable: %s", oneline.Escape(o.Fleet), oneline.Err(err)), 2)
		}
		body = string(BuildDigest(o.Digest, cairn, fleet))
		if title == "" {
			title = "nova digest " + o.Digest
		}
	} else {
		raw, err := os.ReadFile(o.File)
		if err != nil {
			return DraftResult{}, Refuse("bad-file", fmt.Sprintf("--file %s is unreadable: %s", oneline.Escape(o.File), oneline.Err(err)), 2)
		}
		body = string(raw)
	}
	if err := ScanBody(body); err != nil {
		return DraftResult{}, err
	}
	if o.Channel == Bsky {
		if err := requireBskyAlt(body); err != nil {
			return DraftResult{}, err
		}
	}
	payload, err := Render(o.Channel, o.Target, title, o.Link, body, o.Now)
	if err != nil {
		return DraftResult{}, err
	}
	hash := hashOf(payload)
	meta := Meta{
		Channel: string(o.Channel),
		Target:  o.Target,
		Title:   title,
		Link:    o.Link,
		Created: o.Now.UTC().Format("2006-01-02T15:04:05Z"),
		Bytes:   len(payload),
	}
	metaLine, err := json.Marshal(meta)
	if err != nil {
		return DraftResult{}, err
	}
	if err := os.WriteFile(filepath.Join(o.Drafts, hash+".post"), payload, 0o644); err != nil {
		return DraftResult{}, Refuse("write-draft", fmt.Sprintf("cannot write the payload: %s", oneline.Err(err)), 2)
	}
	if err := os.WriteFile(filepath.Join(o.Drafts, hash+".meta"), append(metaLine, '\n'), 0o644); err != nil {
		return DraftResult{}, Refuse("write-draft", fmt.Sprintf("cannot write the meta sidecar: %s", oneline.Err(err)), 2)
	}
	return DraftResult{Hash: hash, Meta: meta, Payload: payload}, nil
}

// ReadDraft loads a stored payload and its sidecar by hash.
func ReadDraft(drafts, hash string) (ShowResult, error) {
	if !validHash(hash) {
		return ShowResult{}, Refuse("bad-draft", fmt.Sprintf("--draft %s is not a payload hash; it wants the sixty-four lower-case hex digits `nova-post draft` prints", oneline.Field(hash)), 2)
	}
	payload, err := os.ReadFile(filepath.Join(drafts, hash+".post"))
	if err != nil {
		return ShowResult{}, Refuse("no-draft", fmt.Sprintf("no draft %s in %s; run `nova-post draft` first", oneline.Field(hash), oneline.Escape(drafts)), 2)
	}
	var meta Meta
	if raw, err := os.ReadFile(filepath.Join(drafts, hash+".meta")); err == nil {
		_ = json.Unmarshal(raw, &meta)
	}
	return ShowResult{Payload: payload, Meta: meta}, nil
}

// Show reads the exact payload bytes a hash names.
func Show(drafts, hash string) (ShowResult, error) {
	if err := EnsureDrafts(drafts); err != nil {
		return ShowResult{}, err
	}
	return ReadDraft(drafts, hash)
}
