package launch

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadEnvFileReadsThePlaysCardEnv(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := filepath.Join(dir, "card.env")
	if err := os.WriteFile(p, []byte("# the bench's card environment\nexport NOVA_CARD_BENCH=hetzner\nexport NOVA_CARD_JOBS=\"/home/nova/nova-bench/card-jobs\"\n\nNOVA_CARD_CLOCK='30m'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := ReadEnvFile(p)
	if err != nil {
		t.Fatal(err)
	}
	want := "NOVA_CARD_BENCH=hetzner NOVA_CARD_JOBS=/home/nova/nova-bench/card-jobs NOVA_CARD_CLOCK=30m"
	if strings.Join(got, " ") != want {
		t.Fatalf("got %q, want %q", strings.Join(got, " "), want)
	}
	if err := os.WriteFile(p, []byte("export NOVA_CARD_BENCH hetzner\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadEnvFile(p); err == nil || !strings.Contains(err.Error(), "card.env:1") {
		t.Fatalf("a line without = is read, err=%v", err)
	}
}
