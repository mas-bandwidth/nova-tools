---------------------------- MODULE MCConfigApply ----------------------------
EXTENDS ConfigApply
=============================================================================
SPECIFICATION Spec
CONSTANTS
 Kinds = {"machine", "friend"}
 Names = {"m1", "m2", "f1", "f2"}
 MaxFields = 2
 MaxRev = 3
 Broken = "none"
INVARIANTS TypeOK HistoryCoversEveryWrite RedisIsACopy ConflictRefusesAhead ApplyOrder StampOnlyWhenComplete RetryIsIdempotent
PROPERTIES CrashedApplyCompletes
