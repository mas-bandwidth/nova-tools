package main

import "github.com/mas-bandwidth/nova-tools/internal/pulse"

// statusHTMLReader is the fleet reader `status --html` folds. Production leaves it nil and
// internal/pulse reads the benches over ssh (ps/pgrep liveness, never log age); a test
// replaces it with a fake that returns per-bench process counts and disk/mem, so no test
// starts ssh or reaches a machine.
var statusHTMLReader pulse.FleetReader
