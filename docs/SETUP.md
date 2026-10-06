# Setting nova up

## When anything is wrong

### setup-nova-doctor-r-r2

Run `nova-doctor` first. It checks each dependency and prints one line per check,
`DOCTOR <check> ok|warn|fail <evidence> [fix: <line>]`; the fix is the one line to run. It
changes nothing. `nova-doctor --local` skips the checks only a fleet needs and says so;
`--check <name>` runs one; `--strict` exits 1 on a warn; the exit is 2 on a fail. The first
check, `self`, reads each nova tool's version on PATH and fails naming a tool from another
release. The contract is [SPEC-DOCTOR.md](SPEC-DOCTOR.md).
