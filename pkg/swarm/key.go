package swarm

import (
	"fmt"
	"os"
	"strings"
)

// SecretFromEnv reads the variable a description names as its `secret` from this
// process's own environment. `nova-secrets exec` is what sets it (docs/SPEC-SECRETS.md,
// the second caller); the value is never written to a file, never printed, and never in a
// RUN or SUPERVISE line -- the refusal below names the VARIABLE and the remedy, never the
// value.
func SecretFromEnv(name string) (string, error) {
	v := os.Getenv(name)
	if strings.TrimSpace(v) == "" {
		return "", fmt.Errorf("the worker description's secret %s is absent or empty in this run's environment; the value is delivered by `nova-secrets exec`, which sets it -- run this binary under `nova-secrets exec --only %s -- <this command>` (or set %s by hand); the value is never a file", name, name, name)
	}
	return v, nil
}

// redactedReason is an error's text with the key file's own CONTENT impossible in it: an
// os error carries the path and the errno and never the bytes, and this function is where
// that claim is made once rather than assumed at four call sites.
func redactedReason(err error) string {
	if err == nil {
		return "<nil>"
	}
	if pe, ok := err.(*os.PathError); ok {
		return pe.Err.Error()
	}
	return err.Error()
}
