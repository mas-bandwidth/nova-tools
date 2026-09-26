package card

import "github.com/mas-bandwidth/nova-tools/internal/yield"

// yieldToCI is what every copy does before it execs its harness (nova-tools#4293):
// setpriority on itself to yield.Nice, so a CI leg at nice 0 on the same
// bench wins the cores without anything scheduling around it. It is one
// variable so the package's tests can keep their own test binary at its
// priority; production never assigns it.
var yieldToCI = yield.ToCI
