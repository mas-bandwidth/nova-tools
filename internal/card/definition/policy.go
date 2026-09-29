package definition

import (
	_ "embed"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/mas-bandwidth/nova-tools/internal/hygiene"
)

// Completion is how a card of a kind completes.
type Completion string

const (
	// CompletionPR completes by a verified landing of a pull request.
	CompletionPR Completion = "pr"
	// CompletionNoPR completes by a recorded outcome, with no pull request.
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

// kindWhyIn is kindWhy against a given policy.
func kindWhyIn(p policy, kind string) (Cause, string) {
	if !hygiene.KindDeclared(kind) {
		return CauseInvalidKind, fmt.Sprintf("KIND %q is not declared; one of: %s", kind, strings.Join(hygiene.Kinds(), ", "))
	}
	if _, ok := p.class[kind]; !ok {
		return CauseUnclassifiedKind, fmt.Sprintf("KIND %q is declared but the completion policy (version %d) does not classify it", kind, p.version)
	}
	return "", ""
}
