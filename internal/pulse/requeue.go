package pulse

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/fleet"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// Issue #2011: Provider Error Auto-Requeue in pulse harvest/fill.
//
// When a card run ends with NATIVE PROVIDER verdict (or usage.tsv end=provider),
// it is an upstream provider infrastructure failure (5xx server error, 429 quota/rate limit,
// or gateway UnknownError), NOT a card or model bug.
//
// Rather than dropping the card into failed/ or counting it as an unrecoverable defect,
// RequeueProviderCard moves the card from --launched back into --ready and records
// retry attempts and failed routes in a <card>.provider-retry marker file.
//
// Up to maxRetries (default 3). When retries are exhausted, the card remains in launched
// with a .provider-failed marker.

const (
	// DefaultMaxProviderRetries is the maximum auto-requeue attempts for provider errors.
	DefaultMaxProviderRetries = 3

	// ProviderRetrySuffix is the marker beside a requeued card recording retry state.
	ProviderRetrySuffix = ".provider-retry"

	// ProviderFailedSuffix is the marker beside an exhausted card in launched.
	ProviderFailedSuffix = ".provider-failed"
)

// ProviderRetryState holds parsed state from a .provider-retry marker.
type ProviderRetryState struct {
	Attempts     int
	FailedRoutes []string
}

// RequeueProviderCard moves a card that failed due to a provider infrastructure error
// from launchedDir back into readyDir, updating/writing its .provider-retry marker.
// If retries are exhausted (> maxRetries, default 3), the card stays in launchedDir
// with a .provider-failed marker.
func RequeueProviderCard(readyDir, launchedDir string, cardBase string, maxRetries int) (requeued bool, nextAttempt int, err error) {
	return RequeueProviderCardWithRoute(readyDir, launchedDir, cardBase, "", maxRetries)
}

// RequeueProviderCardWithRoute behaves like RequeueProviderCard but also associates an explicit
// failed route with the failure if provided.
func RequeueProviderCardWithRoute(readyDir, launchedDir string, cardBase string, failedRoute string, maxRetries int) (requeued bool, nextAttempt int, err error) {
	if strings.TrimSpace(readyDir) == "" {
		return false, 0, errors.New("missing ready directory")
	}
	if strings.TrimSpace(launchedDir) == "" {
		return false, 0, errors.New("missing launched directory")
	}
	if strings.TrimSpace(cardBase) == "" {
		return false, 0, errors.New("missing cardBase")
	}
	if maxRetries <= 0 {
		maxRetries = DefaultMaxProviderRetries
	}

	// Locate source card in launchedDir
	sourceCard := findCardPath(launchedDir, cardBase)
	if sourceCard == "" {
		return false, 0, fmt.Errorf("card %s not found in %s", oneline.Field(cardBase), oneline.Field(launchedDir))
	}
	base := filepath.Base(sourceCard)

	// Read existing retry state if any.
	// FAIL-CLOSED: if .provider-retry exists but cannot be read or is a directory,
	// or has invalid contents, fail-closed immediately! Never reset attempts to 0 or move card.
	existing, err := ReadProviderRetry(launchedDir, base)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, 0, fmt.Errorf("failed to read provider retry state in %s: %w", launchedDir, err)
	}
	if existing == nil {
		existing, err = ReadProviderRetry(readyDir, base)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return false, 0, fmt.Errorf("failed to read provider retry state in %s: %w", readyDir, err)
		}
	}
	attempts := 0
	var failedRoutes []string
	if existing != nil {
		attempts = existing.Attempts
		failedRoutes = append(failedRoutes, existing.FailedRoutes...)
	}

	// Determine route that failed
	route := strings.TrimSpace(failedRoute)
	if route == "" {
		route = detectCardRoute(sourceCard, launchedDir, base)
	}
	if route != "" && !containsRoute(failedRoutes, route) {
		failedRoutes = append(failedRoutes, route)
	}

	nextAttempt = attempts + 1

	// Check if retry limit exceeded
	if nextAttempt > maxRetries {
		// Retries exhausted: persist .provider-failed marker in launchedDir, card stays in launchedDir
		failedMarker := filepath.Join(launchedDir, base+ProviderFailedSuffix)
		body := fmt.Sprintf("failed_routes=%s attempts=%d reason=retries-exhausted\n",
			strings.Join(failedRoutes, ","), attempts)
		if werr := os.WriteFile(failedMarker, []byte(body), 0o644); werr != nil {
			return false, nextAttempt, fmt.Errorf("failed to write provider failed marker: %w", werr)
		}
		return false, nextAttempt, nil
	}

	// Ensure readyDir exists
	if err := os.MkdirAll(readyDir, 0o755); err != nil {
		return false, 0, err
	}

	// FAIL-CLOSED DURABLE PERSISTENCE:
	// Persist the updated retry state before moving the card!
	markerContent := fmt.Sprintf("failed_routes=%s attempts=%d\n",
		strings.Join(failedRoutes, ","), nextAttempt)

	// 1. Persist updated marker in launchedDir first so state is never lost even if crash occurs before move
	launchedRetryMarker := filepath.Join(launchedDir, base+ProviderRetrySuffix)
	if err := os.WriteFile(launchedRetryMarker, []byte(markerContent), 0o644); err != nil {
		return false, 0, fmt.Errorf("failed to persist retry state before moving card: %w", err)
	}

	// 2. Persist updated marker in readyDir before moving the card
	readyRetryMarker := filepath.Join(readyDir, base+ProviderRetrySuffix)
	if err := os.WriteFile(readyRetryMarker, []byte(markerContent), 0o644); err != nil {
		return false, 0, fmt.Errorf("failed to write retry marker in ready dir: %w", err)
	}

	// 3. Move card from launchedDir back to readyDir
	destCard := filepath.Join(readyDir, base)
	if err := os.Rename(sourceCard, destCard); err != nil {
		// Clean up the ready marker if rename fails
		_ = os.Remove(readyRetryMarker)
		return false, 0, fmt.Errorf("failed to move card: %w", err)
	}

	// 4. Remove launched marker in launchedDir so lane is released
	_ = os.Remove(filepath.Join(launchedDir, base+".launched"))
	_ = os.Remove(launchedRetryMarker)

	return true, nextAttempt, nil
}

