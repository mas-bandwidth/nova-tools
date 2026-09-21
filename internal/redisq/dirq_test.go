package redisq_test

import (
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/redisq"
)

func TestDirQueueMultilineRoundTrip(t *testing.T) {
	root := t.TempDir()
	dq := &redisq.DirQueue{Root: root}
	stream := "nova:queue:test:red"
	id := "card-test-1"

	body := "RESULT: test\ninstruction line 1\nkey=value line 2\nand line 3\n"
	fields := map[string]string{
		"card": "test-card",
		"body": body,
	}

	if _, err := dq.Add(stream, id, fields); err != nil {
		t.Fatalf("Add: %v", err)
	}

	card, err := dq.Pull(stream)
	if err != nil {
		t.Fatalf("Pull: %v", err)
	}
	if card == nil {
		t.Fatal("Pull returned nil card")
	}
	if card.Fields["body"] != body {
		t.Fatalf("body round-trip failed:\ngot:  %q\nwant: %q", card.Fields["body"], body)
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
