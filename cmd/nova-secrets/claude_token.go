package main

import (
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/secrets"
	"github.com/mas-bandwidth/nova-tools/internal/tool"
)

// runClaudeTokenCLI uses the shared command skeleton, so this new read-only
// verb does not expand nova-secrets' legacy flag-parser allowance.
func runClaudeTokenCLI(args []string, s streams) int {
	return claudeTokenTool().Run(append([]string{"claude-token"}, args...), s.stdin, s.stdout, s.stderr)
}

func runClaudeTokenFleetCLI(args []string, s streams) int {
	return claudeTokenTool().Run(append([]string{"claude-token-fleet"}, args...), s.stdin, s.stdout, s.stderr)
}

func claudeTokenTool() *tool.Tool {
	return &tool.Tool{
		Name:      "nova-secrets",
		What:      "Plan one Claude subscription token's sealed distribution.",
		How:       "Read sealed key names and an optional issue time; never open a credential.",
		ExitTable: "0 plan printed; 2 invalid input or unreadable store",
		Verbs: []tool.Verb{{
			Name:      "claude-token",
			Usage:     "claude-token --store <dir> --from <source-seat> --to <seat,...> [--issued-at <RFC3339>] [--json]",
			Example:   "claude-token --store ./secrets --from ada --to bo",
			Effect:    tool.Inspection,
			Detail:    "Read-only advisory. A sealed name cannot prove token validity, subscription entitlement or provider access.\nexample: nova-secrets claude-token --store ./secrets --from ada --to bo",
			ExitTable: "0 names-only plan printed; 2 invalid input or unreadable store",
			Flags: func(f *tool.Flags) {
				f.Required("store", "git working copy of the sealed store")
				f.Required("from", "source seat where the setup-token is sealed")
				f.Required("to", "comma-separated target seats, each with its own encrypted file")
				f.String("issued-at", "", "optional recorded RFC3339 issuance instant; never the token's value")
			},
			Run: runClaudeTokenPlan,
		}, {
			Name:      "claude-token-fleet",
			Usage:     "claude-token-fleet --store <dir> --manifest <file> [--json]",
			Example:   "claude-token-fleet --store ./secrets --manifest ./claude-token-fleet.tsv",
			Effect:    tool.Inspection,
			Detail:    "Read-only advisory. Every account/profile/machine/seat binding comes from the reviewed manifest; registry admission and provider validity remain unchecked.\nexample: nova-secrets claude-token-fleet --store ./secrets --manifest ./claude-token-fleet.tsv",
			ExitTable: "0 names-only plan printed; 2 unknown or conflicting binding, invalid file or store",
			Flags: func(f *tool.Flags) {
				f.Required("store", "git working copy of the sealed store")
				f.Required("manifest", "reviewed eight-column account/source-machine/profile/target-machine/seat TSV")
			},
			Run: runClaudeTokenFleetPlan,
		}},
	}
}

func runClaudeTokenPlan(c *tool.Call) *tool.Out {
	plan, err := secrets.PlanClaudeTokenNow(c.Str("store"), c.Str("from"), strings.Split(c.Str("to"), ","), c.Str("issued-at"))
	if err != nil {
		return tool.Refuse(err.Error())
	}
	out := tool.Done().Fact("source", plan.Source.Seat).Fact("source_state", plan.Source.State).
		Fact("targets", len(plan.Targets)).Fact("token_expiry", "unknown").
		Fact("issued_at", plan.IssuedAt).Fact("renew_by", plan.RenewBy).
		Fact("renewal", plan.RenewalState).Fact("store_freshness", "not_checked").Fact("provider_turn", "not_checked")
	for _, seat := range plan.Targets {
		out.Item("seat", "name", seat.Seat, "state", seat.State)
	}
	if plan.Source.State == "missing" || plan.Source.State == "seat_missing" {
		out.Item("next", "action", "issue", "seat", plan.Source.Seat, "command", "claude setup-token")
		out.Item("next", "action", "seal_hidden_prompt", "seat", plan.Source.Seat, "command", "nova-secrets seal -h")
	}
	for _, seat := range plan.Targets {
		if seat.State == "seat_missing" {
			out.Item("next", "action", "create_target_seat", "seat", seat.Seat, "command", "nova-secrets seat add -h")
		} else {
			out.Item("next", "action", "inject_or_refresh", "seat", seat.Seat, "command", "nova-secrets seat inject -h")
		}
		out.Item("next", "action", "check_on_target", "seat", seat.Seat, "command", "nova-secrets check -h")
	}
	out.Note("Read-only advisory: a sealed key name and recorded date do not prove token validity, entitlement, or an actual provider reply.")
	return out
}

func runClaudeTokenFleetPlan(c *tool.Call) *tool.Out {
	plan, err := secrets.PlanClaudeTokenFleetNow(c.Str("store"), c.Str("manifest"))
	if err != nil {
		return tool.Refuse(err.Error())
	}
	out := tool.Done().Fact("accounts", len(plan.Accounts)).Fact("targets", plan.Targets).
		Fact("token_expiry", "unknown").Fact("recipient_registry", "not_checked").
		Fact("provider_turn", "not_checked")
	for _, account := range plan.Accounts {
		out.Item("account", "id", account.ID, "label", account.Label, "source_machine", account.SourceMachine, "source_seat", account.Token.Source.Seat,
			"source_state", account.Token.Source.State, "renew_by", account.Token.RenewBy, "renewal", account.Token.RenewalState)
		for _, target := range account.Targets {
			out.Item("target", "account", account.ID, "machine", target.Machine, "profile", target.Profile, "seat", target.Seat, "state", target.State)
		}
	}
	out.Note("Read-only advisory: distinct seat names do not establish recipient registry admission, token validity, entitlement, or a provider reply.")
	return out
}
