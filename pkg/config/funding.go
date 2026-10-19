package config

import (
	"context"
	"errors"
	"fmt"
)

// ErrOwnerOnly is the refusal when a write only the sprint row's owner may make:
// a provider's funded flip to true, a route's enable on an unfunded provider, and
// the owner declaration itself (docs/SPEC-CONFIG.md, "Funding is the owner's";
// tla/ProviderFunding.tla, OnlyOwnerFunds and NoEnableOnUnfunded).
var ErrOwnerOnly = errors.New("owner only")

// refuserSeamable is a store whose funding refuser a test may turn off: the reversed
// witness's seam, never a flag (DONE-WHEN, REVERSED WITNESS).
type refuserSeamable interface{ refuserDisabled() bool }

// checkRefuseFunding is the cross-row funding guard every store runs beside checkRefs
// before an add or a set (tla/ProviderFunding.tla OnlyOwnerFunds and NoEnableOnUnfunded;
// docs/SPEC-CONFIG.md, "Funding is the owner's"): the actor is a declared name, so it
// stops the machine and the coordinator re-funding by habit, not authentication against
// a deliberate false --actor. It receives the row the write would leave (next), the row
// it starts from (before; the zero Row for an add) and the changes.
func checkRefuseFunding(ctx context.Context, st Store, k *Kind, before, next Row, changes map[string]string, actor string) error {
	if s, ok := st.(refuserSeamable); ok && s.refuserDisabled() {
		return nil
	}
	switch k.Name {
	case KindProvider:
		if next.Fields["funded"] == "true" && before.Fields["funded"] != "true" {
			return refuseOwnerFunds(ctx, st, next.Name, actor)
		}
	case KindRoute:
		if next.Fields["enabled"] == "true" && before.Fields["enabled"] != "true" {
			p, found, err := st.Get(ctx, KindProvider, next.Fields["provider"])
			if err != nil {
				return err
			}
			if found && p.Fields["funded"] != "true" {
				return &RefusedError{Err: ErrOwnerOnly, Detail: fmt.Sprintf(
					"route %s enables provider %s, whose row says funded=false: the owner funds it first (provider set %s --funded true --actor <owner>)",
					next.Name, next.Fields["provider"], next.Fields["provider"])}
			}
		}
	case KindSprint:
		declared, ok := changes["owner"]
		if !ok {
			return nil
		}
		current := before.Fields["owner"]
		if current == "" {
			if actor != declared {
				return &RefusedError{Err: ErrOwnerOnly, Detail: fmt.Sprintf(
					"sprint owner is undeclared and the actor %q is not the name being declared %q: the first declaration names the actor (sprint set --owner %s --actor %s)",
					actor, declared, declared, declared)}
			}
		} else if actor != current {
			return &RefusedError{Err: ErrOwnerOnly, Detail: fmt.Sprintf(
				"sprint owner is %q and the actor %q is not the owner", current, actor)}
		}
	}
	return nil
}

// refuseOwnerFunds refuses a provider's funded flip to true unless the actor is the
// sprint row's owner; with no owner declared every such write is refused, naming the
// first declaration. The refusal names the provider, the actor, the owner and the
// remedy, the owner's to run (tla/ProviderFunding.tla OnlyOwnerFunds).
func refuseOwnerFunds(ctx context.Context, st Store, provider, actor string) error {
	owner := ""
	if sprint, found, err := st.Get(ctx, KindSprint, KindSprint); err != nil {
		return err
	} else if found {
		owner = sprint.Fields["owner"]
	}
	if owner == "" {
		return &RefusedError{Err: ErrOwnerOnly, Detail: fmt.Sprintf(
			"provider %s funded=true needs the owner, and none is declared (sprint set --owner <name> --actor <name>)", provider)}
	}
	if actor != owner {
		return &RefusedError{Err: ErrOwnerOnly, Detail: fmt.Sprintf(
			"provider %s funded=true is the owner's to write: the actor %q is not the owner %q (provider set %s --funded true --actor %s)",
			provider, actor, owner, provider, owner)}
	}
	return nil
}
