package main

import ()

// ONE SLOT LEDGER (nova-tools#3877). A bench's capacity is bench:<b>:desired in Redis and
// the dealer is the one place a card is admitted or refused against it; `native` takes no
// file lease of its own. These tests pin the two halves of that on the native side: a
// launch with no slot store runs, and a launch still handed the retired --slots-store and
// --owner (a caller built before #3877) runs against a store whose share is full, the
// shape that refused seven dealt cards on batman, and writes no lease into it.
