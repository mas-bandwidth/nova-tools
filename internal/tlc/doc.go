// Package tlc runs TLC over the declared cases of tla/CASES.tsv and reads what
// it says.
//
// WHAT IT OWNS. Finding the TLC jar and the java that runs it (jar.go); the
// command line of one TLC run and its bounded execution (run.go); the reading
// of TLC's exit status and output into pass or fail, the statistics and the
// violated invariant, action or temporal property (outcome.go); the case plan
// tla/CASES.tsv and its refusals (cases.go); the run records tla/RUNS.tsv
// (records.go); the fingerprint of the inputs a record was measured on
// (fingerprint.go); and the suite that runs a selection of cases under one
// budget and writes the records (suite.go).
//
// WHAT IT NEVER DOES. It downloads nothing, runs no more than two TLC workers
// per case, and never runs TLC beside the sources: every run happens in a
// private copy of the models under the output directory, because TLC writes an
// error-trace module and its binary beside the spec it was given, and a
// checkout that grew those files would no longer be the inputs a record names.
//
// WHY THE SOURCES ARE EMBEDDED. The runner's own files are part of the
// fingerprint (a change to how a result is read invalidates the records taken
// with the old reading), and a binary built on one machine and run on a bench
// has no checkout of them. The binary therefore carries the bytes it was built
// from; internal/ci recomputes the fingerprint from the checkout's files, so a
// binary built from other files than the ones committed writes records that
// the class test refuses.
package tlc
