package card

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/deal"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/redis/go-redis/v9"
)

// Release moves a waiting card into the pool only when every typed dependency
// is satisfied: a card landed (or done with nothing to land), a PR merged into
// BASE, an issue closed, a stream landed, or a task done. Unresolved cards stay
// in the waiting set; the set is not copied into the pool.
func Release(ctx context.Context, client *redis.Client, sprint string) VerbResult {
	return ReleaseWith(ctx, client, sprint, deal.GH{})
}

// ReleaseWith is Release with an explicit forge seam. One invocation is one
// pass: duplicate GitHub references share one answer in that pass.
func ReleaseWith(ctx context.Context, client *redis.Client, sprint string, refs deal.PRs) VerbResult {
	if !sprintRE.MatchString(sprint) {
		return refused("sprint name must match [a-z0-9-]{1,40}")
	}
	if err := ensure(ctx, client); err != nil {
		return refused(err.Error())
	}
	// the waiting list under the current epoch (nova-tools#4238)
	epoch, err := ws.Epoch(ctx, client)
	if err != nil {
		return refused(err.Error())
	}
	ready, err := releasableCards(ctx, client, sprint, refs, epoch)
	if err != nil {
		return refused(err.Error())
	}
	args := make([]any, 1, len(ready)+1)
	args[0] = sprint
	for _, label := range ready {
		args = append(args, label)
	}
	// log: the waiting list is the epoch's, derived by the function (#4238)
	reply, err := client.FCall(ctx, "ns_card_release", []string{keyLog(sprint)}, args...).Text()

	if err != nil {
		return refused(err.Error())
	}
	var moved, waiting int
	if _, err := fmt.Sscanf(reply, "moved=%d waiting=%d", &moved, &waiting); err != nil {
		return refused(fmt.Sprintf("card release reply %q", reply))
	}
	return VerbResult{Code: exitOK, Stdout: fmt.Sprintf("CARD RELEASE sprint=%s moved=%d waiting=%d\n",
		oneline.Field(sprint), moved, waiting)}
}

type waitingCard struct {
	label string
	base  string
	state string
	deps  []dependency
}

func releasableCards(ctx context.Context, client *redis.Client, sprint string, refs deal.PRs, epoch uint64) ([]string, error) {
	labels, err := client.SMembers(ctx, ws.SprintListAt(epoch, sprint, "waiting")).Result()

	if err != nil {
		return nil, fmt.Errorf("read waiting cards: %w", err)
	}
	sort.Strings(labels)
	pipe := client.Pipeline()
	cardReads := make([]*redis.SliceCmd, len(labels))
	for i, label := range labels {
		cardReads[i] = pipe.HMGet(ctx, keyCard(sprint, label), "state", "base", "depends_on", "depends_on_typed")
	}
	if len(labels) > 0 {
		if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
			return nil, fmt.Errorf("read waiting cards: %w", err)
		}
	}
	cards := make([]waitingCard, 0, len(labels))
	localKeys := map[string]string{}
	for i, label := range labels {
		v := cardReads[i].Val()
		state, base, raw, typed := sliceString(v, 0), sliceString(v, 1), sliceString(v, 2), sliceString(v, 3)
		deps, err := parseStoredDependencies(label, raw, typed)
		if err != nil {
			return nil, fmt.Errorf("card %s: %w", label, err)
		}
		cards = append(cards, waitingCard{label: label, base: base, state: state, deps: deps})
		for _, dep := range deps {
			switch dep.Kind {
			case dependencyCard:
				localKeys[dep.Typed()] = keyCard(sprint, dep.Value)
			case dependencyTask:
				localKeys[dep.Typed()] = keyTask(sprint, dep.Value)
			case dependencyStream:
				localKeys[dep.Typed()] = keyStream(sprint, dep.Value)
			}
		}
	}

	localNames := make([]string, 0, len(localKeys))
	for name := range localKeys {
		localNames = append(localNames, name)
	}
	sort.Strings(localNames)
	pipe = client.Pipeline()
	localReads := make(map[string]*redis.MapStringStringCmd, len(localNames))
	for _, name := range localNames {
		localReads[name] = pipe.HGetAll(ctx, localKeys[name])
	}
	if len(localNames) > 0 {
		if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
			return nil, fmt.Errorf("read local dependencies: %w", err)
		}
	}

	github := &releaseRefs{refs: refs, cache: map[string]releaseRefAnswer{}}
	var ready []string
	for _, card := range cards {
		if card.state != "queued" {
			continue
		}
		ok := true
		for _, dep := range card.deps {
			if dep.Kind == dependencyGitHub {
				if !github.ready(ctx, dep.Value, card.base) {
					ok = false
					break
				}
				continue
			}
			if !localDependencyReady(dep.Kind, localReads[dep.Typed()].Val()) {
				ok = false
				break
			}
		}
		if ok {
			ready = append(ready, card.label)
		}
	}
	return ready, nil
}

func parseStoredDependencies(label, raw, typed string) ([]dependency, error) {
	if typed == "" {
		_, deps, err := parseDepends(label, raw)
		return deps, err
	}
	var deps []dependency
	for _, entry := range strings.Split(typed, ",") {
		kind, value, ok := strings.Cut(entry, ":")
		if !ok || value == "" {
			return nil, fmt.Errorf("DEPENDS-ON typed entry %q is invalid", entry)
		}
		dep := dependency{Kind: dependencyKind(kind), Value: value}
		switch dep.Kind {
		case dependencyCard, dependencyTask:
			if !idRE.MatchString(value) {
				return nil, fmt.Errorf("DEPENDS-ON typed entry %q is invalid", entry)
			}
		case dependencyStream:
			if !streamRE.MatchString(value) {
				return nil, fmt.Errorf("DEPENDS-ON typed entry %q is invalid", entry)
			}
		case dependencyGitHub:
			if githubRE.FindStringSubmatch(value) == nil {
				return nil, fmt.Errorf("DEPENDS-ON typed entry %q is invalid", entry)
			}
		default:
			return nil, fmt.Errorf("DEPENDS-ON typed entry %q has unknown type", entry)
		}
		deps = append(deps, dep)
	}
	return deps, nil
}

type releaseRefAnswer struct {
	ref deal.Ref
	err error
}

type releaseRefs struct {
	refs  deal.PRs
	cache map[string]releaseRefAnswer
}

func (r *releaseRefs) ready(ctx context.Context, name, base string) bool {
	answer, ok := r.cache[name]
	if !ok {
		m := githubRE.FindStringSubmatch(name)
		if m == nil || r.refs == nil {
			return false
		}
		n, _ := strconv.Atoi(m[3])
		answer.ref, answer.err = r.refs.Ref(ctx, m[1]+"/"+m[2], n)
		r.cache[name] = answer
	}
	if answer.err != nil {
		return false
	}
	if !answer.ref.IsPR {
		return answer.ref.State == "closed"
	}
	return answer.ref.Merged && base != "" && answer.ref.Base == base
}

func sliceString(v []any, i int) string {
	if i >= len(v) || v[i] == nil {
		return ""
	}
	s, _ := v[i].(string)
	return s
}
