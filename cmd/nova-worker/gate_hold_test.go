package main

import ()

// THE HOLD ON #1478 (comment 5737662335), P1 AND P2. Both are real, and both are about
// the same mistake: the first version read the shell's line as if it were a sentence about a
// program, when it is a sentence about a PATH and says nothing about what was done to it.
//
// P1: the grammar rejected any path holding a space, so `/opt/sdk tool/bin/go` -- an ordinary
// absolute path -- recreated the exact silent green of #1465 through the tool's own fixture.
//
// P2: a plain redirection to an unwritable path produces the same words, and the card
// RECOVERS from it and exits 0. The line cannot tell an exec denial from a write denial, and
// under --no-wall there is no wall to attribute either to.
