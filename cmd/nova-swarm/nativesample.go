package main

import (
	"math/big"
	"strconv"
	"sync"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/cardcost"
	"github.com/mas-bandwidth/nova-tools/pkg/swarm"
)

// THE LIVE SAMPLER, IN THE PROCESS THAT HOLDS THE CARD'S DEADLINE.
//
// "There is no supervisor on this route and none is spawned. While a launch runs, `native`
// reads the harness's own database under the job's data home (at both
// spellings, read-only) every `--usage-interval` seconds."
//
// WHY IT IS A GOROUTINE AND NOT A CASE IN THE LAUNCH'S SELECT. "A sample is never
// in the deadline's way: a read is given 5 seconds whatever the interval, no sample starts
// while one is unanswered, a read still unanswered at its limit is abandoned and counted as
// a failed read, and the deadline and a TERM from outside end the card at their own instants
// whatever a read is doing." A sample SYNCHRONOUSLY inside the select would make a slow
// read a stretch of time in which the deadline case cannot run -- the very fault the walWait
// comment in pkg/swarm/opencode.go records, where two slow queries spent ~40s in one sample. On this route the sample runs
// beside the select and never inside it, so the longest read this tool can suffer costs the
// deadline nothing.
//
// AND NOTHING EVER WAITS FOR IT. Stopping the sampler SIGNALS it and does not join it: a
// `sqlite3` that has stopped answering must not be able to hold the card's ending open, and
// a read already in flight is abandoned where it stands. What the sampler has learnt is read
// back under a mutex, which is the only thing the two goroutines share.
//
// IT NEVER SAMPLES usage.tsv: "that row is written after a launch's process group
// is dead, so it is the record of a stop and cannot be the cause of one."

// THE STOP WORDS, the `stopped=` field's whole vocabulary:
// "A card the machinery stopped under this rule carries one more field, and its value names
// the budget: `stopped=tokens`, `stopped=max_turns`, `stopped=max_cache_read`, or
// `stopped=unverifiable`. It is a key of its own."
const (
	stoppedTokens       = "tokens"
	stoppedMaxTurns     = "max_turns"
	stoppedMaxCacheRead = "max_cache_read"
	stoppedUnverifiable = "unverifiable"
	stoppedUSD          = "usd"
)

// A FAILED READ NEVER ENDS A CARD BY ITSELF. Three failed reads in a row used to end it
// `stopped=unverifiable`: on the night of 2026-10-03 one member ended 32 cards that way in three
// machine-wide bursts (6, 17 and 6 cards within seconds of each other) because every sqlite3
// launch stalled in the machine's own directory lookups while its Wi-Fi flapped, and each end
// was charged to its card. Now the sampler keeps the last answer and, while reads fail,
// enforces the ceiling against that answer plus a bounded extrapolation: the most the spend
// rose between two answered reads, once for each failed sample since (fold). The card ends
// unverifiable once that figure is at the ceiling (atCeiling); the time bound applies only
// when extrapolation never reaches the ceiling; a source that answers again resumes the budget
// on what it says. A read that
// fails is tried once more at once before it is counted (readOnce), and the limit a read is
// given follows the slowest answered read (readLimit).
const UnverifiableAfter = 5 * time.Minute

// LiveSampleMost bounds the limit one read is given: four times the slowest answered read,
// never under swarm.LiveSampleLimit and never over this (readLimit).
const LiveSampleMost = 60 * time.Second

// SampleRiseFloor is what the max rise between answered reads is seeded with until two
// answers exist (fold). An outage right after the first answer extrapolates from it, so
// that a single answered read followed by silence does not extrapolate zero.
const SampleRiseFloor = 100

