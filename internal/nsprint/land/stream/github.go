package stream

import "github.com/mas-bandwidth/nova-tools/internal/gh"

// GitHub is the one GitHub client (internal/gh, nova-tools #4343): the only
// GitHub calls the lander makes are the ones a git remote cannot avoid
// (open the stream PR, merge it, comment and close the members, read a
// member's body when its record does not say which issues it closes, and
// comment on and close those issues, which GitHub does not close on a merge
// into a branch other than the default). Budget caps the calls one verb
// run makes; a run that would exceed it stops and says so. Every call is
// counted per verb and endpoint in Redis when the client has a store.
// Never GraphQL.
type GitHub = gh.Client

// ErrBudget is returned once the run's REST budget is spent.
var ErrBudget = gh.ErrBudget
