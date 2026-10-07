// Package tablemodel checks nova-table against its TLA+ models.
//
// Three checks live here, each turning the table models into an executable check of
// the models:
//
//   - The suites (steps.go): the table model's contract, witness and control
//     configurations, and the member and epoch protocol's positive checks and
//     mutation controls, each configuration run under one TLC budget and held
//     to the result it declares.
//   - The witnesses (witnesses.go): the findings of the table model replayed as
//     concrete calls of a table.lua in a disposable Redis, each confirmed or
//     refuted by what the store holds.
//   - The replay (replay.go): controlled calls of a table.lua in a disposable
//     Redis, every store state captured and every committed receipt checked
//     against the states around it, the same receipts replayed into a second
//     fresh store, and the captured states handed to TLC as a linear harness
//     over EpochMemberTable that must match them step by step (and must refuse
//     a corrupted observation).
//
// What it runs: java for TLC and redis-server for the disposable store, each
// found on PATH or named by the caller, never downloaded. The store listens on
// a Unix socket in a private directory with TCP off, keeps nothing, and is
// killed when the check ends. Nothing here dials a fleet address or reads a
// credential.
//
// Everything that can be checked without java or a store is a pure function
// over recorded data (the reading of TLC's output is internal/tlc's; the
// rendering of a trace as a TLA+ module, the receipt-delta check and the
// planning of suites are here), and the unit tests hold them to recorded
// fixtures.
package tablemodel