// ReadProviderRetry reads a .provider-retry marker file beside a card in dir.
// It fails closed if the marker cannot be read or is a directory.
func ReadProviderRetry(dir, cardBase string) (*ProviderRetryState, error) {
	markerPath := findMarkerPath(dir, cardBase, ProviderRetrySuffix)
	if markerPath == "" {
		return nil, os.ErrNotExist
	}
	st, err := os.Stat(markerPath)
	if err != nil {
		return nil, err
	}
	if st.IsDir() {
		return nil, fmt.Errorf(".provider-retry is a directory: %s", markerPath)
	}
	raw, err := os.ReadFile(markerPath)
	if err != nil {
		return nil, err
	}
	return parseProviderRetry(raw)
}

func parseProviderRetry(raw []byte) (*ProviderRetryState, error) {
	trimmed := strings.TrimSpace(string(raw))
	if len(trimmed) == 0 {
		return nil, errors.New(".provider-retry is empty")
	}
	st := &ProviderRetryState{}
	foundAttempts := false
	tokens := strings.Fields(trimmed)
	for _, tok := range tokens {
		k, v, ok := strings.Cut(tok, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		switch k {
		case "attempts":
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				st.Attempts = n
				foundAttempts = true
			} else {
				return nil, fmt.Errorf(".provider-retry has invalid attempts: %q", v)
			}
		case "failed_routes":
			if v != "" && v != "-" {
				for _, r := range strings.Split(v, ",") {
					r = strings.TrimSpace(r)
					if r != "" && !containsRoute(st.FailedRoutes, r) {
						st.FailedRoutes = append(st.FailedRoutes, r)
					}
				}
			}
		}
	}
	if !foundAttempts {
		return nil, errors.New(".provider-retry missing valid attempts field")
	}
	return st, nil
}

// SelectAlternateRoute selects the cheapest available route from reg that has not failed yet.
func SelectAlternateRoute(reg *fleet.ProviderRegistry, tier string, inFlight map[string]int, errorRate map[string]float64, failedRoutes []string) (*fleet.Route, error) {
	if reg == nil {
		return nil, errors.New("nil provider registry")
	}
	available := reg.AvailableRoutes(tier, inFlight, errorRate)
	filtered := FilterFailedRoutes(available, failedRoutes)
	if len(filtered) == 0 {
		return nil, fmt.Errorf("all available routes have failed: %v", failedRoutes)
	}
	return filtered[0], nil
}

