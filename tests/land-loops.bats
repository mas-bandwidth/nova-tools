#!/usr/bin/env bats

@test "fleet/loops.tsv has land stream row" {
    run grep "nova-sprint land stream" fleet/loops.tsv
    [ "$status" -eq 0 ]
}

@test "fleet/loops.tsv has land merge row" {
    run grep "nova-sprint land merge" fleet/loops.tsv
    [ "$status" -eq 0 ]
}
