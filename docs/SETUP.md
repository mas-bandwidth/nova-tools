# Setting up

## Checking the setup

### setup-nova-doctor-r-r4.w1: nova-doctor

When anything is wrong, run `nova-doctor` first. It runs every check, prints one
`DOCTOR <check> ok|warn|fail <evidence> [fix: <line>]` line each, and gives the one fix
line for each thing that is not ok. It changes nothing.

```
nova-doctor            every check
nova-doctor --local    skip the checks only a fleet needs, and say which
nova-doctor --check self --json
```

Exit 0 is all ok, 1 a warn under `--strict`, 2 a fail. The first check, `self`, finds the
nova tools on PATH and fails when they are not one release, naming the odd one. The
contract is [SPEC-DOCTOR.md](SPEC-DOCTOR.md).
