package sandbox

import (
	"runtime"
	"strings"
	"testing"
)

// nova-tools #893: on Linux the sandbox always reads the system roots the
// resolver and TLS need. Do not run landlock here; assert the policy object.
func TestLinuxDefaultsIncludeSystemReads(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skipf("skipped on %s: system reads are the linux default", runtime.GOOS)
	}
	write, read, home, _ := scratch(t)
	p, bad := Build(in(t, write, read, home, anExecutable(t)))
	if len(bad) > 0 {
		t.Fatalf("refused: %v", bad)
	}
	joined := "\n" + strings.Join(p.Reads, "\n") + "\n"
	for _, want := range []string{"/etc", "/usr"} {
		if !strings.Contains(joined, "\n"+want+"\n") {
			t.Fatalf("default policy read roots %v do not list %s; the resolver and TLS need it", p.Reads, want)
		}
	}
	if len(p.Reads) < 2 {
		t.Fatalf("read=%d does not include the system roots", len(p.Reads))
	}
}

func TestNoSystemReadsOmitsSystemRoots(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skipf("skipped on %s: system reads are the linux default", runtime.GOOS)
	}
	write, read, home, _ := scratch(t)
	iv := in(t, write, read, home, anExecutable(t))
	iv.NoSystemReads = true
	p, bad := Build(iv)
	if len(bad) > 0 {
		t.Fatalf("refused: %v", bad)
	}
	for _, r := range p.Reads {
		if r == "/etc" || r == "/usr" {
			t.Fatalf("with NoSystemReads the policy still lists %s: %v", r, p.Reads)
		}
	}
	if len(p.Reads) != 1 {
		t.Fatalf("with NoSystemReads read=%d, want the caller's 1", len(p.Reads))
	}
}
