//go:build functional

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/friend"
)

// The daemon's view is the server's GET /api/view/worker?as=<friend>, its answer whole; a
// refusal is an error naming the status and the server's words.
func TestSprintViewReadsTheWorkerView(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/view/worker" || r.URL.Query().Get("as") != "friend-b" {
			http.Error(w, "as=<name> names one fleet member or friend", http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(`{"view":"worker","as":"friend-b","kind":"friend","cards":[]}`))
	}))
	defer srv.Close()
	addr := strings.TrimPrefix(srv.URL, "http://")
	out, err := sprintView(context.Background(), addr, "friend-b")
	require.NoError(t, err)
	cards, err := friend.ParseView("friend-b", out)
	require.NoError(t, err)
	assert.Empty(t, cards)
	_, err = sprintView(context.Background(), addr, "friend-a b")
	assert.ErrorContains(t, err, "400 Bad Request")
	assert.ErrorContains(t, err, "names one fleet member or friend")
}
