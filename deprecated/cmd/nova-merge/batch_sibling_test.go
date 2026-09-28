package main

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/safepath"
)

func TestParseSibling(t *testing.T) {
	t.Parallel()
	ok, err := parseSibling("serialize.go=file:///path/to/s.git@v1.16.2")
	if err != nil {
		t.Fatalf("a well-formed spec is accepted, got %v", err)
	}
	if ok.Name != "serialize.go" || ok.URL != "file:///path/to/s.git" || ok.Ref != "v1.16.2" {
		t.Errorf("got %+v", ok)
	}
	// The last @ splits url from ref, so an ssh URL keeps its user@host.
	ssh, err := parseSibling("serialize.go=git@example.test:mas-bandwidth/serialize.go.git@v1.16.2")
	if err != nil {
		t.Fatalf("an ssh URL is accepted, got %v", err)
	}
	if ssh.URL != "git@example.test:mas-bandwidth/serialize.go.git" || ssh.Ref != "v1.16.2" {
		t.Errorf("ssh parse got %+v", ssh)
	}
	if _, err := parseSibling("serialize.go=git@example.test:mas-bandwidth/serialize.go.git"); err == nil {
		t.Error("an ssh URL with no @ref must be refused")
	}
	if _, err := parseSibling("serialize.go=file:///path/to/s.git"); err == nil {
		t.Error("a spec with no @ref must be refused")
	}
	if !safepath.NameOK("serialize.go") {
		t.Error("serialize.go is the name schema clones; NameOK must admit it")
	}
}
