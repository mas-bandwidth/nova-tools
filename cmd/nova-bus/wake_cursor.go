package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/mas-bandwidth/nova-tools/pkg/tool"
)

// wakeCursor is caller-owned read progress, not a receipt (SPEC-BUS, wait;
// tla/WakeCursor.tla). Hash binds the consumed prefix, including an empty
// prefix, so in-place truncation and rewriting cannot silently skip records.
// Libraries considered: os.SameFile, io.CopyN, crypto/sha256 and encoding/json.
// Only a 64 KiB record and the hash buffer are held in memory; the prefix is
// streamed. Validation costs one pass over the consumed prefix each look.
type wakeCursor struct {
	Version  int    `json:"v"`
	Identity string `json:"file"`
	Offset   int64  `json:"offset"`
	Hash     string `json:"sha256"`
}

func (c wakeCursor) token() string {
	b, _ := json.Marshal(c) // ignored: this struct contains only integers and strings
	return base64.RawURLEncoding.EncodeToString(b)
}

func parseWakeCursor(token string) (wakeCursor, error) {
	var c wakeCursor
	b, err := base64.RawURLEncoding.DecodeString(token)
	if err == nil && len(b) <= 512 {
		dec := json.NewDecoder(bytes.NewReader(b))
		dec.DisallowUnknownFields()
		err = dec.Decode(&c)
		if err == nil {
			var extra any
			if dec.Decode(&extra) != io.EOF {
				err = fmt.Errorf("trailing cursor data")
			}
		}
	} else {
		err = fmt.Errorf("invalid cursor encoding")
	}
	hash, hashErr := hex.DecodeString(c.Hash)
	if err != nil || c.Version != 1 || c.Offset < 0 || hashErr != nil || len(hash) != sha256.Size || (c.Identity == "" && c.Offset != 0) {
		return c, fmt.Errorf("--wake-after wants the complete wake-after cursor printed by wait; run: nova-bus help wait")
	}
	return c, nil
}

// wakeFile refuses pipes before open, and binds the handle to the path across
// opening. A disappeared previously bound file is a visible rotation failure.
func wakeFile(path string, c wakeCursor) (*os.File, os.FileInfo, error) {
	return wakeFileWithOpen(path, c, openSafeWake)
}

// wakeFileWithOpen exposes the stat/open race to a deterministic test.
func wakeFileWithOpen(path string, c wakeCursor, open func(string) (*os.File, error)) (*os.File, os.FileInfo, error) {
	fi, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) && c.Identity == "" {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, nil, fmt.Errorf("wake file wants a seekable regular file, not mode %s", fi.Mode())
	}
	f, err := open(path)
	if err != nil {
		return nil, nil, err
	}
	opened, err := f.Stat()
	if err == nil && !opened.Mode().IsRegular() {
		err = fmt.Errorf("wake file wants a seekable regular file after open, not mode %s", opened.Mode())
	}
	if err == nil && !os.SameFile(fi, opened) {
		err = fmt.Errorf("wake file replaced while opening")
	}
	if err == nil {
		var identity string
		identity, err = wakeIdentity(f, opened)
		if err == nil && c.Identity != "" && c.Identity != identity {
			err = fmt.Errorf("wake file replaced; saved cursor belongs to another file")
		}
	}
	if err == nil && opened.Size() < c.Offset {
		err = fmt.Errorf("wake file truncated below saved byte offset %d", c.Offset)
	}
	if err != nil {
		_ = f.Close() // ignored: read-only handle, the validation error is the result
		return nil, nil, err
	}
	return f, opened, nil
}

func realWakeArm(path, token string) (wakeCursor, error) {
	c := wakeCursor{Version: 1, Hash: fmt.Sprintf("%x", sha256.Sum256(nil))}
	var err error
	if token != "" && token != "0" {
		c, err = parseWakeCursor(token)
		if err != nil {
			return c, err
		}
	}
	f, fi, err := wakeFile(path, c)
	if err != nil || f == nil {
		return c, err
	}
	defer f.Close() // ignored: read-only handle
	if token == "" {
		c.Offset = fi.Size()
	}
	h := sha256.New()
	if _, err = io.CopyN(h, f, c.Offset); err != nil {
		return c, err
	}
	digest := hex.EncodeToString(h.Sum(nil))
	if token != "" && digest != c.Hash {
		return c, fmt.Errorf("wake file consumed prefix changed; saved cursor is stale")
	}
	c.Identity, err = wakeIdentity(f, fi)
	if err != nil {
		return c, err
	}
	c.Hash = digest
	current, err := os.Stat(path)
	if err != nil {
		return c, err
	}
	if !os.SameFile(fi, current) {
		return c, fmt.Errorf("wake file replaced while arming; saved cursor is stale")
	}
	return c, nil
}

// realWakeLine advances only through a complete line actually returned. A bus
// return does not call it again or move this cursor. Restarts re-use its token.
func realWakeLine(path string, c wakeCursor) (string, wakeCursor, error) {
	f, fi, err := wakeFile(path, c)
	if err != nil || f == nil {
		return "", c, err
	}
	defer f.Close() // ignored: read-only handle
	h := sha256.New()
	if _, err = io.CopyN(h, f, c.Offset); err != nil {
		return "", c, err
	}
	if hex.EncodeToString(h.Sum(nil)) != c.Hash {
		return "", c, fmt.Errorf("wake file consumed prefix changed; saved cursor is stale")
	}
	raw, err := io.ReadAll(io.LimitReader(f, wakeLineMax))
	if err != nil {
		return "", c, err
	}
	current, err := os.Stat(path)
	if err != nil {
		return "", c, err
	}
	if !os.SameFile(fi, current) {
		return "", c, fmt.Errorf("wake file replaced while reading; saved cursor is stale")
	}
	c.Identity, err = wakeIdentity(f, fi)
	if err != nil {
		return "", c, err
	}
	if i := bytes.IndexByte(raw, '\n'); i >= 0 {
		_, _ = h.Write(raw[:i+1]) // ignored: hash.Write always succeeds
		c.Offset += int64(i + 1)
		c.Hash = hex.EncodeToString(h.Sum(nil))
		return string(raw[:i]), c, nil
	}
	if len(raw) == wakeLineMax {
		return "", c, fmt.Errorf("wake record wants a newline within %d bytes", wakeLineMax)
	}
	return "", c, nil
}

func wakeRefusal(err error) *tool.Out {
	return tool.Refuse("the wake file cursor cannot be used: " + err.Error() + "; retain unread records and reconcile the file before re-arming without --wake-after; run: nova-bus help wait")
}
func wakeToken(path string, c wakeCursor) string {
	if path == "" {
		return ""
	}
	return c.token()
}
func wakeOffset(path string, c wakeCursor) *int64 {
	if path == "" {
		return nil
	}
	return &c.Offset
}
func wakeFacts(o *tool.Out, path string, c wakeCursor) *tool.Out {
	if path != "" {
		o.Fact("wake-after", c.token()).Fact("wake-offset", c.Offset)
	}
	return o
}
