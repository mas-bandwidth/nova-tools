package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// quackBase is the test repository's branch quack cards start from and merge
// to when --base names none.
const quackBase = "dev"

// cmdQuack cuts quack cards, the sprint's end-to-end test cards, into a
// running store (docs/SPEC-SPRINT.md section 11, quack): count cards per
// stream, each a child's whole brief that asks for one file holding the word
// quack, the tiers taken in turn down each stream. Every id carries the run's
// stamp, `quack-<stamp>-<stream>-<nnn>`, and so does the file its card
// writes: twelve hex digits, so two passes share an id or a file name with a
// chance of about one in 2^48 per pair, and no card's diff is empty unless
// they do. The briefs are held to the card lint as add holds them, and one
// step adds every card. A call with --op takes its stamp from the op and the
// sprint's epoch (quackStamp), so the same call retried makes the same cards
// and replays the recorded result (the --op contract, docs/SPEC-SPRINT.md
// section 11), and other arguments under that op are refused as add refuses
// them.
func (a *app) cmdQuack(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("quack")
	streams := fs.String("streams", "", "the streams to cut quack cards into, comma separated (a stream new to the sprint is made)")
	count := fs.Int("count", 0, "quack cards per stream, at least 1")
	tiers := fs.String("tiers", "flash,pro", "the model tiers each stream's cards take in turn, comma separated: "+cardhdr.RouteList)
	repo := fs.String("repo", "", "the clone URL of the test repository the quack cards commit to")
	base := fs.String("base", quackBase, "the branch of the test repository the cards start from")
	pos, err := parse(fs, args)
	if err != nil {
		return refuse(stderr, "quack", err.Error())
	}
	// ONBOARDING point 2: one run names every problem it finds
	var problems []string
	if len(pos) > 0 {
		problems = append(problems, "takes no ids: the ids are quack-<stamp>-<stream>-<n>, made here")
	}
	ss := sprint.Split(*streams)
	if len(ss) == 0 {
		problems = append(problems, "wants --streams <a,b,...>, the streams to cut into")
	}
	for _, s := range ss {
		if !sprint.ValidID(s) {
			problems = append(problems, fmt.Sprintf("--streams: %q is not a stream id (letters, digits, _ and -)", s))
		}
	}
	if *count < 1 {
		problems = append(problems, "wants --count <n>, the quack cards per stream, at least 1")
	}
	ts := sprint.Split(*tiers)
	if len(ts) == 0 {
		problems = append(problems, "wants --tiers <t,...>: "+cardhdr.RouteList)
	}
	for _, t := range ts {
		if !cardhdr.IsRoute(t) {
			problems = append(problems, fmt.Sprintf("--tiers: %q is not a tier; want %s", t, cardhdr.RouteList))
		}
	}
	if *repo == "" {
		problems = append(problems, "wants --repo <clone url>, the test repository the cards commit to")
	}
	if *base == "" {
		problems = append(problems, "wants --base <branch>, the test repository's branch the cards start from")
	}
	if len(problems) > 0 {
		return refuse(stderr, "quack", strings.Join(problems, "; "))
	}
	var st *store.Store
	rs, code := a.briefRules("", c, &st, stderr)
	if code != 0 {
		return code
	}
	if st == nil {
		if st, err = a.store(*c); err != nil {
			return refuse(stderr, "quack", err.Error())
		}
	}
	var epoch uint64
	if c.op != "" {
		es, err := st.EpochNow(context.Background())
		if err != nil {
			return a.readFailed("quack", err, stderr)
		}
		epoch = es.N
	}
	stamp, err := quackStamp(c.op, epoch)
	if err != nil {
		return refuse(stderr, "quack", "no run stamp: "+err.Error())
	}
	var reqs []sprint.AddReq
	var all []sprint.CardAdd
	for _, s := range ss {
		r := sprint.AddReq{Stream: s, Who: c.actor}
		for i := 1; i <= *count; i++ {
			id := fmt.Sprintf("quack-%s-%s-%03d", stamp, s, i)
			card := sprint.CardAdd{ID: id, File: id, Brief: quackBrief(id, s, ts[(i-1)%len(ts)], *repo, *base, rs)}
			r.Cards = append(r.Cards, card)
			all = append(all, card)
		}
		reqs = append(reqs, r)
	}
	// every card of every stream is checked before anything is written, and the
	// step adds every stream's cards or none (AddEachStep is named): one refusal
	// names every problem (ONBOARDING point 2)
	for _, r := range reqs {
		for _, card := range r.Cards {
			if !sprint.ValidID(card.ID) {
				over := len(card.ID) - sprint.MaxIDLen
				problems = append(problems, fmt.Sprintf("stream %s makes card ids of %d characters (%s), over the %d a card id may be: give a stream of at most %d characters",
					r.Stream, len(card.ID), card.ID, sprint.MaxIDLen, len(r.Stream)-over))
				break
			}
		}
		for _, card := range r.Cards {
			if len(card.Brief) > store.MaxBriefBytes {
				problems = append(problems, fmt.Sprintf("card %s: the brief is %d bytes, over the %d a brief may be", card.ID, len(card.Brief), store.MaxBriefBytes))
			}
		}
	}
	if len(problems) > 0 {
		return refuse(stderr, "quack", strings.Join(problems, "; "))
	}
	if code := lintBriefFiles(all, rs, c.max, stderr); code != 0 {
		return code
	}
	if len(reqs) == 1 {
		return a.runStep("quack", *c, st, store.AddStep(reqs[0]), stdout, stderr)
	}
	return a.runStep("quack", *c, st, store.AddEachStep(reqs), stdout, stderr)
}