// liveSampler is one launch's sampling loop.
type liveSampler struct {
	dataHome string
	interval time.Duration
	// now is the clock and read the one read (swarm.ReadJobUsageLiveWithin); nil is each
	// one's own, a test gives its fakes.
	now  func() time.Time
	read func(dataHome string, limit time.Duration) (swarm.ProviderUsage, error)
	// THE BUDGETS THIS SAMPLER WATCHES. tokens/unmetered belong to the token budget, worker
	// carries the card budget's max_turns and max_cache_read, and logPath is the capture the turn count is
	// asked of -- `<job>/harness-output.log` on this route and never `harness.log`.
	tokens    int
	unmetered bool
	// usd is the dollar budget (--usd): the harness's own cost, read with the tokens, at
	// or past which the card is stopped `stopped=usd`; nil for none (nova-tools #5094).
	usd       *big.Rat
	worker    *swarm.Worker
	logPath   string
	cardLabel string

	stop chan struct{}
	once sync.Once
	// fired carries the ONE stop word this sampler ever sends, and it is buffered and sent
	// at most once: the launch's select reads it, ends the card, and nothing after that can
	// re-stop a card that is already stopping. A stop is terminal, so a second
	// word would have nowhere to go and a send that blocked would leak this goroutine.
	fired     chan string
	firedOnce sync.Once

	mu sync.Mutex
	// spent, observed and partial are the last ANSWER a sample got, folded the way the
	// line folds them. They are the JOB's running figures: the data home is the job's, so
	// a read of it counts every launch that has run into it so far --
	// "every launch counted from the first launch's start" with no arithmetic of its own.
	spent    int
	observed bool
	partial  bool
	// failures is the number of CONSECUTIVE reads that failed. A card ends on the
	// third; two failures and then an answer end nothing, so an answer resets it.
	failures int
	// lastErr is the reason the most recent failed read gave, for the line that reports an
	// unverifiable end.
	lastErr error
	// answered is when a read last answered (the start, until one has); rise is the most
	// the spend rose between two answered reads and costRise the most the cost did, with
	// lastCost the cost the last answer reported: what an outage's extrapolation is made of
	// (atCeiling). slowest is the longest an answered read took (readLimit).
	answered time.Time
	rise     int
	costRise *big.Rat
	lastCost *big.Rat
	slowest  time.Duration
	// answers is how many answered reads have reported figures (usage.Observed), to know
	// when two answers exist for the rise calculation.
	answers int
	// reached is the stop word a budget that has been reached would fire, or "" when none
	// has. It is kept as well as fired so that the "once more before any relaunch" test can
	// ask the question again without a channel.
	reached string
	// defect is the PROMPT-DEFECT line a card budget's stop owes, built at the instant the
	// budget fired, from the figures that fired it. The line prints on native's own
	// stdout AFTER the NATIVE OK line and writes it into no file: a created
	// RESULT.md without the contract line would score the card `line1-mismatch`, so the
	// published report is kept byte for byte.
	defect string
}

// startLiveSampler begins sampling and returns the sampler. A zero or negative interval, or
// an empty data home, returns a sampler that never reads: `native` has already refused an
// interval it cannot use, so this is the belt on a caller that built a config by hand.
func startLiveSampler(dataHome string, interval time.Duration, cfg nativeRunConfig, logPath string) *liveSampler {
	s := &liveSampler{
		dataHome: dataHome, interval: interval,
		tokens: cfg.tokens, unmetered: cfg.unmetered, usd: cfg.usd, worker: cfg.worker, logPath: logPath,
		cardLabel: cfg.label,
		stop:      make(chan struct{}), fired: make(chan string, 1),
	}
	s.answered = s.clock()
	if interval <= 0 || dataHome == "" {
		s.Stop()
		return s
	}
	go s.loop()
	return s
}

// clock is now: the sampler's own, else the wall's.
func (s *liveSampler) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

// reader is the one read: the sampler's own, else rule 13d's live read.
func (s *liveSampler) reader() func(string, time.Duration) (swarm.ProviderUsage, error) {
	if s.read != nil {
		return s.read
	}
	return swarm.ReadJobUsageLiveWithin
}

// readLimit is what the next read is given: four times the slowest answered read, never
// under swarm.LiveSampleLimit and never over LiveSampleMost. A machine whose every sqlite3
// launch stalls (one member, 2026-10-03: about 0.8 s in directory lookups) answers slowly and
// honestly, and a fixed limit counted its answers as failures.
func (s *liveSampler) readLimit() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	return min(max(swarm.LiveSampleLimit, 4*s.slowest), LiveSampleMost)
}

