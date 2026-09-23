package docs

import (
	"os"
	"strings"
	"testing"
)

// issue2235_test.go proves that docs/SPEC-FLEET-KUBE.md carries the five
// behaviours nova-tools#2235 asks for (items 22-26 of the "Tests this spec
// demands" table):
//
//   22. TestNodeLabelsAndNodeSelectorPerKind
//   23. TestWarmCachePreferredAffinityAndLocalHardPin
//   24. TestPersistentVolumesPerBench
//   25. TestWorkQueueLayoutOnSharedVolume
//   26. a-worker-that-dies-mid-card-returns-the-card-to-the-queue
//
// It reads the spec as text and runs nothing.

// specFleetKubePath is the document under test, relative to this package.
const specFleetKubePath = "../../docs/SPEC-FLEET-KUBE.md"

// TestIssue2235 is the anchor test for nova-tools#2235. It reads
// docs/SPEC-FLEET-KUBE.md and proves the five behaviours the issue names
// are present in the spec: node labels and nodeSelector per kind, warm-cache
// preferred affinity and local hard pin, PersistentVolumes per bench, the work
// queue layout on a shared volume, and the dead-card return.
func TestIssue2235(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile(specFleetKubePath)
	if err != nil {
		t.Fatalf("%s: %v; this spec is not optional", specFleetKubePath, err)
	}
	body := string(data)

	// 22. TestNodeLabelsAndNodeSelectorPerKind — each bench labels its node
	// with nova.mas-bandwidth.com/bench=<name> and one or more
	// nova.mas-bandwidth.com/kind=go|lisp|docs|schema-leg, and a Job carries
	// the kind of its card and selects it with nodeSelector.
	if !strings.Contains(body, "nova.mas-bandwidth.com/bench=") {
		t.Errorf("%s: missing `nova.mas-bandwidth.com/bench=`; each bench must label its node with this label so a Job can select the right bench — item 22 of the Tests this spec demands table", specFleetKubePath)
	}
	if !strings.Contains(body, "nova.mas-bandwidth.com/kind=") {
		t.Errorf("%s: missing `nova.mas-bandwidth.com/kind=`; each bench must label its node with the kinds it runs (go, lisp, docs, schema-leg) so a Job with nodeSelector is only placed where its tools are warm — item 22", specFleetKubePath)
	}
	if !strings.Contains(body, "nodeSelector") {
		t.Errorf("%s: missing `nodeSelector`; a Job must carry the kind of its card and select it with nodeSelector so a lisp card is only ever placed where SBCL is warm — item 22", specFleetKubePath)
	}

	// 23. TestWarmCachePreferredAffinityAndLocalHardPin — warm caches are
	// preferredDuringSchedulingIgnoredDuringExecution affinity to the
	// repo/cache label, and the local volume pins placement hard.
	if !strings.Contains(body, "preferredDuringSchedulingIgnoredDuringExecution") {
		t.Errorf("%s: missing `preferredDuringSchedulingIgnoredDuringExecution`; warm caches must be a preferred affinity so the scheduler favours a node with the repo/cache already warm but can fall back — item 23", specFleetKubePath)
	}
	if !strings.Contains(body, "local") || !strings.Contains(body, "pins placement hard") {
		t.Errorf("%s: missing the `local` volume hard pin; when a card needs a specific checkout the local volume must pin placement hard — item 23", specFleetKubePath)
	}

	// 24. TestPersistentVolumesPerBench — one local PersistentVolume for the
	// mirror (ReadOnlyMany, WaitForFirstConsumer) and one for the shared Go
	// cache (ReadWriteOnce), both under $HOME/nova-bench.
	if !strings.Contains(body, "PersistentVolume") {
		t.Errorf("%s: missing `PersistentVolume`; each bench must have PersistentVolumes for the mirror and the shared Go cache so they survive a Job — item 24", specFleetKubePath)
	}
	if !strings.Contains(body, "ReadOnlyMany") {
		t.Errorf("%s: missing `ReadOnlyMany`; the mirror PV must be ReadOnlyMany so multiple Jobs can read the same fetch-only mirror — item 24", specFleetKubePath)
	}
	if !strings.Contains(body, "ReadWriteOnce") {
		t.Errorf("%s: missing `ReadWriteOnce`; the Go cache PV must be ReadWriteOnce for the shared cache and kept worktrees — item 24", specFleetKubePath)
	}
	if !strings.Contains(body, "WaitForFirstConsumer") {
		t.Errorf("%s: missing `WaitForFirstConsumer`; the mirror PV's volumeBindingMode must be WaitForFirstConsumer so it binds to the node the pod lands on — item 24", specFleetKubePath)
	}
	if !strings.Contains(body, "$HOME/nova-bench") {
		t.Errorf("%s: missing `$HOME/nova-bench`; the PVs must live under the bench's $HOME/nova-bench as the shared cache and mirror root — item 24", specFleetKubePath)
	}

	// 25. TestWorkQueueLayoutOnSharedVolume — queue/lanes/{red,green,small,next}/
	// and taken/, the exact layout nova-pulse cut writes, with the atomic take.
	if !strings.Contains(body, "queue/lanes/") {
		t.Errorf("%s: missing `queue/lanes/`; the work queue must have the queue/lanes/ directory structure — item 25", specFleetKubePath)
	}
	if !strings.Contains(body, "taken/") {
		t.Errorf("%s: missing `taken/`; the work queue must have the taken/ directory for cards on lease — item 25", specFleetKubePath)
	}
	if !strings.Contains(body, "red") || !strings.Contains(body, "green") || !strings.Contains(body, "small") || !strings.Contains(body, "next") {
		t.Errorf("%s: missing the lane names (red, green, small, next); the work queue layout must be queue/lanes/{red,green,small,next}/ — item 25", specFleetKubePath)
	}

	// 26. a-worker-that-dies-mid-card-returns-the-card-to-the-queue — a Job
	// killed before its clip is observed by the puller, and the card is back
	// in its lane once and re-runnable.
	if !strings.Contains(body, "a-worker-that-dies-mid-card-returns-the-card-to-the-queue") {
		t.Errorf("%s: missing `a-worker-that-dies-mid-card-returns-the-card-to-the-queue`; a card whose Job dies must be returned by the puller to the lane it came from — item 26", specFleetKubePath)
	}
	if !strings.Contains(body, "A card whose Job dies is returned by the puller to the lane it came from") {
		t.Errorf("%s: missing the sentence that says a card whose Job dies is returned by the puller to the lane it came from; this is the behaviour item 26 proves — without it the spec does not name the recovery", specFleetKubePath)
	}

	// Verify the Tests this spec demands section lists items 22-26.
	if !strings.Contains(body, "TestNodeLabelsAndNodeSelectorPerKind") {
		t.Errorf("%s: the Tests this spec demands section does not list `TestNodeLabelsAndNodeSelectorPerKind` (item 22); the table must enumerate the test so a reader can verify it exists", specFleetKubePath)
	}
	if !strings.Contains(body, "TestWarmCachePreferredAffinityAndLocalHardPin") {
		t.Errorf("%s: the Tests this spec demands section does not list `TestWarmCachePreferredAffinityAndLocalHardPin` (item 23)", specFleetKubePath)
	}
	if !strings.Contains(body, "TestPersistentVolumesPerBench") {
		t.Errorf("%s: the Tests this spec demands section does not list `TestPersistentVolumesPerBench` (item 24)", specFleetKubePath)
	}
	if !strings.Contains(body, "TestWorkQueueLayoutOnSharedVolume") {
		t.Errorf("%s: the Tests this spec demands section does not list `TestWorkQueueLayoutOnSharedVolume` (item 25)", specFleetKubePath)
	}
}