// quackStampBytes is the stamp's length: six bytes, twelve hex digits. A
// cleared sprint forgets its cards but the test repository's history keeps
// their files, so the stamp alone keeps a new pass's files new.
const quackStampBytes = 6

// quackStamp is a run's stamp: with an op id, the first bytes of the SHA-256
// of the op and the sprint's epoch, so a retry of the call makes the same
// cards and a clear gives the same op new ones; with none, bytes from the
// system's random source.
func quackStamp(op string, epoch uint64) (string, error) {
	b := make([]byte, quackStampBytes)
	if op != "" {
		sum := sha256.Sum256([]byte("quack\x00" + op + "\x00" + strconv.FormatUint(epoch, 10)))
		copy(b, sum[:])
	} else if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// quackBrief is one quack card's whole brief: line 1 names the card and its
// tier (cardhdr.ReadModel), BASE and REPO the staged checkout, then what to
// do, the known answer, how it finishes and how it is read, and the RULES
// paragraph of the rule set the sprint holds briefs to (swarm.RulesParagraph),
// so the card lint passes by construction.
func quackBrief(id, stream, tier, repo, base string, rules []swarm.ChildRule) string {
	file := "quacks/" + id + ".txt"
	lines := []string{
		fmt.Sprintf("%s: quack like a duck (%s) tier: %s", id, stream, tier),
		"BASE: " + base,
		"REPO: " + repo,
		"The member stages this job: you start in the job directory (export JOB=$PWD). Read $JOB/JOB.md first: it names the staged checkout (a full clone at BASE) and how this card finishes. Work in the staged checkout, commit as usual, and finish as JOB.md says.",
		"Needs: none",
		"Libraries considered: none; this card writes no code.",
		"Why: a quack card proves the whole chain (deal, run, finish, two reads, merge) on every route with a known answer, in about a minute.",
		fmt.Sprintf("What: Quack like a duck. Create the file %s in the staged checkout containing exactly one line, `quack`, commit it with the message `quack: %s`, and finish as JOB.md says. Your report (the PR title, or the output and report fields of RESULT.md) must contain the word quack. Do nothing else: read no other file, run no test, change no other file. A quack card runs in under a minute.", file, id),
		"Tests: none. The known answer is the word quack in the report and the one-line file in the diff.",
		fmt.Sprintf("Files: %s only.", file),
		fmt.Sprintf("Gate: none; `cat %s` prints quack.", file),
		fmt.Sprintf("Finish: the report is the PR. `gh pr create` with the title `quack: %s` and the body `quack` plus the diff stat, last line 🤖 Generated with [Claude Code](https://claude.com/claude-code); or, as JOB.md says for your profile, RESULT.md with output and report both containing quack.", id),
		fmt.Sprintf("As a read (JOB.md says kind read): change nothing, commit nothing. Approve (`gh pr review --approve`) when, and only when, the report contains the word quack and the diff is exactly the one file %s holding the one line quack; otherwise `gh pr review --request-changes --body <what is missing>`. A read of a quack takes under a minute: do not run tests or read the repo.", file),
	}
	return strings.Join(lines, "\n") + "\n\n" + strings.TrimSuffix(swarm.RulesParagraph(rules), "\n")
}