// Fired is the channel the ONE stop word arrives on. The launch's select reads it beside
// the child's exit, the deadline and a TERM, so a budget that fires ends the card on the
// same path those three do.
func (s *liveSampler) Fired() <-chan string { return s.fired }

// fire sends the stop word, once and never blocking. A sampler that has already fired says
// nothing more: a stop is terminal.
func (s *liveSampler) fire(word string) {
	s.firedOnce.Do(func() { s.fired <- word })
}

// StopWordAtFinal is the "once more before any relaunch" test asked of the
// job's FINAL reads as well as the samples: "The stop is `spent >= n`, tested at
// every sample and once more before any relaunch." A launch that dies fast, before any
// interval has elapsed, leaves no sample behind, so StopWord alone would be "" and a first
// launch that reached the budget alone would buy a second one.
// spent is the job's sum of every launch's final read, and observed says whether any of
// those reads answered; a word already reached by a sample is kept, so a card budget that
// fired first still names itself.
//
// cost is the job's cost over those final reads, the harness's own, "" when none reported
// one; the dollar budget is asked of it the same way (nova-tools #5094).
func (s *liveSampler) StopWordAtFinal(spent int, observed bool, cost string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.reached == "" && s.overUSD(cost) {
		s.reached = stoppedUSD
	}
	if s.reached == "" && observed && !s.unmetered && s.tokens > 0 && spent >= s.tokens {
		s.reached = stoppedTokens
	}
	return s.reached
}

// overUSD says whether cost, a harness's reported cost, is at or past the dollar budget. A
// cost not reported never reaches it: the token budget and the deadline stay the stops.
func (s *liveSampler) overUSD(cost string) bool {
	if s.usd == nil || cost == "" {
		return false
	}
	r, ok := new(big.Rat).SetString(cost)
	return ok && r.Cmp(s.usd) >= 0
}

// loop is the sampling itself: one read per tick, and NEVER two at once. The ticker is not
// used, deliberately -- a ticker would queue a tick behind a slow read and then fire it the
// instant the read returned, which is two samples back to back and not "every interval". The
// gap is taken AFTER the read returns, so a sample that took four seconds is followed by a
// full interval of quiet.
func (s *liveSampler) loop() {
	for {
		select {
		case <-s.stop:
			return
		case <-time.After(s.interval):
		}
		// A stop that landed while the gap was running ends the loop before another read
		// starts, so a card that has ended never opens a new `sqlite3`.
		select {
		case <-s.stop:
			return
		default:
		}
		s.readOnce()
	}
}

// readOnce takes one reading and folds it. The read is bounded by swarm.LiveSampleLimit
// inside ReadJobUsageLiveWithin, so an abandoned read returns here as an ordinary error and is
// counted as a failed read.
func (s *liveSampler) readOnce() {
	began, limit := s.clock(), s.readLimit()
	usage, err := s.reader()(s.dataHome, limit)
	if err != nil {
		// a read that fails is tried once more at once before it is counted: one launch
		// that stalled is not a source that stopped answering. The retry is capped to the
		// time remaining on the sample's limit, so one sample never exceeds the read limit.
		if rem := limit - s.clock().Sub(began); rem > 0 {
			usage, err = s.reader()(s.dataHome, rem)
		}
	}
	if took := s.clock().Sub(began); err == nil {
		s.mu.Lock()
		s.slowest = max(s.slowest, took)
		s.mu.Unlock()
	}
	// THE CARD'S OWN TURN COUNT is read OUTSIDE the lock, because it opens a file: the count is
	// the harness log's assistant turns, "or the usage row count where the log has
	// fewer", and on this route the log is `<job>/harness-output.log`.
	turns := 0
	if err == nil && s.worker != nil && s.worker.HasCardBudget() {
		turns = swarm.CountCardTurnsIn(s.logPath, usage)
	}
	s.fold(usage, err, turns)
}

