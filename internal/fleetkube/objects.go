// Package fleetkube models the Kubernetes side of docs/SPEC-FLEET-KUBE.md
// Part 2 as plain values: node labels, the Job a card becomes, the two local
// PersistentVolumes per bench, and the directory work queue with its atomic
// take and died-card return. It talks to no cluster; the objects it builds
// are what a bench's k3s is given, and the tests run them against a fake
// scheduler and a queue in a temp dir.
//
// #2235 is the first slice (spec behaviours 22-26); the recuts of #2232 and
// #2226 stack on this package.
package fleetkube

import (
	"fmt"
	"path/filepath"
)

// Label keys a bench puts on its single k3s node.
const (
	LabelPrefix = "nova.mas-bandwidth.com/"
	LabelBench  = LabelPrefix + "bench"
)

// Worker kinds: the only values a kind label may name.
const (
	KindGo        = "go"
	KindLisp      = "lisp"
	KindDocs      = "docs"
	KindSchemaLeg = "schema-leg"
)

var kinds = map[string]bool{KindGo: true, KindLisp: true, KindDocs: true, KindSchemaLeg: true}

// KindLabel is the node label that says a bench is warm for kind k. A node
// may carry several, so each kind is its own key with value "true".
func KindLabel(k string) string { return LabelPrefix + "kind-" + k }

// CacheLabel is the node label that says a bench holds a warm cache of repo.
func CacheLabel(repo string) string { return LabelPrefix + "cache-" + repo }

// Node is one bench's single k3s node.
type Node struct {
	Name   string
	Labels map[string]string
}

// NewNode labels a bench's node with bench=<name>, one kind label per kind
// (at least one, each go|lisp|docs|schema-leg) and one cache label per warm repo.
func NewNode(bench string, nodeKinds, warmRepos []string) (Node, error) {
	if bench == "" {
		return Node{}, fmt.Errorf("fleetkube: node has no bench name")
	}
	if len(nodeKinds) == 0 {
		return Node{}, fmt.Errorf("fleetkube: node %s has no kind label", bench)
	}
	l := map[string]string{LabelBench: bench}
	for _, k := range nodeKinds {
		if !kinds[k] {
			return Node{}, fmt.Errorf("fleetkube: node %s: unknown kind %q", bench, k)
		}
		l[KindLabel(k)] = "true"
	}
	for _, r := range warmRepos {
		l[CacheLabel(r)] = "true"
	}
	return Node{Name: bench, Labels: l}, nil
}

// Card is the part of a card the Job is built from.
type Card struct {
	Name     string
	Kind     string
	Repo     string // warm cache wanted (soft)
	Checkout string // bench whose local volume holds the checkout (hard); "" if none
}

// Term is one label requirement key=value.
type Term struct{ Key, Value string }

// Affinity carries preferredDuringSchedulingIgnoredDuringExecution terms
// (Preferred) and requiredDuringSchedulingIgnoredDuringExecution terms (Required).
type Affinity struct {
	Preferred []Term
	Required  []Term
}

// Job is the Kubernetes Job one card becomes.
type Job struct {
	Name         string
	Kind         string
	NodeSelector map[string]string
	Affinity     Affinity
	Volume       string // the local PV a pinned card mounts; "" if none
}

// JobFor builds the Job for a card: nodeSelector on its kind, a preferred
// affinity to its repo's warm cache, and when the card needs a specific
// checkout, a hard pin to that bench plus its local cache volume.
func JobFor(c Card) Job {
	j := Job{
		Name:         "card-" + c.Name,
		Kind:         c.Kind,
		NodeSelector: map[string]string{KindLabel(c.Kind): "true"},
	}
	if c.Repo != "" {
		j.Affinity.Preferred = append(j.Affinity.Preferred, Term{CacheLabel(c.Repo), "true"})
	}
	if c.Checkout != "" {
		j.Affinity.Required = append(j.Affinity.Required, Term{LabelBench, c.Checkout})
		j.Volume = VolumeCache
	}
	return j
}

// Schedule is a fake scheduler with the semantics the Job relies on: a node
// must match every nodeSelector entry and every required term; among those,
// the one matching the most preferred terms wins, ties by input order.
func Schedule(nodes []Node, j Job) (Node, bool) {
	best, bestScore := -1, -1
	for i, n := range nodes {
		ok := true
		for k, v := range j.NodeSelector {
			ok = ok && n.Labels[k] == v
		}
		for _, t := range j.Affinity.Required {
			ok = ok && n.Labels[t.Key] == t.Value
		}
		if !ok {
			continue
		}
		score := 0
		for _, t := range j.Affinity.Preferred {
			if n.Labels[t.Key] == t.Value {
				score++
			}
		}
		if score > bestScore {
			best, bestScore = i, score
		}
	}
	if best < 0 {
		return Node{}, false
	}
	return nodes[best], true
}

// The two local volumes every bench has.
const (
	VolumeMirror = "mirror"
	VolumeCache  = "cache"
)

// PersistentVolume is one local PV on a bench.
type PersistentVolume struct {
	Volume       string
	Type         string // "local"
	LocalPath    string
	AccessMode   string
	BindingMode  string
	NodeAffinity map[string]string
}

// PersistentVolumesFor returns the bench's two local PVs under
// $HOME/nova-bench: the fetch-only mirror (ReadOnlyMany) and the Go cache and
// kept worktrees (ReadWriteOnce), both WaitForFirstConsumer and pinned to the
// bench, so a fresh pod on the node resumes with the same cache and mirror.
func PersistentVolumesFor(bench, home string) []PersistentVolume {
	base := filepath.Join(home, "nova-bench")
	pin := map[string]string{LabelBench: bench}
	return []PersistentVolume{
		{Volume: VolumeMirror, Type: "local", LocalPath: filepath.Join(base, "mirror"), AccessMode: "ReadOnlyMany", BindingMode: "WaitForFirstConsumer", NodeAffinity: pin},
		{Volume: VolumeCache, Type: "local", LocalPath: filepath.Join(base, "cache"), AccessMode: "ReadWriteOnce", BindingMode: "WaitForFirstConsumer", NodeAffinity: pin},
	}
}
