package main

import (
	"reflect"
	"testing"
)

func TestUsageVerbsIgnoresWrappedExplanation(t *testing.T) {
	banner := "usage:\n" +
		"  nova-swarm mirror --repos <a,b>\n" +
		"                        nova-swarm mirror as a nova-config loop row.\n" +
		"  nova-swarm NOTE: mirror needs credentials.\n" +
		"  nova-swarm help [<verb>]\n"
	if got, want := usageVerbs(banner, "nova-swarm"), []string{"mirror"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("usageVerbs() = %q, want %q", got, want)
	}
}

func TestBannerReferenceLeavesExamplesToHandWrittenDocs(t *testing.T) {
	banner := "usage:\n  nova-up version\n\nexit codes: 0 done\n\nexample:\n  nova-up version\n"
	want := "usage:\n  nova-up version\n\nexit codes: 0 done"
	if got := bannerReference(banner); got != want {
		t.Fatalf("bannerReference() = %q, want %q", got, want)
	}
}

func TestBannerReferenceLeavesInboxTranscriptToHandWrittenDocs(t *testing.T) {
	banner := "usage:\n  nova-sprint inbox\n\nreading the inbox and answering a judgment:\n\n  $ nova-sprint inbox\n"
	if got, want := bannerReference(banner), "usage:\n  nova-sprint inbox"; got != want {
		t.Fatalf("bannerReference() = %q, want %q", got, want)
	}
}
