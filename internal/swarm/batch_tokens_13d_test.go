package swarm

import ()

// EVERY CALLER OF `native` PASSES THE WORD, AND NONE INVENTS IT (SPEC-SWARM rule 13d,
// demanded test 13d, issue #1545).
//
// The clauses this file holds:
//
//	"`batch --cards` takes `--tokens <n>|unmetered`, required, exit 2 naming the flag
//	 before any card starts."
//	"It is each card's own budget, the same for every card as `batch --tasks` has it, never
//	 divided among the cards and never a total for the batch."
//	"The batch puts it verbatim into every `native` argv it builds, the local one under
//	 `--harness` and the remote one under **Benches**."
//	"A `--runner` ... is handed the word as a sixth argument after those five, and a runner
//	 that reaches `native` without passing it on meets `native`'s own refusal."
