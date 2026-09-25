//go:build !darwin && !linux

package life

// freeBytes cannot measure here; the beat carries no disk fact and preflight
// reads it as MISSING.
func freeBytes(string) (uint64, bool) { return 0, false }
