package merge

import (
	"os"
	"reflect"
	"testing"
)

func TestDefaultWiringIsPreserved(t *testing.T) {
	t.Parallel()

	// Assert (&Records{}).sleepFn() points to Sleep (using reflect.ValueOf(...).Pointer()).
	if got, want := reflect.ValueOf((&Records{}).sleepFn()).Pointer(), reflect.ValueOf(Sleep).Pointer(); got != want {
		t.Fatalf("(&Records{}).sleepFn() pointer = %v, want Sleep pointer %v", got, want)
	}

	// Assert (&stateUpdater{}).saveToWaitRefusal points to replaceRefusal.
	if got, want := reflect.ValueOf((&stateUpdater{}).saveToWaitRefusal()).Pointer(), reflect.ValueOf(replaceRefusal).Pointer(); got != want {
		t.Fatalf("(&stateUpdater{}).saveToWaitRefusal() pointer = %v, want replaceRefusal pointer %v", got, want)
	}

	// Assert default events sink in UpdateQueue / NewEnqueuer defaults to DefaultEvents.
	if got, want := defaultEvents(), DefaultEvents; got != want {
		t.Fatalf("defaultEvents() = %v, want DefaultEvents %v", got, want)
	}
	if door := NewEnqueuer(newFakeEnqueueHost()); door.Events != DefaultEvents {
		t.Fatalf("NewEnqueuer door.Events = %v, want DefaultEvents %v", door.Events, DefaultEvents)
	}

	// Assert redisAddrFromLookup defaults to os.Getenv when lookup is nil.
	if got, want := reflect.ValueOf(redisGetenv(nil)).Pointer(), reflect.ValueOf(os.Getenv).Pointer(); got != want {
		t.Fatalf("redisGetenv(nil) pointer = %v, want os.Getenv pointer %v", got, want)
	}
	if got, want := redisAddrFromLookup(nil), redisAddrFromLookup(os.Getenv); got != want {
		t.Fatalf("redisAddrFromLookup(nil) = %q, want redisAddrFromLookup(os.Getenv) = %q", got, want)
	}
}
