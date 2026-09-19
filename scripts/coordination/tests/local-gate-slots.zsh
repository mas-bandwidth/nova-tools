#!/bin/zsh
# Isolated test of local-gate.sh's slot functions, as they are in the file.
. "$HOME/rowan-working/bin/safe-rm.sh"; die() { print -- "DIE: $*"; exit 9 }
eval "$(sed -n '/^slot_release() {/p;/^slot_acquire() {/,/^}/p' $HOME/rowan-working/bin/local-gate.sh)"
GATES_DIR=$1; SLOTS=2; SLOT_DIR=$GATES_DIR/slots; SLOT=''; mkdir -p $SLOT_DIR
slot_acquire; print "acquired: ${SLOT:t}"; slot_release; print "after release, slots left: $(ls $SLOT_DIR | wc -l | tr -d ' ')"
mkdir -p $SLOT_DIR/1 $SLOT_DIR/2; print 999999 > $SLOT_DIR/1/pid; print 999998 > $SLOT_DIR/2/pid   # two stale slots, dead pids
( slot_acquire; print "acquired over stale slots: ${SLOT:t}" ) & w=$!; sleep 12; if kill -0 $w 2>/dev/null; then print "STUCK: still waiting after 12 s with only dead slots"; kill $w; else wait $w; fi
