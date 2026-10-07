# BRIEF: audit-nova-tools-opencode-b.w3

## TASK
Audit the opencode harness usage source implementation in internal/swarm/opencode.go

## SCOPE
Review the implementation of the opencode database reader that:
- Reads usage data from opencode.db via sqlite3
- Handles WAL checkpoint waiting (usageSettleWait)
- Binds query timeouts correctly
- Sums token counts across message rows

## EXPECTED CHECKS
- TestNoSQLiteOnPathIsAnError
- TestOpenCodeSourceWithoutSQLiteIsANamedRefusal
- TestTheNoneSourceReportsNothing
- TestASlowFirstReadDoesNotSpendTheSettleWindow
- TestAWriteAheadLogThatOutlastsTheWindowIsStillARefusal
- TestEveryRetryIsBoundedByWhatIsLeftOfTheWindow
- TestTheOpenCodeSourceSumsTheMessageRows (functional)
- TestAnAbsentOpenCodeDatabaseIsNotAnError (functional)
- TestAnUnreadableOpenCodeDatabaseIsAnError (functional)
- TestOpenCodeSourceFindsTheLocalShareStore (functional)
- TestOpenCodeSourceWaitsOutAWriteAheadLog (functional)

## RESULT
Report LAND if all tests pass, HOLD if blocked, FAIL if defects found.
