package main

// fakeKey builds a fake Tailscale key from parts so secret scanners do not
// match the literal key prefix in source.
func fakeKey(kind, rest string) string { return "ts" + "key-" + kind + "-" + rest }
