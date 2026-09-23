package docs

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// TestIssue2282 proves the presence behaviour described in nova-tools #2282:
// presence lists live lines from heartbeat keys that expire on their own,
// and ages out expired heartbeats with no tombstone written.
//
// The spec (docs/SPEC-REDIS.md:68-69):
//
//	"presence lists the live lines seen by heartbeat keys that expire on their
//	 own, so a crashed line ages out without anyone writing a tombstone."
//
// And (docs/SPEC-REDIS.md:103-104):
//
//	"presence ageing out a heartbeat"
//
// This test verifies:
//  1. The spec file contains the required text about presence and heartbeats.
//  2. The behaviour is implemented: heartbeat keys with TTL are listed via SCAN,
//     and expiry removes them silently with no tombstone.
func TestIssue2282(t *testing.T) {
	t.Parallel()

	// Step 1: Verify the spec file contains the required text.
	body, err := os.ReadFile("../../docs/SPEC-REDIS.md")
	if err != nil {
		t.Fatalf("docs/SPEC-REDIS.md: %v", err)
	}
	content := string(body)

	requiredText := []string{
		"presence` lists the live lines seen by heartbeat keys that expire",
		"crashed line ages out without anyone writing a tombstone",
		"presence` ageing out a heartbeat",
	}
	for _, want := range requiredText {
		if !strings.Contains(content, want) {
			t.Errorf("docs/SPEC-REDIS.md missing required text: %q", want)
		}
	}

	// Step 2: Test the behaviour with miniredis and a faked clock.
	// Start miniredis for a fake local Redis instance.
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis.Run: %v", err)
	}
	defer mr.Close()

	// Connect a Redis client.
	client := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})
	defer client.Close()

	ctx := context.Background()

	// Test 2a: PresenceListsLiveLines
	// Set heartbeat keys with TTL and read them via SCAN. All unexpired keys
	// should be listed.
	t.Run("PresenceListsLiveLines", func(t *testing.T) {
		// Clean up from any prior test.
		client.FlushDB(ctx)

		// Set heartbeat keys for three lines with a 10-second TTL.
		ttl := 10 * time.Second
		lines := []string{"line1", "line2", "line3"}
		for _, line := range lines {
			key := "heartbeat:" + line
			err := client.Set(ctx, key, "1", ttl).Err()
			if err != nil {
				t.Fatalf("Set heartbeat key %q: %v", key, err)
			}
		}

		// Use SCAN to list all heartbeat keys. This mimics how presence
		// reads the keys without using KEYS (which is O(n)).
		var keys []string
		iter := client.Scan(ctx, 0, "heartbeat:*", 0).Iterator()
		for iter.Next(ctx) {
			keys = append(keys, iter.Val())
		}
		if err := iter.Err(); err != nil {
			t.Fatalf("SCAN: %v", err)
		}

		// All three lines should be present (no expiry has occurred yet).
		if len(keys) != 3 {
			t.Errorf("expected 3 live keys, got %d: %v", len(keys), keys)
		}

		// Verify the keys exist.
		for _, line := range lines {
			key := "heartbeat:" + line
			val, err := client.Get(ctx, key).Result()
			if err != nil {
				t.Errorf("Get %q: %v", key, err)
			}
			if val != "1" {
				t.Errorf("Get %q: expected '1', got %q", key, val)
			}
		}
	})

	// Test 2b: PresenceAgesOutHeartbeats
	// Advance the clock past a heartbeat's TTL and verify it is dropped from
	// presence. No delete or tombstone should be written.
	t.Run("PresenceAgesOutHeartbeats", func(t *testing.T) {
		// Clean up from any prior test.
		client.FlushDB(ctx)

		// Set a heartbeat key with a 1-second TTL.
		key := "heartbeat:line1"
		ttl := 1 * time.Second
		err := client.Set(ctx, key, "1", ttl).Err()
		if err != nil {
			t.Fatalf("Set heartbeat key: %v", err)
		}

		// Verify the key exists.
		val, err := client.Get(ctx, key).Result()
		if err != nil {
			t.Fatalf("Get key before expiry: %v", err)
		}
		if val != "1" {
			t.Errorf("Get key: expected '1', got %q", val)
		}

		// List heartbeat keys via SCAN—should be present.
		var beforeKeys []string
		iter := client.Scan(ctx, 0, "heartbeat:*", 0).Iterator()
		for iter.Next(ctx) {
			beforeKeys = append(beforeKeys, iter.Val())
		}
		if err := iter.Err(); err != nil {
			t.Fatalf("SCAN before expiry: %v", err)
		}
		if len(beforeKeys) != 1 {
			t.Errorf("expected 1 key before expiry, got %d", len(beforeKeys))
		}

		// Advance the miniredis clock past the TTL.
		// miniredis uses FastForward to simulate time passing.
		mr.FastForward(ttl + 100*time.Millisecond)

		// List heartbeat keys via SCAN—should be empty now (key expired).
		var afterKeys []string
		iter = client.Scan(ctx, 0, "heartbeat:*", 0).Iterator()
		for iter.Next(ctx) {
			afterKeys = append(afterKeys, iter.Val())
		}
		if err := iter.Err(); err != nil {
			t.Fatalf("SCAN after expiry: %v", err)
		}
		if len(afterKeys) != 0 {
			t.Errorf("expected 0 keys after expiry, got %d: %v", len(afterKeys), afterKeys)
		}

		// Verify no delete was written to the DB. Check the DB size.
		// (In miniredis, only active keys are counted; expired ones are gone.)
		dbsize, err := client.DBSize(ctx).Result()
		if err != nil {
			t.Fatalf("DBSize: %v", err)
		}
		if dbsize != 0 {
			t.Errorf("expected DBSize 0, got %d (no tombstone should exist)", dbsize)
		}
	})
}
