package secrets

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ClaudeTokenSeat is names-only evidence for one selected subscription seat.
// A key name cannot establish that its value is current, valid, or entitled.
type ClaudeTokenSeat struct {
	Seat  string
	State string
}

// ClaudeTokenPlan is a read-only plan over the central sealed store. The token
// value and the provider's actual expiry are never inspected by this plan.
type ClaudeTokenPlan struct {
	Source       ClaudeTokenSeat
	Targets      []ClaudeTokenSeat
	IssuedAt     string
	RenewBy      string
	RenewalState string
}

const claudeTokenName = "CLAUDE_CODE_OAUTH_TOKEN"

// PlanClaudeTokenNow is the CLI entry point; the pure planner takes a clock so
// its recorded-date judgments can be tested without depending on wall time.
func PlanClaudeTokenNow(store, source string, targets []string, issuedAt string) (ClaudeTokenPlan, error) {
	return PlanClaudeToken(store, source, targets, issuedAt, time.Now())
}

// PlanClaudeToken checks only encrypted-file key names. issuedAt is an optional
// operator record, not an assertion from Claude; the 330-day reminder leaves a
// margin before the documented one-year lifetime and cannot prove validity.
func PlanClaudeToken(store, source string, targets []string, issuedAt string, now time.Time) (ClaudeTokenPlan, error) {
	if !IsValidAsName(source) || len(targets) == 0 || len(targets) > 64 {
		return ClaudeTokenPlan{}, fmt.Errorf("name one source and 1–64 target seats; run: nova-secrets claude-token -h")
	}
	if err := storeShape(store); err != nil {
		return ClaudeTokenPlan{}, err
	}
	seen := map[string]bool{source: true}
	for _, target := range targets {
		if target == "" || strings.TrimSpace(target) != target || !IsValidAsName(target) || seen[target] {
			return ClaudeTokenPlan{}, fmt.Errorf("target seats must be distinct, nonempty names different from the source; run: nova-secrets claude-token -h")
		}
		seen[target] = true
	}
	plan := ClaudeTokenPlan{RenewalState: "unknown"}
	if issuedAt != "" {
		stamp, err := time.Parse(time.RFC3339, issuedAt)
		if err != nil || stamp.After(now) {
			return ClaudeTokenPlan{}, fmt.Errorf("--issued-at must be a past RFC3339 instant recorded when setup-token was issued; run: nova-secrets claude-token -h")
		}
		plan.IssuedAt = stamp.UTC().Format(time.RFC3339)
		plan.RenewBy = stamp.Add(330 * 24 * time.Hour).UTC().Format(time.RFC3339)
		plan.RenewalState = "planned"
		if !now.Before(stamp.Add(330 * 24 * time.Hour)) {
			plan.RenewalState = "due"
		}
	}
	seat := func(name string) (ClaudeTokenSeat, error) {
		entry, err := os.Lstat(filepath.Join(store, name+".yaml"))
		if errors.Is(err, os.ErrNotExist) {
			return ClaudeTokenSeat{Seat: name, State: "seat_missing"}, nil
		}
		if err != nil || !entry.Mode().IsRegular() {
			return ClaudeTokenSeat{}, fmt.Errorf("seat %s file must be a regular encrypted file in the store", name)
		}
		report, err := RunNames(store, name, 0)
		if err != nil {
			return ClaudeTokenSeat{}, err
		}
		found := false
		for _, row := range report.Rows {
			if row.Name == claudeTokenName {
				if found || row.Clear {
					return ClaudeTokenSeat{}, fmt.Errorf("%s in seat %s is duplicate or clear; correct the store before use", claudeTokenName, name)
				}
				found = true
			}
		}
		if found {
			return ClaudeTokenSeat{Seat: name, State: "sealed_name_present_unverified"}, nil
		}
		return ClaudeTokenSeat{Seat: name, State: "missing"}, nil
	}
	var err error
	plan.Source, err = seat(source)
	if err != nil {
		return ClaudeTokenPlan{}, fmt.Errorf("source seat: %w", err)
	}
	if plan.IssuedAt != "" && plan.Source.State != "sealed_name_present_unverified" {
		return ClaudeTokenPlan{}, fmt.Errorf("--issued-at cannot describe a missing source token name; seal it first or omit the date")
	}
	for _, target := range targets {
		item, err := seat(target)
		if err != nil {
			return ClaudeTokenPlan{}, fmt.Errorf("target seat %s: %w", target, err)
		}
		plan.Targets = append(plan.Targets, item)
	}
	return plan, nil
}
