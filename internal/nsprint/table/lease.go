// Package table parses and renders the nova-sprint table from the single
// consistent snapshot returned by the ns_snapshot Redis Function. One call,
// one server instant: section 6 of #2756.
//
// This file holds the renderer lease (#3045): the key lease:table:<out> lets
// exactly one renderer write a given output path, so a second renderer on the
// same --out is refused. Publish writes the body through a temp file then
// renames it into place, so a reader never observes a partially written table.
package table

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/redis/go-redis/v9"
)

// LeaseKey is the lease key for one output path (spec: lease:table:<out>).
func LeaseKey(out string) string { return "lease:table:" + out }

// Renderer renders the table to one output path under a lease. Two renderers
// aimed at the same --out cannot both hold the lease: the second Acquire
// refuses.
type Renderer struct {
	client *redis.Client
	out    string
	ttl    time.Duration
	token  string
}

// NewRenderer builds a renderer for out. ttl bounds the lease so a crashed
// renderer releases the path on its own.
func NewRenderer(client *redis.Client, out string, ttl time.Duration) *Renderer {
	return &Renderer{client: client, out: out, ttl: ttl}
}

// Acquire takes the lease. It reports false without error when another
// renderer holds it, which is the refusal a second renderer turns into exit 1.
func (r *Renderer) Acquire(ctx context.Context) (bool, error) {
	if r == nil || r.client == nil {
		return false, fmt.Errorf("renderer: nil client")
	}
	token, err := newToken()
	if err != nil {
		return false, err
	}
	ok, err := r.client.SetNX(ctx, LeaseKey(r.out), token, r.ttl).Result()
	if err != nil {
		return false, fmt.Errorf("renderer lease %s: %w", r.out, err)
	}
	if !ok {
		return false, nil
	}
	r.token = token
	return true, nil
}

// Release drops the lease, but only if this renderer still holds it.
func (r *Renderer) Release(ctx context.Context) error {
	if r == nil || r.client == nil || r.token == "" {
		return nil
	}
	held, err := r.client.Get(ctx, LeaseKey(r.out)).Result()
	if err != nil {
		if err == redis.Nil {
			return nil
		}
		return fmt.Errorf("renderer release %s: %w", r.out, err)
	}
	if held != r.token {
		return nil
	}
	if err := r.client.Del(ctx, LeaseKey(r.out)).Err(); err != nil {
		return fmt.Errorf("renderer release %s: %w", r.out, err)
	}
	r.token = ""
	return nil
}

// Publish writes body to the renderer's output path atomically: a temp file in
// the same directory, then a rename, so the path is never observed partially
// written.
func (r *Renderer) Publish(body string) error {
	if r == nil || r.out == "" {
		return fmt.Errorf("renderer: no output path")
	}
	file, err := os.CreateTemp(filepath.Dir(r.out), "."+filepath.Base(r.out)+".tmp-")
	if err != nil {
		return err
	}
	name := file.Name()
	defer os.Remove(name)
	if _, err := io.WriteString(file, body); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Chmod(0o644); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(name, r.out)
}

func newToken() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("renderer token: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}
