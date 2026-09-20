RESULT tools22-pre-2149-r1 sha=5298f6be12ea — a pre-read of mas-bandwidth/nova-tools#2149 at head 410b28681418: tests: standing probe for planted symlink and FIFO at RESULT.md and INDEX (#233)
PREREAD 2149 claims=10 proven=10 unproven=0 defects=0 high=0
PR 2149
HEAD 410b2868141857b1c3abb55248d871b81d1dded3
BASE dev
MERGE-BASE a7611c8189d33979923064f4d10eb07fb9957730
BEHIND 1
FILES 0 production, 5 test
LINES +424 -0

1. native's result lookup must not follow a symlink planted at RESULT.md and treat it as published
2. native must not block on a FIFO planted at RESULT.md
3. native's harnessState must not follow a symlink planted at RESULT.md
4. native's harnessState must not block on a FIFO planted at RESULT.md
5. harvest must not follow a symlink planted at RESULT.md and score it as done
6. harvest --working must not follow a symlink planted at RESULT.md
7. harvest must not block on a FIFO planted at RESULT.md
8. harvest --working must not block on a FIFO planted at RESULT.md
9. ReadLaneIndex must refuse a symlink planted at a lane's INDEX
10. ReadLaneIndex must not block on a FIFO planted at a lane's INDEX

PROVEN-BY cmd/nova-swarm/planted_result_test.go:42 TestNativeDoesNotTreatAPlantedSymlinkAsAPublishedResult — asserts native run harness is not "ok" and file outside job was not rewritten
PROVEN-BY cmd/nova-swarm/planted_result_unix_test.go:39 TestNativeDoesNotBlockOnAPlantedFIFOAtResult — asserts native run completes within bound and harness is not "ok"
PROVEN-BY cmd/nova-swarm/planted_result_test.go:65 TestNativeHarnessStateDoesNotFollowAPlantedSymlinkAtResult — asserts harnessState does not return "ok"
PROVEN-BY cmd/nova-swarm/planted_result_unix_test.go:67 TestNativeHarnessStateDoesNotBlockOnAPlantedFIFO — asserts harnessState returns within bound and is not "ok"
PROVEN-BY internal/pulse/planted_result_test.go:32 TestHarvestDoesNotTreatAPlantedSymlinkAsADoneResult — asserts harvest output contains neither "done=1" nor planted branch name
PROVEN-BY internal/pulse/planted_result_test.go:75 TestHarvestWorkingDoesNotFollowAPlantedSymlinkAtResult — asserts harvest --working output does not contain planted branch name
PROVEN-BY internal/pulse/planted_result_unix_test.go:33 TestHarvestDoesNotBlockOnAPlantedFIFOAtResult — asserts harvest completes within bound
PROVEN-BY internal/pulse/planted_result_unix_test.go:56 TestHarvestWorkingDoesNotBlockOnAPlantedFIFOAtResult — asserts harvest --working completes within bound
PROVEN-BY internal/bus/planted_index_sequence_unix_test.go:18 TestFriendSequencePlantedIndexIsRefused — asserts ReadLaneIndex returns error containing "symlink"
PROVEN-BY internal/bus/planted_index_sequence_unix_test.go:44 TestFriendSequencePlantedIndexIsRefused — asserts ReadLaneIndex returns error containing "fifo" within bound

DEFECTS none

QUESTIONS FOR THE REVIEWER
1. The planted symlink tests verify the file outside the job was not rewritten — is this behavior tested elsewhere in the codebase or is it solely the purpose of this test?
2. The INDEX test uses `victimHolding` and `plant` helper functions — are these defined in other test files that were not changed in this PR?
3. Is there a reason the symlink and FIFO tests for INDEX are combined in one test function while RESULT.md tests are split across two files?

Left owed
- Did not read existing test files in internal/bus/ to find victimHolding/plant helpers
- Did not read existing test files in cmd/nova-swarm/ or internal/pulse/ to understand nativeHarness/harnessState implementation

git status --short
git rev-parse HEAD 410b2868141857b1c3abb55248d871b81d1dded3
