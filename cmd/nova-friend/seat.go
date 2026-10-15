package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
)

// coordinatorSeat is the seat's holder as the sprint server's coordinator view
// says it (GET /api/view/coordinator?all=1, its seat): who the daemon tells.
func coordinatorSeat(ctx context.Context, server string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+server+"/api/view/coordinator?all=1", nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("the sprint server at %s did not answer: %w", server, err)
	}
	defer resp.Body.Close() // ignored: a read-only body
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxView+1))
	switch {
	case err != nil:
		return "", fmt.Errorf("the sprint server at %s: its view was cut: %w", server, err)
	case len(raw) > maxView:
		return "", fmt.Errorf("the sprint server at %s: its view is over %d bytes", server, maxView)
	case resp.StatusCode != http.StatusOK:
		return "", fmt.Errorf("the sprint server at %s refused the view (%s): %s", server, resp.Status, oneline.Cap(strings.TrimSpace(string(raw)), 300))
	}
	var v struct {
		Seat string `json:"seat"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return "", fmt.Errorf("the sprint server at %s: its view is not JSON: %w", server, err)
	}
	return v.Seat, nil
}
