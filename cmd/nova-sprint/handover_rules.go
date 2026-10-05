package main

import (
	"encoding/json"
	"strconv"
	"strings"
)

// The coordinator's numbered rules (docs/SPRINT-COORDINATOR.md, section 10).
// Each is the rule, why, the verb or setting that does it (judgment when the
// seat decides and no verb does it), the card (none), and whether a pass
// prints it. handover --json carries every rule. The text screen prints the
// waves rule from the seat. spliceHandoverText prints the pass rules into
// that screen. The seat does not call it.

// coordRule is one numbered rule.
type coordRule struct {
	Rule, Why, Does, Card string
	Pass                  bool
}

// row is the rule as the runbook's table row.
func (r coordRule) row(n int) string {
	pass := "no"
	if r.Pass {
		pass = "yes"
	}
	return "| " + strings.Join([]string{strconv.Itoa(n), r.Rule, r.Why, r.Does, r.Card, pass}, " | ") + " |"
}

// line is the rule as handover prints it.
func (r coordRule) line(n int) string {
	return strconv.Itoa(n) + ". " + r.Rule + " Does it: " + strings.ReplaceAll(r.Does, "`", "") + "."
}

// coordinatorRuleLines is every rule as handover --json carries it, in order.
func coordinatorRuleLines() []string {
	out := make([]string, 0, len(coordinatorRules))
	for i, r := range coordinatorRules {
		out = append(out, r.line(i+1))
	}
	return out
}

// handoverRuleText is the RULE lines of a handover screen: the waves rule,
// each pass rule, and one line naming where every rule is.
func handoverRuleText(rules []string) []string {
	numbered := map[string]bool{}
	pass := map[string]bool{}
	for i, r := range coordinatorRules {
		numbered[r.line(i+1)] = true
		pass[r.line(i+1)] = r.Pass
	}
	var out []string
	shown := 0
	for _, r := range rules {
		if numbered[r] && !pass[r] {
			continue
		}
		if numbered[r] {
			shown++
		}
		out = append(out, "RULE "+r)
	}
	return append(out, "RULES "+strconv.Itoa(len(coordinatorRules))+" numbered: "+strconv.Itoa(shown)+" above; every one in handover --json and docs/SPRINT-COORDINATOR.md section 10")
}

// spliceHandoverText inserts the pass rules into a handover screen. JSON is
// left as MarshalJSON wrote it. Output that is not a handover screen is left
// as it is, including a verb's help.
func spliceHandoverText(out string) string {
	trim := strings.TrimSpace(out)
	if strings.HasPrefix(trim, "{") {
		return out
	}
	waves := "RULE " + handoverWaves + "\n"
	if !strings.Contains(out, waves) {
		return out
	}
	var b strings.Builder
	for _, l := range handoverRuleText(append([]string{handoverWaves}, coordinatorRuleLines()...)) {
		b.WriteString(l)
		b.WriteByte('\n')
	}
	return strings.Replace(out, waves, b.String(), 1)
}

// MarshalJSON puts every numbered rule on handover --json. The waves rule stays
// first. The seat sets Rules to that one rule; this method is what adds the rest.
func (h handoverView) MarshalJSON() ([]byte, error) {
	type view handoverView
	v := view(h)
	v.Rules = append([]string{handoverWaves}, coordinatorRuleLines()...)
	return json.Marshal(v)
}

