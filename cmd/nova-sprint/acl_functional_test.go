//go:build functional

package main

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

// The play's shape end to end on a throwaway redis-server: users.acl written
// from the mirror rows (`user <name> on #<sha256> <rules>`, default off), the
// verb reads ACL LIST as admin and finds no drift; a hand ACL SETUSER is
// drift; ACL LOAD of the same users.acl (the play's apply) converges it.
func TestACLCheckAgainstALiveStore(t *testing.T) {
	f, err := os.Open(aclRowsFixture)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("pw"))
	hash := hex.EncodeToString(sum[:])
	var b strings.Builder
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
	f.Close()
	aclFile := filepath.Join(t.TempDir(), "users.acl")
	if err := os.WriteFile(aclFile, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	addr := testutil.Start(t, "--aclfile", aclFile)
	t.Setenv("NS_ADMIN", "pw")

	check := func() (int, string, string) {
		return runSprint("acl", "check", "--redis", addr, "--rows", aclRowsFixture)
	}
	if code, stdout, stderr := check(); code != 0 || stderr != "" || !strings.HasPrefix(stdout, "ACL CHECK OK ") {
		t.Fatalf("fresh store: exit %d stdout %q stderr %q", code, stdout, stderr)
	}

	ctx := context.Background()
	c := redis.NewClient(&redis.Options{Addr: addr, Username: "admin", Password: "pw"})
	defer c.Close()
	if err := c.Do(ctx, "ACL", "SETUSER", "bench", "-@write", "~stray:*").Err(); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := check()
	want := `ACL DRIFT user=bench missing="+@write" extra="-@write ~stray:*"`
	if code != 1 || stderr != "" || !strings.HasPrefix(stdout, want+"\n") || !strings.Contains(stdout, "ACL CHECK DRIFT ") {
		t.Fatalf("hand edit: exit %d stdout %q stderr %q; want %s", code, stdout, stderr, want)
	}

	if err := c.Do(ctx, "ACL", "LOAD").Err(); err != nil {
		t.Fatal(err)
	}
	if code, stdout, stderr := check(); code != 0 || stderr != "" || !strings.HasPrefix(stdout, "ACL CHECK OK ") {
		t.Fatalf("after ACL LOAD: exit %d stdout %q stderr %q", code, stdout, stderr)
	}
}
