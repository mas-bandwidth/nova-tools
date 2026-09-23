package main

import (
	"sync"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// THE LIVE SAMPLER, IN THE PROCESS THAT HOLDS THE CARD'S DEADLINE (SPEC-SWARM rule 13d,
// issue #1545).
//
// "There is no supervisor on this route and none is spawned. While a launch runs, `native`
// reads the harness's own database under the job's data home (rule 13's source, at both
// spellings, read-only) every `--usage-interval` seconds."
//
// WHY IT IS A GOROUTINE AND NOT A CASE IN THE LAUNCH'S SELECT. Rule 13d: "a sample is never
// in the deadline's way: a read is given 5 seconds whatever the interval, no sample starts
// while one is unanswered, a read still unanswered at its limit is abandoned and counted as
// a failed read, and the deadline and a TERM from outside end the card at their own instants
// whatever a read is doing." The pool route samples SYNCHRONOUSLY inside its select
// (internal/swarm/supervise.go), and there a slow read is a stretch of time in which the
// deadline case cannot run -- the very fault the walWait comment in internal/swarm/opencode.go
// records, where two slow queries spent ~40s in one sample. On this route the sample runs
// beside the select and never inside it, so the longest read this tool can suffer costs the
// deadline nothing.
//
// AND NOTHING EVER WAITS FOR IT. Stopping the sampler SIGNALS it and does not join it: a
// `sqlite3` that has stopped answering must not be able to hold the card's ending open, and
// a read already in flight is abandoned where it stands. What the sampler has learnt is read
// back under a mutex, which is the only thing the two goroutines share.
//
// IT NEVER SAMPLES usage.tsv (rule 13d): "that row is written after a launch's process group
// is dead, so it is the record of a stop and cannot be the cause of one."

// THE STOP WORDS rule 13d names, and they are the `stopped=` field's whole vocabulary:
// "A card the machinery stopped under this rule carries one more field, and its value names
// the budget: `stopped=tokens`, `stopped=max_turns`, `stopped=max_cache_read`, or
// `stopped=unverifiable`. It is a key of its own."
const (
	stoppedTokens       = "tokens"
	stoppedMaxTurns     = "max_turns"
	stoppedMaxCacheRead = "max_cache_read"
	stoppedUnverifiable = "unverifiable"
)

// unverifiableSamples is how many CONSECUTIVE failed reads end a card (rule 13, quoted by
// 13d): "a read that fails ... on three consecutive samples ends the card exactly as the
// budget does ... two failures and then an answer end nothing".
const unverifiableSamples = 3

// liveSampler is one launch's sampling loop.
type liveSampler struct {
	dataHome string
	interval time.Duration
	// THE BUDGETS THIS SAMPLER WATCHES. tokens/unmetered are rule 13's, worker carries rule
	// 13b's max_turns and max_cache_read, and logPath is the capture the turn count is
	// asked of -- `<job>/harness-output.log` on this route and never `harness.log`.
	tokens    int
	unmetered bool
	worker    *swarm.Worker
	logPath   string
	cardLabel string

	stop chan struct{}
	once sync.Once
	// fired carries the ONE stop word this sampler ever sends, and it is buffered and sent
	// at most once: the launch's select reads it, ends the card, and nothing after that can
	// re-stop a card that is already stopping. A stop is terminal (rule 13d), so a second
	// word would have nowhere to go and a send that blocked would leak this goroutine.
	fired     chan string
	firedOnce sync.Once

	mu sync.Mutex
	// spent, observed and partial are the last ANSWER a sample got, folded the way the
	// line folds them. They are the JOB's running figures: the data home is the job's, so
	// a read of it counts every launch that has run into it so far, which is rule 13d's
	// "every launch counted from the first launch's start" with no arithmetic of its own.
	spent    int
	observed bool
	partial  bool
	// failures is the number of CONSECUTIVE reads that failed. Rule 13d ends a card on the
	// third; two failures and then an answer end nothing, so an answer resets it.
	failures int
	// lastErr is the reason the most recent failed read gave, for the line that reports an
	// unverifiable end.
	lastErr error
	// samples is how many reads have been ANSWERED, and inflight how many are running. The
	// two exist so a test can prove that no sample starts while one is unanswered without
	// reaching into the loop's own timing.
	samples   int
	inflight  int
	maxFlight int
	// reached is the stop word a budget that has been reached would fire, or "" when none
	// has. It is kept as well as fired so that the "once more before any relaunch" test can
	// ask the question again without a channel.
	reached string
	// defect is the PROMPT-DEFECT line a card budget's stop owes, built at the instant the
	// budget fired, from the figures that fired it. Rule 13d prints it on native's own
	// stdout AFTER the NATIVE OK line and writes it into no file (decision 16): a created
	// RESULT.md without the contract line would score the card `line1-mismatch`, and 13d
	// promises the published report is kept byte for byte.
	defect string
}

// startLiveSampler begins sampling and returns the sampler. A zero or negative interval, or
// an empty data home, returns a sampler that never reads: `native` has already refused an
// interval it cannot use, so this is the belt on a caller that built a config by hand.
func startLiveSampler(dataHome string, interval time.Duration, cfg nativeRunConfig, logPath string) *liveSampler {
	s := &liveSampler{
		dataHome: dataHome, interval: interval,
		tokens: cfg.tokens, unmetered: cfg.unmetered, worker: cfg.worker, logPath: logPath,
		cardLabel: cfg.label,
		stop:      make(chan struct{}), fired: make(chan string, 1),
	}
	if interval <= 0 || dataHome == "" {
		s.Stop()
		return s
	}
	go s.loop()
	return s
}

// Fired is the channel the ONE stop word arrives on. The launch's select reads it beside
// the child's exit, the deadline and a TERM, so a budget that fires ends the card on the
// same path those three do.
func (s *liveSampler) Fired() <-chan string { return s.fired }

// fire sends the stop word, once and never blocking. A sampler that has already fired says
// nothing more: rule 13d's stop is terminal.
func (s *liveSampler) fire(word string) {
	s.firedOnce.Do(func() { s.fired <- word })
}

// StopWord is the word a budget that has ALREADY been reached would fire, or "" when none
// has. It is the "once more before any relaunch" test of rule 13d, asked between launches
// where there is no select to read a channel: "The stop is rule 13's `spent >= n`, tested at
// every sample and once more before any relaunch."
func (s *liveSampler) StopWord() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reached
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
// inside ReadJobUsageLive, so an abandoned read returns here as an ordinary error and is
// counted as a failed read, which is what rule 13d asks for.
func (s *liveSampler) readOnce() {
	s.enter()
	usage, err := swarm.ReadJobUsageLive(s.dataHome)
	s.leave()
	// THE CARD'S OWN TURN COUNT is read OUTSIDE the lock, because it opens a file: rule 13b
	// counts the harness log's assistant turns, "or the usage row count where the log has
	// fewer", and on this route the log is `<job>/harness-output.log`.
	turns := 0
	if err == nil && s.worker != nil && s.worker.HasCardBudget() {
		turns = swarm.CountCardTurnsIn(s.logPath, usage)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.samples++
	if err != nil {
		// A READ THAT FAILS, which rule 13 and 13d keep apart from a source that has
		// reported nothing: the first ends a card on the third in a row, the second
		// leaves the budget unable to fire and the deadline to end the job.
		s.failures++
		s.lastErr = err
		// A NUMERIC BUDGET THE TOOL HAS STOPPED BEING ABLE TO SEE is a budget the caller
		// believes is enforced and is not. It ends the card exactly as the budget does.
		// An `unmetered` card with no card budget has nothing to verify, so a reader that
		// fails costs it nothing -- there is no promise to break.
		if s.watching() && s.failures >= unverifiableSamples && s.reached == "" {
			s.reached = stoppedUnverifiable
			s.fire(stoppedUnverifiable)
		}
		return
	}
	s.failures, s.lastErr = 0, nil
	if !usage.Observed {
		// Nothing reported yet. It is an absence, so it changes no figure: a zero here
		// would be a measurement the harness never made. The budget cannot fire, and the
		// deadline still ends the card.
		return
	}
	sum, seen, partial := usage.Budget()
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
	// THE TOKEN BUDGET: rule 13's `spent >= n`, so a sum exactly equal to the budget ends
	// the card. A partial observation can reach it and stop the card, and can never show
	// that the card stayed under it, which is what the plus on the line says.
	if !s.unmetered && s.tokens > 0 && seen > 0 && sum >= s.tokens {
		s.reached = stoppedTokens
		s.fire(stoppedTokens)
	}
}

// watching says whether this sampler has a promise to keep: a numeric budget, or a card
// budget. A sampler with neither reads nothing that anybody is relying on, so a reader that
// stops answering it ends no card.
func (s *liveSampler) watching() bool {
	return (!s.unmetered && s.tokens > 0) || (s.worker != nil && s.worker.HasCardBudget())
}

// label is the id the PROMPT-DEFECT line names. Rule 13b spells the line
// `PROMPT-DEFECT task=<id> ...` and rule 13d does not re-say what the id is on this route,
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

// enter and leave record that a read is running, so a test can prove no two overlap.
func (s *liveSampler) enter() {
	s.mu.Lock()
	s.inflight++
	if s.inflight > s.maxFlight {
		s.maxFlight = s.inflight
	}
	s.mu.Unlock()
}

func (s *liveSampler) leave() {
	s.mu.Lock()
	s.inflight--
	s.mu.Unlock()
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

// Counts is how many samples were answered and whether any two ever overlapped. It exists
// for the tests that hold rule 13d's "no sample starts while one is unanswered".
func (s *liveSampler) Counts() (answered, maxInFlight int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.samples, s.maxFlight
}
