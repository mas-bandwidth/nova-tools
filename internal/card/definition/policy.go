package definition

import (
	_ "embed"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/mas-bandwidth/nova-tools/internal/card"
	"github.com/mas-bandwidth/nova-tools/internal/hygiene"
)

// Completion is how a card of a kind completes.
type Completion string

const (
	// CompletionPR ends by a landing: its code reaches the development branch.
	CompletionPR Completion = "pr"
	// CompletionNoPR has no code to land. Where such a card ends is an open
	// question: the lifecycle names no state for it.
	CompletionNoPR Completion = "non-pr"
)

//go:embed completion.txt
var policyData string

// policy is one parsed completion policy.
type policy struct {
	version int
	class   map[string]Completion
	order   []string
}

var (
	policyOnce sync.Once
	policyVal  policy
	policyErr  error
)

func loadPolicy() (policy, error) {
	policyOnce.Do(func() { policyVal, policyErr = parsePolicy(policyData) })
	return policyVal, policyErr
}

// parsePolicy reads a completion policy file. It refuses a missing or repeated
// version, a kind classified twice, and a class that is not pr or non-pr.
func parsePolicy(data string) (policy, error) {
	p := policy{class: map[string]Completion{}}
	for n, line := range strings.Split(data, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) != 2 {
			return policy{}, fmt.Errorf("completion.txt:%d: want <kind>\\t<pr|non-pr>", n+1)
		}
		name, val := strings.TrimSpace(f[0]), strings.TrimSpace(f[1])
		if name == "version" {
			v, err := strconv.Atoi(val)
			if err != nil || v < 1 || p.version != 0 {
				return policy{}, fmt.Errorf("completion.txt:%d: version must be one positive integer, once", n+1)
			}
			p.version = v
			continue
		}
		c := Completion(val)
		if c != CompletionPR && c != CompletionNoPR {
			return policy{}, fmt.Errorf("completion.txt:%d: %s is classified %q, want pr or non-pr", n+1, name, val)
		}
		if _, dup := p.class[name]; dup {
			return policy{}, fmt.Errorf("completion.txt:%d: %s is classified twice", n+1, name)
		}
		p.class[name] = c
		p.order = append(p.order, name)
	}
	if p.version == 0 {
		return policy{}, fmt.Errorf("completion.txt: no version row")
	}
	return p, nil
}

// PolicyDigest is the SHA-256 of the embedded completion policy file, in lower-case
// hexadecimal: the identity of the policy's content, which the version names.
func PolicyDigest() string { return string(card.Sum([]byte(policyData))) }

// PolicyVersion is the version of the embedded completion policy, 0 when the
// embedded file cannot be read.
func PolicyVersion() int {
	p, err := loadPolicy()
	if err != nil {
		return 0
	}
	return p.version
}

// PolicyKinds is every kind the policy classifies, sorted.
func PolicyKinds() []string {
	p, err := loadPolicy()
	if err != nil {
		return nil
	}
	out := append([]string(nil), p.order...)
	sort.Strings(out)
	return out
}

// Classify answers the completion class of every kind in the array, in order.
// A kind the policy does not classify answers "" and false. Kinds are looked up
// as written: there is no case folding and no default class.
func Classify(kinds []string) ([]Completion, []bool) {
	cs := make([]Completion, len(kinds))
	oks := make([]bool, len(kinds))
	p, err := loadPolicy()
	if err != nil {
		return cs, oks
	}
	for i, k := range kinds {
		cs[i], oks[i] = p.class[k]
	}
	return cs, oks
}

// unclassified is the declared kinds the policy does not classify, and the
// classified kinds that are no longer declared, each sorted. Both are drift.
func unclassified(declared []string, p policy) (missing, stale []string) {
	isDeclared := map[string]bool{}
	for _, k := range declared {
		isDeclared[k] = true
		if _, ok := p.class[k]; !ok {
			missing = append(missing, k)
		}
	}
	for k := range p.class {
		if !isDeclared[k] {
			stale = append(stale, k)
		}
	}
	sort.Strings(missing)
	sort.Strings(stale)
	return missing, stale
}

// kindProblemIn is kindProblem against a given policy.
func kindProblemIn(p policy, kind string) *problem {
	if !hygiene.KindDeclared(kind) {
		return bad(CauseInvalidKind, card.Value(kind), "KIND is one of internal/hygiene/kinds.txt: "+strings.Join(hygiene.Kinds(), ", "), "use a kind internal/hygiene/kinds.txt declares")
	}
	if _, ok := p.class[kind]; !ok {
		return bad(CauseUnclassifiedKind, card.Value(kind), fmt.Sprintf("the completion policy (version %d) classifies KIND", p.version), "classify the kind in internal/card/definition/completion.txt")
	}
	return nil
}
