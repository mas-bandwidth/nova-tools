package card

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/redis/go-redis/v9"
)

func keyCard(sprint, label string) string  { return "s:" + sprint + ":card:" + label }
func keyPool(sprint string) string         { return "s:" + sprint + ":pool" }
func keyWaiting(sprint string) string      { return "s:" + sprint + ":waiting" }
func keyLog(sprint string) string          { return "s:" + sprint + ":log" }
func keyIdx(sprint, state string) string   { return "s:" + sprint + ":idx:card:" + state }
func keyTask(sprint, id string) string     { return "s:" + sprint + ":task:" + id }
func keyStream(sprint, slug string) string { return "s:" + sprint + ":stream:" + slug }

// Push is PushWith under no options.
func Push(ctx context.Context, client *redis.Client, sprint string, body []byte) VerbResult {
	return PushWith(ctx, client, sprint, body, PushOptions{})
}

// PushOptions are card push's flags.
type PushOptions struct {
	// MapKind rewrites a classification KIND to its RESULT kind by KindMap
	// (MapKind, kinds.go) before lint, so the payload sha is the sha of the
	// card as stored, KIND line included.
	MapKind bool
}

// PushWith is PushBatch of one card under opts: its one result.
func PushWith(ctx context.Context, client *redis.Client, sprint string, body []byte, opts PushOptions) VerbResult {
	return PushBatch(ctx, client, sprint, []CardFile{{Body: body}}, opts)[0]
}

// CardFile is one card body for PushBatch. Name (a file path) prefixes a
// refusal so the operator knows which card of the batch it was; "" does not.
type CardFile struct {
	Name string
	Body []byte
}

// PushBatch lints every card, then writes each into the pool or the waiting
// set. It makes at most two Redis round trips for any number of cards (spec
// #2756 section 4, nova-tools#3266): one pipeline reads every card's local
// DEPENDS-ON records, one pipeline sends every card's FCALL ns_card_push. It
// never loads the function library: the fleet's library is the deploy's job
// (#3551), and a missing function is refused with the remedy nova-sprint fn
// load.
//
// A missing required line, a KIND that is not a RESULT or runner kind
// (kinds.go), a private repository, a card, task or stream dependency with no
// record (neither in the sprint nor pushed earlier in this batch), or a dead
// task dependency refuses the whole batch before any write: the result is
// that one refusal. Otherwise the results are one per card, in order: exit 0
// wrote or found the same card, exit 4 a label whose payload differs, exit 2 a
// BENCH: not in the benches set or Redis did not accept that card's write. A
// dependency that is not landed goes to waiting, not the pool.
func PushBatch(ctx context.Context, client *redis.Client, sprint string, files []CardFile, opts PushOptions) []VerbResult {
	if !sprintRE.MatchString(sprint) {
		return []VerbResult{refused("sprint name must match [a-z0-9-]{1,40}")}
	}
	if client == nil {
		return []VerbResult{refused("redis client is required")}
	}
	if len(files) == 0 {
		return []VerbResult{refused("no card to push")}
	}
	docs := make([]cardDoc, len(files))
	for i, f := range files {
		body := f.Body
		if opts.MapKind {
			body, _, _ = MapKind(body)
		}
		doc, err := lint(ctx, body)
		if err != nil {
			return []VerbResult{refused(named(f.Name, err.Error()))}
		}
		docs[i] = doc
	}
	ready, i, err := batchDependenciesReady(ctx, client, sprint, docs)
	if err != nil {
		return []VerbResult{refused(named(files[i].Name, err.Error()))}
	}
	pipe := client.Pipeline()
	cmds := make([]*redis.Cmd, len(docs))
	headers := make([]*redis.Cmd, len(docs))
	for i, doc := range docs {
		keys := []string{
			keyCard(sprint, doc.Label),
			keyPool(sprint),
			keyWaiting(sprint),
			keyLog(sprint),
			keyIdx(sprint, "queued"),
		}
		// Arguments 14-18 are bench, est, test, stream and origin (#3650,
		// #3653, #3689, #3692); 19 is leg (#3255).
		cmds[i] = pipe.FCall(ctx, "ns_card_push", keys,
			doc.Label, doc.Payload, doc.Priority, doc.Base, doc.BaseSHA, doc.Paths, doc.Repo, doc.Kind,
			doc.DependsOn, doc.Type, doc.TypedDependsOn, boolString(ready[i]), doc.Route, doc.Bench, doc.Est, doc.Test,
			doc.Stream, doc.Origin, doc.Leg,
		)
		// Then, in the same pipeline, ns_card_header writes the card's
		// DONE-WHEN line (harvest's PR body, #2932) and its TASK line (the
		// PR title, #3712) only when the card is
		// stored from this payload; after EXISTS it writes nothing, after
		// CONFLICT it refuses.
		headers[i] = pipe.FCall(ctx, "ns_card_header", keys[:1], doc.Payload, doc.DoneWhen, doc.Task)
	}
	_, _ = pipe.Exec(ctx) // each reply is read, with its own error, below
	out := make([]VerbResult, len(docs))
	for i, doc := range docs {
		reply, err := cmds[i].Text()
		if err != nil && functionMissing(err) {
			return []VerbResult{refused(fmt.Sprintf("ns_card_push is not loaded in %s; card push never loads it: run nova-sprint fn load as the library's owner (%v)", fn.Library, err))}
		}
		if err == nil && reply == "NOBENCH" {
			out[i] = refused(named(files[i].Name, unregisteredBench(ctx, client, doc.Bench)))
			continue
		}
		out[i] = pushResult(sprint, doc.Label, reply, err)
		// The header reply counts only for a card this push stored.
		if err == nil && strings.HasPrefix(reply, "OK place=") && out[i].Code == exitOK {
			if h, herr := headers[i].Text(); herr != nil || h != "OK" {
				out[i] = refused(named(files[i].Name, fmt.Sprintf("card header reply %q %v", h, herr)))
			}
		}
	}
	return out
}

