package redisq_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/redisq"
)

func TestDirQueueMultilineRoundTrip(t *testing.T) {
	root := t.TempDir()
	dq := &redisq.DirQueue{Root: root}
	stream := "nova:queue:test:red"
	id := "card-test-1"

	body := "RESULT: test\ninstruction line 1\nkey=value line 2\nand line 3\nwith literal \\n and \\r and C:\\path\\to\\file\r\n"
	fields := map[string]string{
		"card":     "test-card",
		"body":     body,
		"path":     `C:\tools\nova\bin`,
		"regex":    `\d+\s+\w+`,
		"affinity": "repo:mas-bandwidth/nova-tools",
	}

	cardPath, err := dq.Add(stream, id, fields)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	// Verify that the persisted file starts with the format marker.
	raw, err := os.ReadFile(cardPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.HasPrefix(string(raw), redisq.CardFormatMarker+"\n") {
		t.Fatalf("persisted card does not begin with format marker %q:\n%s", redisq.CardFormatMarker, raw)
	}

	card, err := dq.Pull(stream)
	if err != nil {
		t.Fatalf("Pull: %v", err)
	}
	if card == nil {
		t.Fatal("Pull returned nil card")
	}
	if card.ID != id {
		t.Errorf("got ID %q, want %q", card.ID, id)
	}
	for k, want := range fields {
		if got := card.Fields[k]; got != want {
			t.Fatalf("field %q round-trip failed:\ngot:  %q\nwant: %q", k, got, want)
		}
	}
}

func TestDirQueueLegacyRawRecordWithoutFormatMarker(t *testing.T) {
	root := t.TempDir()
	dq := &redisq.DirQueue{Root: root}
	stream := "nova:queue:test:legacy"
	id := "card-legacy-42"

	// Legacy pre-change card written directly to disk without a format marker.
	// Old Add wrote raw key=value pairs without # dirq:v1 header or escaping.
	streamDir := dq.DirStream(stream)
	if err := os.MkdirAll(streamDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	rawCardContent := "affinity=bench:studio\n" +
		"body=RESULT: legacy test with literal \\n not newline and \\r not cr\n" +
		"card=legacy-42\n" +
		"path=C:\\Users\\glenn\\AppData\\Local\\Temp\n" +
		"regex=\\d+\\.\\d+\\s+[a-z]+\\n\n" +
		"priority=3\n"

	cardFile := filepath.Join(streamDir, id+".card")
	if err := os.WriteFile(cardFile, []byte(rawCardContent), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	card, err := dq.Pull(stream)
	if err != nil {
		t.Fatalf("Pull: %v", err)
	}
	if card == nil {
		t.Fatal("Pull returned nil card for legacy record")
	}
	if card.ID != id {
		t.Errorf("got card ID %q, want %q", card.ID, id)
	}

	// Verify legacy raw records preserve literal backslashes byte-for-byte.
	expected := map[string]string{
		"affinity": "bench:studio",
		"body":     "RESULT: legacy test with literal \\n not newline and \\r not cr",
		"card":     "legacy-42",
		"path":     "C:\\Users\\glenn\\AppData\\Local\\Temp",
		"regex":    "\\d+\\.\\d+\\s+[a-z]+\\n",
		"priority": "3",
	}
	for k, want := range expected {
		if got := card.Fields[k]; got != want {
			t.Errorf("legacy field %q corrupted:\ngot:  %q\nwant: %q", k, got, want)
		}
	}
}

func TestDirQueuePreChangeFixture(t *testing.T) {
	root := t.TempDir()
	dq := &redisq.DirQueue{Root: root}
	stream := "nova:queue:chore:small"
	id := "card-prechange-fixture"

	streamDir := dq.DirStream(stream)
	if err := os.MkdirAll(streamDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	// Exact pre-change fixture: raw key=value pairs, no header line.
	const fixture = "body=echo \\n test\\r\\nC:\\nova\\bin\ncard=9346\npriority=1\n"
	if err := os.WriteFile(filepath.Join(streamDir, id+".card"), []byte(fixture), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	card, err := dq.Pull(stream)
	if err != nil {
		t.Fatalf("Pull: %v", err)
	}
	if card == nil {
		t.Fatal("Pull returned nil card")
	}

	const wantBody = `echo \n test\r\nC:\nova\bin`
	if got := card.Fields["body"]; got != wantBody {
		t.Fatalf("pre-change fixture body corrupted:\ngot:  %q\nwant: %q", got, wantBody)
	}
	if got := card.Fields["card"]; got != "9346" {
		t.Errorf("card = %q, want %q", got, "9346")
	}
	if got := card.Fields["priority"]; got != "1" {
		t.Errorf("priority = %q, want %q", got, "1")
	}
}

func TestDirQueueRejectsEscapedStream(t *testing.T) {
	root := t.TempDir()
	dq := &redisq.DirQueue{Root: root}
	escaped := "../../escaped:red"

	if _, err := dq.Add(escaped, "card-1", map[string]string{"card": "test"}); err == nil {
		t.Fatalf("Add with escaped stream %q expected error, got nil", escaped)
	}
	if _, err := dq.Pull(escaped); err == nil {
		t.Fatalf("Pull with escaped stream %q expected error, got nil", escaped)
	}
	if err := dq.Ack(escaped, "card-1"); err == nil {
		t.Fatalf("Ack with escaped stream %q expected error, got nil", escaped)
	}
	if _, err := dq.Reclaim(escaped, time.Minute, time.Now()); err == nil {
		t.Fatalf("Reclaim with escaped stream %q expected error, got nil", escaped)
	}
}
