package docs

import (
	"bytes"
	"os"
	"testing"
)

func TestIssue2281(t *testing.T) {
	spec, err := os.ReadFile("../../docs/SPEC-REDIS.md")
	if err != nil {
		t.Fatalf("reading spec: %v", err)
	}

	// This is the text we will add to the spec.
	// The test will fail until this text is present.
	want := []byte("Bind nova-redis to localhost and the tailnet, take auth from nova-secrets, and keep persistence off")

	if !bytes.Contains(spec, want) {
		t.Errorf("spec does not contain %q", want)
	}
}