func named(name, reason string) string {
	if name == "" {
		return reason
	}
	return name + ": " + reason
}

func pushResult(sprint, label, reply string, err error) VerbResult {
	if err != nil {
		return refused(err.Error())
	}
	switch reply {
	case "EXISTS":
		return VerbResult{Code: exitOK, Stdout: pushLine(sprint, label, "exists")}
	case "CONFLICT":
		return VerbResult{Code: exitConflict, Stderr: oneline.Escape("label conflict") + "\n"}
	}
	place, ok := strings.CutPrefix(reply, "OK place=")
	if !ok || (place != "pool" && place != "waiting") {
		return refused(fmt.Sprintf("card push reply %q", reply))
	}
	return VerbResult{Code: exitOK, Stdout: pushLine(sprint, label, place)}
}

// unregisteredBench is the refusal for a BENCH: line naming a bench that is not
// in the benches set. It names the registered benches, read once on this
// refusal path only, so the remedy is on the line.
func unregisteredBench(ctx context.Context, client *redis.Client, bench string) string {
	names, err := client.SMembers(ctx, "benches").Result()
	registered := "unknown (" + fmt.Sprint(err) + ")"
	if err == nil {
		sort.Strings(names)
		registered = strings.Join(names, ",")
		if registered == "" {
			registered = "none"
		}
	}
	return fmt.Sprintf("BENCH: %s is not a registered bench (not in the benches set; registered: %s); name a registered bench or drop the BENCH: line", bench, registered)
}

// batchDependenciesReady resolves every card's DEPENDS-ON in one pipeline. A
// card dependency with no record in the sprint that an earlier card of this
// batch pushes counts as present and not ready: that card is queued, not
// landed, which is what a push one at a time would have read. On error it
// returns the index of the card refused.
func batchDependenciesReady(ctx context.Context, client *redis.Client, sprint string, docs []cardDoc) ([]bool, int, error) {
	pipe := client.Pipeline()
	reads := make([][]*redis.MapStringStringCmd, len(docs))
	for i, doc := range docs {
		reads[i] = queueDependencyReads(ctx, pipe, sprint, doc.Deps)
	}
	if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
		return nil, 0, fmt.Errorf("read DEPENDS-ON: %w", err)
	}
	ready := make([]bool, len(docs))
	earlier := map[string]bool{}
	for i, doc := range docs {
		ok, err := dependenciesReady(sprint, doc.Deps, reads[i], earlier)
		if err != nil {
			return nil, i, err
		}
		ready[i] = ok
		earlier[doc.Label] = true
	}
	return ready, 0, nil
}

