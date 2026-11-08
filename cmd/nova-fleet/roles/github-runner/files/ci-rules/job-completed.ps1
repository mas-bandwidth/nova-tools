# ACTIONS_RUNNER_HOOK_JOB_COMPLETED: the CI rules (fleet/README.md, "CI rules"); put here by nova-fleet, edits are overwritten.
& (Join-Path $PSScriptRoot 'nova-ci-rules.ps1') completed
exit $LASTEXITCODE
