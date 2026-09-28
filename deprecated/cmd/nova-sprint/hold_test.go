package main

// Controls of nova-tools #3092 rev 7 on the Functions harness: a throwaway
// redis-server with the real nova_sprint library (fn.Load), driven through
// the real verbs via run(...). Records are keyed by the #3139 unit contract;
// each PR is seeded as a unit through the real writer ns_unit_head.

import (
	"os"
	"time"
)

func holdWait() time.Duration {
	if v := os.Getenv("NOVA_TEST_WAIT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return 30 * time.Second
}