// FilterFailedRoutes removes any routes whose Route or Name matches an entry in failedRoutes.
func FilterFailedRoutes(routes []*fleet.Route, failedRoutes []string) []*fleet.Route {
	if len(failedRoutes) == 0 {
		return routes
	}
	var out []*fleet.Route
	for _, r := range routes {
		if !containsRoute(failedRoutes, r.Route) && !containsRoute(failedRoutes, r.Name) {
			out = append(out, r)
		}
	}
	return out
}

// NextAlternateRoute returns the first route name from candidates that is not in failedRoutes.
func NextAlternateRoute(candidates []string, failedRoutes []string) (string, bool) {
	for _, c := range candidates {
		trimmed := strings.TrimSpace(c)
		if trimmed != "" && !containsRoute(failedRoutes, trimmed) {
			return trimmed, true
		}
	}
	return "", false
}

// IsJobProviderError checks whether a finished job ended in an upstream provider error
// (by inspecting usage.tsv end=provider, or NATIVE PROVIDER in logs).
func IsJobProviderError(jobDir string) bool {
	if raw, err := os.ReadFile(filepath.Join(jobDir, "usage.tsv")); err == nil {
		for _, line := range strings.Split(string(raw), "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.Contains(trimmed, "end=provider") {
				return true
			}
			for _, f := range strings.Split(trimmed, "\t") {
				if strings.TrimSpace(f) == "provider" {
					return true
				}
			}
		}
	}
	for _, name := range []string{"native.log", "harness-output.log", "harness.log"} {
		if raw, err := os.ReadFile(filepath.Join(jobDir, name)); err == nil {
			if strings.Contains(string(raw), "NATIVE PROVIDER") {
				return true
			}
			if pf, ok := swarm.ClassifyProviderFailure(raw); ok && pf.Why != "" {
				return true
			}
		}
	}
	return false
}

func containsRoute(routes []string, target string) bool {
	for _, r := range routes {
		if strings.EqualFold(r, target) {
			return true
		}
	}
	return false
}

func findCardPath(dir, base string) string {
	p := filepath.Join(dir, base)
	if _, err := os.Stat(p); err == nil {
		return p
	}
	if strings.HasSuffix(base, ".md") {
		alt := filepath.Join(dir, strings.TrimSuffix(base, ".md"))
		if _, err := os.Stat(alt); err == nil {
			return alt
		}
	} else {
		alt := filepath.Join(dir, base+".md")
		if _, err := os.Stat(alt); err == nil {
			return alt
		}
	}
	return ""
}

func findMarkerPath(dir, base, suffix string) string {
	p := filepath.Join(dir, base+suffix)
	if _, err := os.Stat(p); err == nil {
		return p
	}
	if strings.HasSuffix(base, ".md") {
		alt := filepath.Join(dir, strings.TrimSuffix(base, ".md")+suffix)
		if _, err := os.Stat(alt); err == nil {
			return alt
		}
	} else {
		alt := filepath.Join(dir, base+".md"+suffix)
		if _, err := os.Stat(alt); err == nil {
			return alt
		}
	}
	return ""
}

func detectCardRoute(cardPath, launchedDir, base string) string {
	// 1. Check launched marker
	launchedPath := filepath.Join(launchedDir, base+".launched")
	if raw, err := os.ReadFile(launchedPath); err == nil {
		for _, line := range strings.Split(string(raw), "\n") {
			k, v, ok := strings.Cut(line, "=")
			if ok && (strings.TrimSpace(k) == "route" || strings.TrimSpace(k) == "model") {
				val := strings.TrimSpace(v)
				if val != "" {
					return val
				}
			}
		}
	}

	// 2. Check card file
	if raw, err := os.ReadFile(cardPath); err == nil {
		for _, line := range strings.Split(string(raw), "\n") {
			trimmed := strings.TrimSpace(line)
			for _, prefix := range []string{"MODEL:", "ROUTE:", "PROVIDER:"} {
				if v, ok := strings.CutPrefix(trimmed, prefix); ok {
					val := strings.TrimSpace(v)
					if val != "" {
						return val
					}
				}
			}
		}
	}
	return ""
}
