package testutil_test

import (
	"io"
	"net/http"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
)

func TestGitHubStubStartsCleanly(t *testing.T) {
	stub := testutil.StartGitHubStub(t)
	if stub.URL == "" {
		t.Fatal("expected non-empty stub URL")
	}
	if stub.Calls() != 0 {
		t.Fatalf("expected 0 calls initially, got %d", stub.Calls())
	}
}

func TestGitHubStubInterceptsHTTPCalls(t *testing.T) {
	// Use a dummy testing.T to verify it reports an error when called
	subT := &testing.T{}
	stub := testutil.StartGitHubStub(subT)

	resp, err := http.Get(stub.URL + "/repos/mas-bandwidth/nova-tools/pulls")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	_, _ = io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected status 403 Forbidden, got %d", resp.StatusCode)
	}
	if stub.Calls() != 1 {
		t.Fatalf("expected 1 recorded call, got %d", stub.Calls())
	}
	if !subT.Failed() {
		t.Fatal("expected subT to fail on HTTP call to GitHub stub")
	}
}