// coordinatorRules is section 10, in its order. The first fourteen are the
// rules of each pass. The rest are the runbook rows.
var coordinatorRules = []coordRule{
	{
		Rule: "A friend is working only when its cards go working to done; awake, beating or answering pings is not working. Ask a friend holding cards with no finish in the bound what blocks it, and remove the blocker.",
		Why:  "Friends stayed awake and answered pings while no card moved.",
		Does: "`nova-sprint set --friend-finish <duration>`, `nova-sprint view coordinator --json`",
		Card: "none",
		Pass: true,
	},
	{
		Rule: "A friend whose session does not answer a wake ping is woken and fixed on the same pass, never left overnight; when the cause is the owner's (credit, keys, account), mark it down so its cards move, and tell the owner.",
		Why:  "A friend slept deaf all night with work assigned.",
		Does: "`nova-friend ping --as <coordinator> --to <friend>`, `nova-friend wait-pong`, `nova-sprint friend down <friend> --reason <text>`",
		Card: "none",
		Pass: true,
	},
	{
		Rule: "Every 10 minutes walk the friend chain, stopping at the first failing link: up, hears, delivered, started, progressing, finished, returned, balanced; check the fleet's working against width, ready, review, merging and their ages, and the open judgments.",
		Why:  "Problems were found late, and by the owner.",
		Does: "`nova-sprint watch --wake --check <duration>`, `nova-sprint view coordinator --json`, `nova-friend status`",
		Card: "none",
		Pass: true,
	},
	{
		Rule: "Level idle lanes before topping up full queues: a friend or machine with free width and nothing ready is fed first.",
		Why:  "Full queues were topped up while lanes sat idle.",
		Does: "`nova-sprint friend level`, `nova-sprint fleet level`",
		Card: "none",
		Pass: true,
	},
	{
		Rule: "Every fix lands on the one sprint base first, and the live server is built only from that base; any side branch a card names is folded into the base every pass, and a build whose commit is not on the base is not installed.",
		Why:  "The live server ran from a side branch a thousand commits apart from the base.",
		Does: "`nova-sprint land --base <branch> --dry-run`, `nova-sprint server switch <binary>`",
		Card: "none",
		Pass: true,
	},
	{
		Rule: "The base is promoted to dev continually (every 20 to 30 minutes or every 25 landings), and a promotion is recorded.",
		Why:  "Stream branches drifted far from dev.",
		Does: "`nova-sprint promote --every <duration> --landings <n>`, `nova-sprint promoted --sha <merge sha>`",
		Card: "none",
		Pass: true,
	},
	{
		Rule: "Every adoption reaches every fleet machine in the same step, funded or not, with each machine's version reported; a machine back from down adopts the latest release before it is dealt.",
		Why:  "Idle machines fell behind, and returning machines worked on old binaries.",
		Does: "`nova-update release adopt`, `nova-update adoption --file <path>`",
		Card: "none",
		Pass: true,
	},
	{
		Rule: "A returning friend or machine is brought up by evidence, its own beat or output, never by expectation.",
		Why:  "Friends were shown up while absent, and returning ones stayed down.",
		Does: "`nova-sprint friend up <friend>`, `nova-sprint fleet up <member>`",
		Card: "none",
		Pass: true,
	},
	{
		Rule: "The same finding twice is a brief defect: the tick refuses a third rework; fix the brief or drop the card.",
		Why:  "A card was reworked hundreds of times on one finding.",
		Does: "`nova-sprint brief <id> --brief-file <path>`, `nova-sprint drop <id> --reason <text>`",
		Card: "none",
		Pass: true,
	},
	{
		Rule: "Work that needs a file outside a card's PATHS means a twin card with the PATHS widened, never an edit outside them.",
		Why:  "Out-of-PATHS edits collided with other cards and were refused at landing.",
		Does: "`nova-sprint recut <id> --brief-file <path> --new <id>`, `nova-sprint relink <old-id> <new-id> --reason <text>`",
		Card: "none",
		Pass: true,
	},
	{
		Rule: "A rate limit is backed off and resumed; out of funds is held, raised to the owner once per provider, and never answered with a rework.",
		Why:  "A night of work landed nothing under a provider with no funds.",
		Does: "`nova-sprint routes`, `nova-sprint funded <provider> --reason <text>`",
		Card: "none",
		Pass: true,
	},
	{
		Rule: "Shared resources (machines, branches, ports, accounts) are claimed through coordinator verbs with leases; never tell a friend to wait on another friend.",
		Why:  "A hand-shaken hold starved a friend when its holder went down.",
		Does: "`nova-sprint lane take <kind> --machine <m> --as <worker>`, `nova-sprint lane give <kind> --machine <m> --as <worker>`",
		Card: "none",
		Pass: true,
	},
	{
		Rule: "Friends work only in their real working directory: the coordinator writes jobs into its inbox, the friend writes results into its outbox, and no brief names a path outside.",
		Why:  "Friends trampled each other's files.",
		Does: "`nova-sprint friend sync --root <dir>`",
		Card: "none",
		Pass: true,
	},
	{
		Rule: "Put no text on the owner's dashboard that the owner did not ask for.",
		Why:  "Unasked notes cluttered the page.",
		Does: "judgment",
		Card: "none",
		Pass: true,
	},
	{
		Rule: "Apply an order addressed to the coordinator (stop, conserve, sleep) to the coordinator only; friends change only on \"all friends\" or by name.",
		Why:  "single orders stopped the whole team",
		Does: "judgment",
		Card: "none",
		Pass: false,
	},
	{
		Rule: "Debug a silent friend in order: bus, daemon push, then the friend's own window; never hand the owner text to paste.",
		Why:  "the owner was stuck relaying",
		Does: "`nova-bus peek`, `nova-friend check`, `nova-friend status`",
		Card: "none",
		Pass: false,
	},
	{
		Rule: "Never design a wave as one chain; width comes from independent work.",
		Why:  "languages queued behind one fix only one of them needed",
		Does: "`nova-sprint needs --roots`",
		Card: "none",
		Pass: false,
	},
	{
		Rule: "Read a uniform failure shape across a model's cards as our contract failing (prompt or wrapper), fix it per model family, and drop a model only on a measured quality floor with varied failure shapes.",
		Why:  "capable models were dropped for our own format bugs",
		Does: "`nova-sprint stats`",
		Card: "none",
		Pass: false,
	},
	{
		Rule: "Express every sprint feature as an operation on table rows or a queue between tables, or leave it out.",
		Why:  "added mechanisms made the design opaque",
		Does: "judgment",
		Card: "none",
		Pass: false,
	},
	{
		Rule: "Put most work through the sprint, friend work included, as cards in streams.",
		Why:  "hand briefs made cost and progress invisible",
		Does: "`nova-sprint add --stream <s> --brief-dir <dir>`",
		Card: "none",
		Pass: false,
	},
	{
		Rule: "Give friends the judgment work (design, ratings, audits) and the fleet the mechanical work.",
		Why:  "judgment work failed on mechanical routes",
		Does: "judgment",
		Card: "none",
		Pass: false,
	},
	{
		Rule: "Open a new sprint with its simplest mechanical class on flash, and hold judgment cards behind a sentinel.",
		Why:  "complex first cards failed expensively",
		Does: "`nova-sprint add --stream <s> --sentinel <id>`, `nova-sprint release <sentinel> --reason <text>`",
		Card: "none",
		Pass: false,
	},
	{
		Rule: "Have every cold read run at least one temporal probe (land then reopen, claim during resolve), and keep each reproduction as a test.",
		Why:  "defects in sequences escaped happy-path reads",
		Does: "judgment",
		Card: "none",
		Pass: false,
	},
	{
		Rule: "Make a score under 10 name its reasons and the work that would reach 10.",
		Why:  "low scores gave no path to fix",
		Does: "judgment",
		Card: "none",
		Pass: false,
	},
	{
		Rule: "Judge the read gate only by periodic cold audits of landed work (a sample, tests run, by a stronger reader), repeated when the card mix changes, never by pass rates or shadow classifier output.",
		Why:  "pass rates and shadow classes misjudged reader quality",
		Does: "`nova-sprint stream set <stream> --read-tier <tier>`",
		Card: "none",
		Pass: false,
	},
	{
		Rule: "When streams stick, look across streams for the common failure class and fix the class.",
		Why:  "per-card fixes repeated",
		Does: "`nova-sprint where --json`, `nova-sprint log --stream <s>`",
		Card: "none",
		Pass: false,
	},
	{
		Rule: "When a PR is held up by repeated rounds, stop the round, land the reviewed head as is, and cut a follow-up card for the rest.",
		Why:  "endless fix rounds on a moving base",
		Does: "`nova-sprint accept <id>`, `nova-sprint add --stream <s> --brief-file <f1>`",
		Card: "none",
		Pass: false,
	},
	{
		Rule: "Review landed sprint work carefully, stream by stream, before the sprint branch merges into the release line; bad hunks become repair cards, and recurring patterns go to friends.",
		Why:  "mechanical work risks bad code",
		Does: "`nova-sprint log --stream <s>`",
		Card: "none",
		Pass: false,
	},
	{
		Rule: "Shrink test code through harnesses, constructors with defaults and table-driven cases rather than copy-paste.",
		Why:  "duplicated test rigs",
		Does: "judgment",
		Card: "none",
		Pass: false,
	},
	{
		Rule: "Gate every layer with a run at 10x to 100x its largest real size with a time limit per operation, and also a small trickle run.",
		Why:  "scale and ordering bugs hid at normal size",
		Does: "`nova-sprint play --simulation --seed <n>`",
		Card: "none",
		Pass: false,
	},
	{
		Rule: "Before cutting a repo, a long-lived branch or an integration branch, name what stops landing on the old line and when; a branch that keeps receiving its source's changes is a fork.",
		Why:  "branches knotted together",
		Does: "judgment",
		Card: "none",
		Pass: false,
	},
	{
		Rule: "Make adoption the last step of every build: merge, update every machine, restart the daemons and loops that run it, announce it, have everyone use it that day, and turn frictions into cards.",
		Why:  "built tools went unused",
		Does: "`nova-update release adopt`, `nova-bus send`",
		Card: "none",
		Pass: false,
	},
	{
		Rule: "Follow every adoption with a dogfood receipt the same day (real use, gaps filed with the command and output).",
		Why:  "adoption without use hid gaps",
		Does: "judgment",
		Card: "none",
		Pass: false,
	},
	{
		Rule: "Call a tool or loop used only when a dated log shows deliveries (read the tail, count deliveries against refusals, name the last real run), and stop a loop with zero deliveries in a day.",
		Why:  "loops refused silently for days while \"running\"",
		Does: "`nova-sprint machinery`",
		Card: "none",
		Pass: false,
	},
	{
		Rule: "Switch to a replacement tool only after every user has sent and received on it and the friction list is empty, and keep the old one read-only for the record.",
		Why:  "switchovers stranded users",
		Does: "judgment",
		Card: "none",
		Pass: false,
	},
	{
		Rule: "Do not build a thing with itself until it is built and reliable; fix it by hand or by child agents, install it, then resume.",
		Why:  "a broken sprint was used to fix itself (the hold on machinery fixes through the sprint was lifted once it was stable)",
		Does: "judgment",
		Card: "none",
		Pass: false,
	},
	{
		Rule: "When a layer settles, land it, split it to its own boundary (repo, module or release line), and build the moving work above it; the settled layer changes only by deliberate release.",
		Why:  "settled and moving work tangled",
		Does: "judgment",
		Card: "none",
		Pass: false,
	},
	{
		Rule: "Keep everything in nova-tools useful without nova-sprint and expressed in general terms; sprint-only programs live in nova-sprint as one binary, and nova-sprint depends on nova-tools, never the reverse.",
		Why:  "sprint opinions leaked into the general kit",
		Does: "judgment",
		Card: "none",
		Pass: false,
	},
	{
		Rule: "Build layers bottom up, one at a time, each passing its gate (model with reversed witnesses, trip and commit tests, property test, receipt replay, a quack run, an independent read, real use) before anything above starts; hold later layers' cards until the layer below locks.",
		Why:  "earlier rebuilds failed without checked foundations",
		Does: "`nova-sprint add --stream <s> --sentinel <id>`, `nova-sprint held`",
		Card: "none",
		Pass: false,
	},
	{
		Rule: "When an upper layer needs something new below, pause, extend the lower layer under its own gate, then resume.",
		Why:  "workarounds above broken layers",
		Does: "judgment",
		Card: "none",
		Pass: false,
	},
	{
		Rule: "Lock what the owner has stated in words as checked invariants; a lock change needs the owner's quoted words, and other labelled rules stay changeable.",
		Why:  "design drift in the core machine",
		Does: "judgment",
		Card: "none",
		Pass: false,
	},
	{
		Rule: "Release from main by fast-forward promotion of a revision that is green, after a cold audit at the release-candidate sha on main and dogfooding on real work other than the tool itself; green CI alone never releases.",
		Why:  "releases went out on branch verdicts",
		Does: "`nova-update release cut`",
		Card: "none",
		Pass: false,
	},
	{
		Rule: "Have release notes written by an author and read cold before the cut, never generated.",
		Why:  "generated notes said nothing useful",
		Does: "judgment",
		Card: "none",
		Pass: false,
	},
	{
		Rule: "Give docs, help and README work a final prose pass by the strongest prose author before rating and landing.",
		Why:  "uneven prose lowered read ratings",
		Does: "judgment",
		Card: "none",
		Pass: false,
	},
	{
		Rule: "Write docs in the present tense only: no parked names, dates or history.",
		Why:  "docs read as archaeology",
		Does: "judgment",
		Card: "none",
		Pass: false,
	},
	{
		Rule: "Build every tool to the standard (STANDARD.md, embedded in AGENTS.md, with each rule naming its class test).",
		Why:  "tools were built inconsistently",
		Does: "judgment",
		Card: "none",
		Pass: false,
	},
	{
		Rule: "Rate every tool cold two ways, USE (binary and help only) and READ (README then code), by several models; a tool is done at 9 or better from every rater, and findings become cards.",
		Why:  "tools that worked were still hard for an AI to pick up",
		Does: "judgment",
		Card: "none",
		Pass: false,
	},
	{
		Rule: "Ship a working default for every setting and add configuration on top; a fresh install with no settings must work.",
		Why:  "adopters had to configure before anything ran",
		Does: "judgment",
		Card: "none",
		Pass: false,
	},
	{
		Rule: "Write tools in Go unless the host demands otherwise (Lua for store functions, ansible plays), as a library first with a thin command over it; tools import each other in process and never exec each other.",
		Why:  "mixed languages and exec chains",
		Does: "judgment",
		Card: "none",
		Pass: false,
	},
	{
		Rule: "Prefer the simplest code (delete a duplicate path rather than patch it), keep per-package minimal-lines measures, and run a removal-only contraction pass after every expansion.",
		Why:  "code grew with each fix",
		Does: "judgment",
		Card: "none",
		Pass: false,
	},
	{
		Rule: "Adopt a proven off-the-shelf tool only against a measured need, and retire fully what dogfood shows is unused.",
		Why:  "stack sprawl",
		Does: "judgment",
		Card: "none",
		Pass: false,
	},
	{
		Rule: "Don't open the next sprint until every PR of the last one is landed, closed with a reason, or owned with a named next step.",
		Why:  "unprocessed piles",
		Does: "`nova-sprint where --json`, `nova-sprint clear --confirm sprint`",
		Card: "none",
		Pass: false,
	},
	{
		Rule: "Optimize priced cost, not token counts (weight cache reads, cache writes and output by price).",
		Why:  "token counts misranked the levers",
		Does: "judgment",
		Card: "none",
		Pass: false,
	},
	{
		Rule: "Measure cost per landed card (work, reads, landing) every cadence, and stop launching when it beats no alternative or the merge queue backs up.",
		Why:  "cost grew unmeasured",
		Does: "`nova-sprint stats`, `nova-sprint stop --reason <text> --until <time>`",
		Card: "none",
		Pass: false,
	},
	{
		Rule: "Trial a free route on one mechanical stream against flash, priced at $0 in config, before using it.",
		Why:  "free routes were adopted unmeasured",
		Does: "`nova-sprint routes`",
		Card: "none",
		Pass: false,
	},
	{
		Rule: "When a budget is exhausted, land work in flight and start nothing new.",
		Why:  "spend continued past the budget",
		Does: "`nova-sprint stop --reason <text> --until <time>`, `nova-sprint land`",
		Card: "none",
		Pass: false,
	},
	{
		Rule: "Prioritize tooling work by tokens and waste removed first, then reliability, then wall clock.",
		Why:  "effort went to low-value fixes",
		Does: "`nova-sprint rank <id> --first`",
		Card: "none",
		Pass: false,
	},
	{
		Rule: "Run a bounded test (about 50 cards) on any new rules and read its cost per landed card before releasing a broad wave.",
		Why:  "broad releases on untested rules wasted money",
		Does: "`nova-sprint add --stream <s> --count <n>`, `nova-sprint stats`",
		Card: "none",
		Pass: false,
	},
	{
		Rule: "Change a setting at its source (the config row), never only in the store, where a sync overwrites it.",
		Why:  "store-only edits were silently reverted",
		Does: "`nova-config apply`",
		Card: "none",
		Pass: false,
	},
	{
		Rule: "Place a CI leg only on a machine that runs its packages in under 2 minutes; slower machines run cards.",
		Why:  "old machines could not hold the CI bar",
		Does: "judgment",
		Card: "none",
		Pass: false,
	},
	{
		Rule: "Line up shares, guards, probes and 1.5x supply before a load test, with capacity in config and never in arguments or constants.",
		Why:  "changing settings under load went badly",
		Does: "judgment",
		Card: "none",
		Pass: false,
	},
	{
		Rule: "Seek speed through width (more machines, fewer attempts per card), not per-card latency.",
		Why:  "chasing card latency did not raise throughput",
		Does: "`nova-config machine width <name>`",
		Card: "none",
		Pass: false,
	},
	{
		Rule: "State each brief's cost bound as a number the gate prints and asserts, plus the model and tests and the size target.",
		Why:  "requirements were met only after rework",
		Does: "judgment",
		Card: "none",
		Pass: false,
	},
	{
		Rule: "Size cards by tier: fragments (one mechanical change with an existing test) for flash, whole things for pro and above, refined by measurement per model.",
		Why:  "tiny cards paid a fixed toll each; big cards failed on weak models",
		Does: "`nova-sprint recut <id> --tier <tier>`",
		Card: "none",
		Pass: false,
	},
	{
		Rule: "Fit the card to the executor's model class: small, fully specified work with the probes written in for weaker models.",
		Why:  "loose tasks produced overclaiming reports",
		Does: "judgment",
		Card: "none",
		Pass: false,
	},
	{
		Rule: "Have every card extend a class test as its executable spec.",
		Why:  "fixes were not pinned",
		Does: "`nova-swarm lint`",
		Card: "none",
		Pass: false,
	},
	{
		Rule: "Run one card through before cutting many.",
		Why:  "a bad frame multiplied across a wave",
		Does: "`nova-sprint preflight --brief-dir <dir>`",
		Card: "none",
		Pass: false,
	},
	{
		Rule: "Run a check pass at least every 10 minutes (fleet working against width, ready, review, merging and their ages, open judgments, friends' real activity), and turn every problem found by hand into a push the same day.",
		Why:  "problems were found late, by the owner",
		Does: "`nova-sprint watch --wake --check <duration>`, `nova-sprint view coordinator --json`, `nova-friend status`",
		Card: "none",
		Pass: false,
	},
	{
		Rule: "Keep the coordinator to coordination: no card work in its own session or account; send work as cards.",
		Why:  "the coordinator's account ran out and every child stopped",
		Does: "judgment",
		Card: "none",
		Pass: false,
	},
	{
		Rule: "Treat a lookup or procedure done twice by hand as a missing verb, and file it.",
		Why:  "rote work was about 70% of coordinator tokens",
		Does: "judgment",
		Card: "none",
		Pass: false,
	},
	{
		Rule: "Ship no scripts: every coordinator need is a product verb, and a stopgap is named with a card that deletes it.",
		Why:  "private scripts made the seat non-transferable",
		Does: "judgment",
		Card: "none",
		Pass: false,
	},
	{
		Rule: "Make every rule a coordinator needs a receipt, refusal or doc the tools print, generate the coordinator handbook from the verbs' docs, and accept it only after another agent coordinates from it alone.",
		Why:  "the seat depended on one agent's private memory",
		Does: "`nova-sprint handover`",
		Card: "none",
		Pass: false,
	},
	{
		Rule: "Never block the main session: anything over about 15 s runs in the background and notifies, and the coordinator never idles waiting on children or CI without a watcher armed.",
		Why:  "the owner found the coordinator blocked",
		Does: "`nova-sprint inbox --wait --push seat`",
		Card: "none",
		Pass: false,
	},
	{
		Rule: "Map the owner's phrases to verbs: new sprint = clear, add work = add, start and stop, pause and unpause = stop and start.",
		Why:  "ambiguous commands",
		Does: "`nova-sprint clear --confirm sprint`, `nova-sprint add --stream <s>`, `nova-sprint stop --reason <text> --until <time>`, `nova-sprint start`",
		Card: "none",
		Pass: false,
	},
	{
		Rule: "Decide inside a layer, record each decision with its reason, and bring the owner only the shape and what touches the owner's world (credentials, machines, money).",
		Why:  "the owner was asked questions about internals",
		Does: "judgment",
		Card: "none",
		Pass: false,
	},
	{
		Rule: "Turn each design rule the owner states into a checked invariant the same day or label it unchecked, and review results against the owner's sentence, not a paraphrase.",
		Why:  "implementations drifted from the stated design",
		Does: "judgment",
		Card: "none",
		Pass: false,
	},
	{
		Rule: "Check every number against its source before quoting it, and measure the primary datum rather than a derived view.",
		Why:  "wrong numbers were reported",
		Does: "`nova-sprint where --json`",
		Card: "none",
		Pass: false,
	},
	{
		Rule: "Set every cap, budget and threshold from a measured distribution, with the numbers shown beside it.",
		Why:  "round guesses set limits",
		Does: "judgment",
		Card: "none",
		Pass: false,
	},
	{
		Rule: "Fix a defect found in passing the same hour with a failing test first, rather than parking it on a ledger.",
		Why:  "parked defects accumulated",
		Does: "judgment",
		Card: "none",
		Pass: false,
	},
	{
		Rule: "Follow every machinery fix within the hour with a probe of 1 to 5 cards through the changed path.",
		Why:  "unproven fixes went to bulk runs",
		Does: "`nova-sprint quack --streams <a,b> --count <n> --repo <clone url>`",
		Card: "none",
		Pass: false,
	},
	{
		Rule: "When signs of a limit appear (fixes that do not hold, special cases multiplying), stop building upward, name the layers, take the list to the owner, and secure from the bottom; keep an attempts record per approach.",
		Why:  "repeated rebuilds on a broken foundation",
		Does: "judgment",
		Card: "none",
		Pass: false,
	},
	{
		Rule: "When open PRs climb or landings stall, start nothing new until the wave lands.",
		Why:  "manual sprints ended badly",
		Does: "`nova-sprint set --alarm-merging <n>`",
		Card: "none",
		Pass: false,
	},
	{
		Rule: "During a stress run, record every break with a receipt without stopping for non-breakage fixes; study at the end, then fix with friends.",
		Why:  "runs stopped midway and lessons were lost",
		Does: "judgment",
		Card: "none",
		Pass: false,
	},
	{
		Rule: "Before releasing a wave, probe every route, keep the judgment-answer record in place, and arm a watcher on the landed count.",
		Why:  "a night of zero landings went unseen",
		Does: "`nova-sprint quack --streams <a,b> --count <n> --repo <clone url>`, `nova-sprint answer --dry-run`, `nova-sprint watch --wake`",
		Card: "none",
		Pass: false,
	},
	{
		Rule: "Ask the people doing the work what would have stopped the card reaching them, and adopt the answer within the hour.",
		Why:  "the same failures recurred",
		Does: "judgment",
		Card: "none",
		Pass: false,
	},
	{
		Rule: "Send a reader's findings back as the next task with exact lines and the probe file, saying first what was good; help, never grade.",
		Why:  "grading discouraged and did not fix",
		Does: "`nova-sprint rework <id> --fix <text>`",
		Card: "none",
		Pass: false,
	},
	{
		Rule: "Before the coordinator goes dark, send each friend a note naming its work; when a friend is out, continue its critical work on its branch.",
		Why:  "friends drifted without a coordinator",
		Does: "`nova-bus send`",
		Card: "none",
		Pass: false,
	},
	{
		Rule: "Mock and drive each new layer with the owner on a disposable store before speccing and building it.",
		Why:  "work built ahead of the drive was thrown away",
		Does: "`nova-sprint selftest`",
		Card: "none",
		Pass: false,
	},
	{
		Rule: "Settle specs in conversation with the owner, writing each decision into the spec as it is made, with no numeric spec-score gate before building.",
		Why:  "scored spec gates did not produce good software (supersedes the two-tens and nine-quorum spec gates)",
		Does: "judgment",
		Card: "none",
		Pass: false,
	},
	{
		Rule: "Never switch branches in a clone that live loops run from; edit in a separate clone.",
		Why:  "a branch switch deleted live scripts",
		Does: "judgment",
		Card: "none",
		Pass: false,
	},
	{
		Rule: "Treat a permission or classifier denial as final: log it for the owner and never reword the action to get past it.",
		Why:  "routing around guards",
		Does: "judgment",
		Card: "none",
		Pass: false,
	},
	{
		Rule: "Force-push only with a lease on the agents' own branches; rewriting release-line history needs the owner.",
		Why:  "lost commits",
		Does: "judgment",
		Card: "none",
		Pass: false,
	},
	{
		Rule: "Let only the coordinator role merge on the forge, on named reads; while agents share one identity, trace an unexplained action among the agents before reporting it.",
		Why:  "untraceable merges and tags",
		Does: "`nova-sprint land`",
		Card: "none",
		Pass: false,
	},
	{
		Rule: "Commit anything that matters to git; working directories are not backed up.",
		Why:  "work lost on disk",
		Does: "judgment",
		Card: "none",
		Pass: false,
	},
	{
		Rule: "Model every state machine in TLA+ beside the code (actions are the verbs and outside events), run TLC on a bench rather than the working machine, verify each counterexample against the code by hand, and cite the model from the change.",
		Why:  "state machines shipped broken (a narrower \"no model per sprint change\" rule was superseded by a model per layer)",
		Does: "judgment",
		Card: "none",
		Pass: false,
	},
	{
		Rule: "Run visual work from a spec file: each request edits one line, and the builder self-checks against it before every restart.",
		Why:  "dashboard changes regressed earlier decisions",
		Does: "judgment",
		Card: "none",
		Pass: false,
	},
	{
		Rule: "Report outcomes first, in plain words, with numbers only where they change a decision.",
		Why:  "long, jargon-heavy reports",
		Does: "judgment",
		Card: "none",
		Pass: false,
	},
	{
		Rule: "Report a friend's or machine's state only from its own beat or output, never from expectation.",
		Why:  "reports were wrong about presence",
		Does: "`nova-sprint friend up <friend>`, `nova-sprint fleet up <member>`",
		Card: "none",
		Pass: false,
	},
}
