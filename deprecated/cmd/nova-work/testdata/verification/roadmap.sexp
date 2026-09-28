; Fixture for cmd/nova-work/verification_test.go (nova-tools#3459): the shape of
; docs/roadmaps/nova-work.sexp's :verification block, two features, five rows.
(  :schema "nova-work-roadmap-baseline-1"
  :verification (    :measured-at "2026-09-19"
    :revision "4c793b55a30160e5fe1ed45e25928f2c85dcfe5c"
    :branch "dev"
    :suite "cd lisp/nova-work && ./run-tests.sh"
    :suite-result "NOVA-WORK SLICE1 total=336 pass=336 fail=0"
    :verified-features 0
    :by-feature (
      (:feature "E01-F01" :verified 2 :total 3 :tests "tests/acceptance/slice-01-reader.lisp: reader-a, reader-b; reader-c"
       :criteria ((:id "E01-F01-01" :state "verified" :text "one")
                  (:id "E01-F01-02" :state "verified" :text "two")
                  (:id "E01-F01-03" :state "unverified" :text "three" :tests "newly-green")))
      (:feature "E02-F01" :verified 1 :total 2 :tests "journal-a; internal/ghcapture/adapter_test.go: TestOnlyGo"
       :criteria ((:id "E02-F01-01" :state "verified" :text "four")
                  (:id "E02-F01-02" :state "unverified" :text "five" :tests "still-red-or-absent"))))))
