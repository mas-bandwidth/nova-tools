import re

with open('internal/nsprint/task/take_batch_test.go', 'r') as f:
    batch_test = f.read()

# Fix TestTakeAvailableThreeQueuesFiveClaimsOnePipelineOneFCall
batch_test = re.sub(
    r'if len\(rec\.pipelines\) != 1 \|\| len\(rec\.singles\) != 1 \|\| rec\.singles\[0\] != "fcall ns_task_take_n" \{\n\t\tt.Fatalf\("round trips: pipelines=%v singles=%v; want one pipeline and one FCALL ns_task_take_n", rec\.pipelines, rec\.singles\)\n\t\}',
    'if rec.trips() != 1 || len(rec.singles) != 1 || rec.singles[0] != "fcall ns_task_take_n" {\n\t\tt.Fatalf("round trips: pipelines=%v singles=%v; want exactly one FCALL ns_task_take_n", rec.pipelines, rec.singles)\n\t}',
    batch_test
)
batch_test = re.sub(
    r'if p := strings.Join\(rec\.pipelines\[0\], ","\); !strings.Contains\(p, "fcall_ro ns_task_take_view"\) \{\n\t\tt.Fatalf\("pipeline %s does not carry the view", p\)\n\t\}',
    '',
    batch_test
)

# Fix TestTakeAvailableOneSprintEightSlotsTwoTrips
batch_test = re.sub(
    r'if len\(claims\) != 8 \|\| rec\.trips\(\) != 2 \|\| len\(rec\.pipelines\) != 1 \{\n\t\tt.Fatalf\("claims=%d pipelines=%v singles=%v; want 8 claims in one pipeline and one FCALL", len\(claims\), rec\.pipelines, rec\.singles\)\n\t\}',
    'if len(claims) != 8 || rec.trips() != 1 || len(rec.singles) != 1 {\n\t\tt.Fatalf("claims=%d pipelines=%v singles=%v; want 8 claims in exactly one FCALL", len(claims), rec.pipelines, rec.singles)\n\t}',
    batch_test
)

# Fix TestTakeAvailableIDBlockedStrict (trips == 1 instead of 2)
batch_test = re.sub(
    r'if rec\.trips\(\) != 2 \{\n\t\tt.Fatalf\("trips=%d, want 2", rec\.trips\(\)\)\n\t\}',
    'if rec.trips() != 1 {\n\t\tt.Fatalf("trips=%d, want 1", rec.trips())\n\t}',
    batch_test
)

with open('internal/nsprint/task/take_batch_test.go', 'w') as f:
    f.write(batch_test)
