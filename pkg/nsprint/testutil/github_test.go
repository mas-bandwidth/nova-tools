package testutil_test

import (
	"io"
	"net/http"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/nsprint/testutil"
	"github.com/stretchr/testify/require"
)

func TestGitHubStubStartsCleanly(t *testing.T) {
	t.Parallel()

	stub := testutil.StartGitHubStub(t)
	if stub.URL == "" {
		require.NotEqual(t, "", stub.URL, "expected non-empty stub URL")
	}
	require.Contains(t, stub.Env, "GITHUB_API_URL="+stub.URL)
	if stub.Calls() != 0 {
		require.Equal(t, 0, stub.Calls(), "expected 0 calls initially, got %d", stub.Calls())
	}
}

func TestGitHubStubInterceptsHTTPCalls(t *testing.T) {
	t.Parallel()

	// Use a dummy testing.T to verify it reports an error when called
	subT := &testing.T{}
	stub := testutil.StartGitHubStub(subT)

	resp, err := http.Get(stub.URL + "/repos/mas-bandwidth/nova-tools/pulls")
	if err != nil {
		require.NoError(t, err, "get: %v", err)
	}
	defer func() { _ = resp.Body.Close() }() // ignored: a response body opened only for reading; the read below is the report
	_, _ = io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusForbidden {
		require.Equal(t, http.StatusForbidden, resp.StatusCode, "expected status 403 Forbidden, got %d", resp.StatusCode)
	}
	if stub.Calls() != 1 {
		require.Equal(t, 1, stub.Calls(), "expected 1 recorded call, got %d", stub.Calls())
	}
	if !subT.Failed() {
		require.True(t, subT.Failed(), "expected subT to fail on HTTP call to GitHub stub")
	}
}
