//go:build functional

package acl

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
)

// readLiveServer queries INFO server, ACL LIST, and ACL CAT across all categories.
func readLiveServer(ctx context.Context, c *redis.Client) (Server, error) {
	var info *redis.StringCmd
	var list, cats *redis.Cmd
	if _, err := c.Pipelined(ctx, func(p redis.Pipeliner) error {
		info = p.Info(ctx, "server")
		list = p.Do(ctx, "ACL", "LIST")
		cats = p.Do(ctx, "ACL", "CAT")
		return nil
	}); err != nil {
		return Server{}, err
	}
	lines, err := list.StringSlice()
	if err != nil {
		return Server{}, err
	}
	names, err := cats.StringSlice()
	if err != nil {
		return Server{}, err
	}
	s := Server{List: lines, Cats: Cats{}}
	for _, line := range strings.Split(info.Val(), "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "redis_version:"); ok {
			s.Version = v
		}
	}
	members := make(map[string]*redis.Cmd, len(names))
	if _, err := c.Pipelined(ctx, func(p redis.Pipeliner) error {
		for _, cat := range names {
			members[cat] = p.Do(ctx, "ACL", "CAT", cat)
		}
		return nil
	}); err != nil {
		return Server{}, err
	}
	for cat, cmd := range members {
		m, err := cmd.StringSlice()
		if err != nil {
			return Server{}, err
		}
		s.Cats[cat] = m
	}
	return s, nil
}

// TestLiveStoreACLDriftAndConvergence tests live Redis ACL against declared rows,
// detecting drift when users are modified or added/deleted, and verifying convergence
// via ACL LOAD.
func TestLiveStoreACLDriftAndConvergence(t *testing.T) {
	t.Parallel()

	f, err := os.Open("testdata/acl-rows.tsv")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	declared, err := ParseRows(f)
	if err != nil {
		t.Fatal(err)
	}

	sum := sha256.Sum256([]byte("pw"))
	hash := hex.EncodeToString(sum[:])
	var b strings.Builder
	if _, err := f.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		name, rules, ok := strings.Cut(sc.Text(), "\t")
		if !ok || strings.HasPrefix(name, "#") {
			continue
		}
		if name == "default" {
			b.WriteString("user default " + rules + "\n")
			continue
		}
		b.WriteString("user " + name + " on #" + hash + " " + rules + "\n")
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}

	aclFile := filepath.Join(t.TempDir(), "users.acl")
	if err := os.WriteFile(aclFile, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}

	addr := testutil.Start(t, "--aclfile", aclFile)
	ctx := context.Background()
	c := redis.NewClient(&redis.Options{Addr: addr, Username: "admin", Password: "pw"})
	t.Cleanup(func() { _ = c.Close() })

	// Initial state: loaded directly from declared rows, expect zero drift.
	s, err := readLiveServer(ctx, c)
	if err != nil {
		t.Fatalf("reading server: %v", err)
	}
	live, err := ParseList(s.List)
	if err != nil {
		t.Fatalf("parsing live ACL list: %v", err)
	}
	drift := Diff(declared, live, s.Cats)
	if len(drift) != 0 {
		t.Fatalf("fresh server want 0 drift, got:\n%s", lines(drift))
	}

	// Apply hand edits to create multiple drift conditions:
	// 1. bench: remove +hset and add stray key pattern
	if err := c.Do(ctx, "ACL", "SETUSER", "bench", "-hset", "~stray:*").Err(); err != nil {
		t.Fatal(err)
	}
	// 2. viewer: disable user (state drift)
	if err := c.Do(ctx, "ACL", "SETUSER", "viewer", "off").Err(); err != nil {
		t.Fatal(err)
	}
	// 3. ghost: undeclared extra user
	if err := c.Do(ctx, "ACL", "SETUSER", "ghost", "on", "nopass", "~*", "+@all").Err(); err != nil {
		t.Fatal(err)
	}
	// 4. ns-deploy: delete declared user
	if err := c.Do(ctx, "ACL", "DELUSER", "ns-deploy").Err(); err != nil {
		t.Fatal(err)
	}

	s, err = readLiveServer(ctx, c)
	if err != nil {
		t.Fatalf("reading edited server: %v", err)
	}
	live, err = ParseList(s.List)
	if err != nil {
		t.Fatalf("parsing live ACL list after edits: %v", err)
	}
	drift = Diff(declared, live, s.Cats)
	if len(drift) != 4 {
		t.Fatalf("want 4 drifted users, got %d:\n%s", len(drift), lines(drift))
	}

	driftMap := make(map[string]Drift)
	for _, d := range drift {
		driftMap[d.User] = d
	}

	if d, ok := driftMap["bench"]; !ok || strings.Join(d.Missing, " ") != "+hset" || strings.Join(d.Extra, " ") != "~stray:*" {
		t.Errorf("bench drift: got %+v", d)
	}
	if d, ok := driftMap["viewer"]; !ok || d.State != "off" {
		t.Errorf("viewer drift: got %+v", d)
	}
	if d, ok := driftMap["ghost"]; !ok || d.Absent != "declared" {
		t.Errorf("ghost drift: got %+v", d)
	}
	if d, ok := driftMap["ns-deploy"]; !ok || d.Absent != "live" {
		t.Errorf("ns-deploy drift: got %+v", d)
	}

	// Converge via ACL LOAD
	if err := c.Do(ctx, "ACL", "LOAD").Err(); err != nil {
		t.Fatalf("ACL LOAD: %v", err)
	}

	s, err = readLiveServer(ctx, c)
	if err != nil {
		t.Fatalf("reading converged server: %v", err)
	}
	live, err = ParseList(s.List)
	if err != nil {
		t.Fatalf("parsing live ACL list after convergence: %v", err)
	}
	drift = Diff(declared, live, s.Cats)
	if len(drift) != 0 {
		t.Fatalf("after ACL LOAD want 0 drift, got:\n%s", lines(drift))
	}
}
