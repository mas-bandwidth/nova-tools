// Package tlc runs TLC over the declared cases of tla/CASES.tsv and reads what
// it says.
//
// WHAT IT OWNS. Finding the TLC jar and the java that runs it (jar.go); the
// command line of one TLC run and its bounded execution (run.go); the reading
// of TLC's exit status and output into pass or fail, the statistics and the
// violated invariant, action or temporal property (outcome.go); the case plan
// tla/CASES.tsv into the cases a run is judged by, and its refusals (plan.go);
// the plan's checks against the tree and the choice of a run's cases (cases.go);
// the run records tla/RUNS.tsv (records.go); the inputs a case reads and the
// fingerprint a record names (inputs.go, with the runner's own files in
// fingerprint.go); and the suite that runs a selection of cases under one budget
// and writes the records (suite.go).
//
// WHAT IT NEVER DOES. It downloads nothing, runs no more than two TLC workers
// per case, and never runs TLC beside the sources: every run happens in a
// private copy of the models under the output directory, because TLC writes an
// error-trace module and its binary beside the spec it was given, and a
// checkout that grew those files would no longer be the inputs a record names.
//
// WHAT A FINGERPRINT COVERS. One case's inputs and nothing else: its
// configuration, its module and the modules that one extends or instantiates,
// its own row of the plan, and the runner's result files (the ones that decide how a
// result is produced and read, and how a plan row is read into the case a run is
// judged by; see fingerprint.go). Editing one model leaves the
// records of the cases that do not read it as they are.
//
// WHY THE SOURCES ARE EMBEDDED. The runner's result files are part of every
// fingerprint (a change to how a result is read invalidates records that
// depend on that reading), and a binary built on one machine and run on a bench
// has no checkout of them. The binary therefore carries the bytes it was built
// from; internal/ci recomputes the fingerprint from the checkout's files, so a
// binary built from other files than the ones committed writes records that
// the class test refuses.
package tlc
