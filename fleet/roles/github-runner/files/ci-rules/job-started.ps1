# ACTIONS_RUNNER_HOOK_JOB_STARTED: the CI rules (fleet/README.md, "CI rules"); put here by nova-fleet, edits are overwritten.
& (Join-Path $PSScriptRoot 'nova-ci-rules.ps1') started
exit $LASTEXITCODE