// fold is one reading's answer, folded: the figures kept, and a budget reached fired. It
// opens nothing, so a test hands it a reading directly.
func (s *liveSampler) fold(usage swarm.ProviderUsage, err error, turns int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		// A READ THAT FAILS, which the sampler keeps apart from a source that has
		// reported nothing: the first ends a card on the third in a row, the second
		// leaves the budget unable to fire and the deadline to end the job.
		s.failures++
		s.lastErr = err
		// A NUMERIC BUDGET THE TOOL HAS STOPPED BEING ABLE TO SEE is enforced against the
		// last answer plus the extrapolation (atCeiling), and ends the card once that
		// figure is at the ceiling; the time bound applies only when extrapolation never
		// reaches the ceiling (the comment on UnverifiableAfter). An `unmetered` card with
		// no card budget has nothing to verify, so a reader that fails costs it nothing --
		// there is no promise to break.
		if s.watching() && s.reached == "" && (s.atCeiling() || (!s.observed && s.clock().Sub(s.answered) >= UnverifiableAfter)) {
			s.reached = stoppedUnverifiable
			s.fire(stoppedUnverifiable)
		}
		return
	}
	s.failures, s.lastErr = 0, nil
	s.answered = s.clock()
	if !usage.Observed {
		// Nothing reported yet. It is an absence, so it changes no figure: a zero here
		// would be a measurement the harness never made. The budget cannot fire, and the
		// deadline still ends the card.
		return
	}
	sum, seen, partial := usage.Budget()
	s.answers++
	switch {
	case s.answers == 1:
		s.rise = SampleRiseFloor
	case s.answers == 2:
		s.rise = max(0, sum-s.spent)
	default:
		if sum > s.spent {
			s.rise = max(s.rise, sum-s.spent)
		}
	}
	if cost, ok := new(big.Rat).SetString(usage.Values["cost"]); ok && usage.Values["cost"] != "" {
		if s.lastCost != nil && cost.Cmp(s.lastCost) > 0 {
			if rose := new(big.Rat).Sub(cost, s.lastCost); s.costRise == nil || rose.Cmp(s.costRise) > 0 {
				s.costRise = rose
			}
		}
		s.lastCost = cost
	}
	s.spent, s.partial, s.observed = sum, partial, seen > 0
	if s.reached != "" {
		return
	}
	// RULE 13b's CARD BUDGET, BESIDE THE TOKEN BUDGET and asked first, so that a card which
	// crossed both is named by the one the description set: `stopped=` says WHICH budget
	// fired, and a cache-read runaway reported as `stopped=tokens` would send its reader to
	// the wrong prompt.
	if s.worker != nil && s.worker.HasCardBudget() {
		if over, cacheRead, max := s.worker.OverCardBudget(usage, turns); over {
			word := stoppedMaxTurns
			if max == s.worker.MaxCacheRead && s.worker.MaxCacheRead > 0 && cacheRead > s.worker.MaxCacheRead {
				word = stoppedMaxCacheRead
			}
			s.reached = word
			s.defect = swarm.PromptDefectLine(s.label(), cacheRead, max, turns)
			s.fire(word)
			return
		}
	}
	// THE DOLLAR BUDGET, asked before the token budget: it is the
	// budget the route bounds cost by, so a sample past both names it.
	if s.overUSD(usage.Values["cost"]) {
		s.reached = stoppedUSD
		s.fire(stoppedUSD)
		return
	}
	// THE TOKEN BUDGET: `spent >= n`, so a sum exactly equal to the budget ends
	// the card. A partial observation can reach it and stop the card, and can never show
	// that the card stayed under it, which is what the plus on the line says.
	if !s.unmetered && s.tokens > 0 && seen > 0 && sum >= s.tokens {
		s.reached = stoppedTokens
		s.fire(stoppedTokens)
	}
}

// atCeiling says whether the spend extrapolated over the failed samples since the last
// answer is at a ceiling: the last answer plus the most it ever rose between two answers,
// once per failed sample, against the token budget and against the dollar budget. A
// sampler that never saw a figure, or never saw it rise, extrapolates nothing: the deadline
// ends such a card, as it does one whose source reports nothing.
func (s *liveSampler) atCeiling() bool {
	if s.observed && !s.unmetered && s.tokens > 0 && s.spent+s.failures*s.rise >= s.tokens {
		return true
	}
	if s.usd != nil && s.lastCost != nil && s.costRise != nil {
		cost := new(big.Rat).Add(s.lastCost, new(big.Rat).Mul(s.costRise, big.NewRat(int64(s.failures), 1)))
		return cost.Cmp(s.usd) >= 0
	}
	return false
}