func queueDependencyReads(ctx context.Context, pipe redis.Pipeliner, sprint string, deps []dependency) []*redis.MapStringStringCmd {
	reads := make([]*redis.MapStringStringCmd, len(deps))
	for i, dep := range deps {
		switch dep.Kind {
		case dependencyCard:
			reads[i] = pipe.HGetAll(ctx, keyCard(sprint, dep.Value))
		case dependencyTask:
			reads[i] = pipe.HGetAll(ctx, keyTask(sprint, dep.Value))
		case dependencyStream:
			reads[i] = pipe.HGetAll(ctx, keyStream(sprint, dep.Value))
		}
	}
	return reads
}

func dependenciesReady(sprint string, deps []dependency, reads []*redis.MapStringStringCmd, earlier map[string]bool) (bool, error) {
	ready := true
	for i, dep := range deps {
		if dep.Kind == dependencyGitHub {
			ready = false // the forge is read once by release, never once per push
			continue
		}
		fields := reads[i].Val()
		if len(fields) == 0 {
			if dep.Kind == dependencyCard && earlier[dep.Value] {
				ready = false
				continue
			}
			return false, missingDependency(sprint, dep)
		}
		if dep.Kind == dependencyTask && deadTaskState(fields["state"]) {
			return false, fmt.Errorf("DEPENDS-ON: task:%s is %s in sprint %s and will never finish", dep.Value, fields["state"], sprint)
		}
		if !localDependencyReady(dep.Kind, fields) {
			ready = false
		}
	}
	return ready, nil
}

// missingDependency is the push refusal for a local dependency whose record
// does not exist. A card parked on a record nothing will ever write waits for
// ever, so every local kind (card, task, stream) must name an existing record
// at push time; the refusal names the record the push looked for.
func missingDependency(sprint string, dep dependency) error {
	switch dep.Kind {
	case dependencyTask:
		return fmt.Errorf("DEPENDS-ON: task:%s has no record %s in sprint %s", dep.Value, keyTask(sprint, dep.Value), sprint)
	case dependencyStream:
		return fmt.Errorf("DEPENDS-ON: stream/%s has no record %s in sprint %s", dep.Value, keyStream(sprint, dep.Value), sprint)
	}
	return fmt.Errorf("DEPENDS-ON: %s is not a card in sprint %s", dep.Value, sprint)
}

// deadTaskState names the task states task.lua treats as dead dependencies
// (dep:cancelled, dep:reconcile-required): a card parked on one waits for ever,
// so push refuses it the way it refuses a missing record.
func deadTaskState(state string) bool {
	return state == "cancelled" || state == "reconcile-required"
}

func localDependencyReady(kind dependencyKind, fields map[string]string) bool {
	switch kind {
	case dependencyCard:
		if fields["state"] == "landed" && shaRE.MatchString(fields["merge_sha"]) {
			return true
		}
		return fields["state"] == "ended" && fields["outcome"] == "DONE" && fields["pushed_sha"] == ""
	case dependencyTask:
		return fields["state"] == "closed" || fields["state"] == "done"
	case dependencyStream:
		return fields["state"] == "landed"
	}
	return false
}

func boolString(v bool) string {
	if v {
		return "1"
	}
	return "0"
}

func pushLine(sprint, label, place string) string {
	return fmt.Sprintf("CARD PUSH sprint=%s label=%s place=%s\n",
		oneline.Field(sprint), oneline.Field(label), oneline.Field(place))
}

func ensure(ctx context.Context, client *redis.Client) error {
	if client == nil {
		return fmt.Errorf("redis client is required")
	}
	return fn.LoadMissing(ctx, client)
}
