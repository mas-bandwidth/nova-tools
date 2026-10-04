#!/bin/sh
# The live store's fleet table takes the two columns of the readers' verdicts
# (internal/sprint/TABLES.lock, the change of 2026-10-04; docs/SPEC-SPRINT.md
# section 1) before a build that has them is installed: finished (hidden) holds a
# work card its worker finished that no reader has given a verdict on, and redealt
# (shown, after ok%) counts a friend's cards taken back past their deadline.
#
# Run once, on a STOPPED machine (nova-sprint stop), against the live store, with
# nova-table pointed at it as the machine's tools are; then install the build and
# start. Each line is a table-layer write that adds an empty column or hides one; no
# card moves. TestTheMigrationGivesTheFleetTableItsLockedShape applies these lines to
# the fleet table as it stood before and checks the result is the locked shape.
set -eu
nova-table col add fleet redealt --after okpct
nova-table col add fleet finished --after withdrawn
nova-table set fleet --hide finished