// watching says whether this sampler has a promise to keep: a numeric budget, or a card
// budget. A sampler with neither reads nothing that anybody is relying on, so a reader that
// stops answering it ends no card.
func (s *liveSampler) watching() bool {
	return (!s.unmetered && s.tokens > 0) || s.usd != nil || (s.worker != nil && s.worker.HasCardBudget())
}

// label is the id the PROMPT-DEFECT line names. The line is spelled
// `PROMPT-DEFECT task=<id> ...` and the id is not re-said on this route,
// so it is the CARD'S LABEL -- the same token `NATIVE OK label=` carries one line above it,
// which is the only id a reader of native's stdout has to join the two by.
func (s *liveSampler) label() string {
	if s.cardLabel != "" {
		return s.cardLabel
	}
	return "-"
}

// Defect is the PROMPT-DEFECT line a card budget's stop owes, or "" when no card budget
// fired.
func (s *liveSampler) Defect() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.defect
}

// Stop signals the loop and RETURNS AT ONCE. It never joins: a read in flight is abandoned
// where it stands, because the deadline and a TERM end the card at their own instants
// whatever a read is doing. It is safe to call more than once and from any goroutine.
func (s *liveSampler) Stop() { s.once.Do(func() { close(s.stop) }) }

// Observed is what the sampler has seen so far: the last answered sample's figures, the
// count of consecutive failed reads, and the reason the last failure gave.
func (s *liveSampler) Observed() (spent int, observed, partial bool, failures int, lastErr error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.spent, s.observed, s.partial, s.failures, s.lastErr
}

// Why is the reason the last failed read gave, in its own words, or "" when the last read
// answered. It is what an unverifiable end carries onto the NATIVE BUDGET line: three reads
// that each did not answer within the limit and three that each exited on a locked database
// both end a card `stopped=unverifiable`, and the member that reads the line cannot tell
// one from the other without it (one member, 2026-10-03: 32 cards ended this way in three
// bursts, and the record said only that the source stopped answering).
func (s *liveSampler) Why() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lastErr == nil {
		return ""
	}
	return s.lastErr.Error()
}

// nativeBudgetWords is which budget ended the card and at what count, with what the job
// cost (the harness's own figure, to the cent and rounded up): the words of the NATIVE
// BUDGET line, which the member carries into the finish's reason after the end
// (nova-tools #5094): "tokens 509,940 of 400,000, $0.03". tokens is the --tokens budget,
// 0 when unmetered; cost is the job's spend= cost, "" when the harness reported none; why
// is the last failed read's reason for an unverifiable end, "" for every other stop.
func nativeBudgetWords(stopped string, tokens, spent int, partial bool, cost, usd, why string) string {
	count := groupThousands(spent)
	if partial {
		count += "+"
	}
	if tokens > 0 {
		count += " of " + groupThousands(tokens)
	}
	money := "cost unreported"
	if r, ok := new(big.Rat).SetString(cost); ok && cost != "" {
		money = cardcost.Cents(r)
	}
	switch stopped {
	case stoppedUSD:
		limit, _ := new(big.Rat).SetString(usd)
		if limit == nil {
			limit = new(big.Rat)
		}
		return money + " of " + cardcost.Cents(limit) + ", tokens " + count
	case stoppedTokens:
		return "tokens " + count + ", " + money
	case stoppedUnverifiable:
		// why is the last failed read's own reason (liveSampler.Why), and it rides inside
		// the sentence so that the figures keep their place at the end of the line.
		if why != "" {
			return "unverifiable: the usage source stopped answering (last read: " + why + "), tokens " + count + ", " + money
		}
		return "unverifiable: the usage source stopped answering, tokens " + count + ", " + money
	}
	return stopped + ", tokens " + groupThousands(spent) + ", " + money
}

// groupThousands is n with a comma between each group of three digits: 509,940.
func groupThousands(n int) string {
	s := strconv.Itoa(n)
	for i := len(s) - 3; i > 0 && s[i-1] != '-'; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}
