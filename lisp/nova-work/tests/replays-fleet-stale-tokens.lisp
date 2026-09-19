;;;; replays-fleet-stale-tokens.lisp --- the fleet's stale-token refusals.
;;;;
;;;; SPEC-WORK.md:3646-3687 and :6147-6149. `take`, `heartbeat` and `release`
;;;; are verbs over one allocator per machine, and each validates the tokens it
;;;; is handed: the machine's CONFIG generation, the allocation's own generation
;;;; and whether the allocation is live at all. A verb that grants work on a
;;;; stale token is a verb that hands a slot to a holder the fleet has moved on
;;;; from.
;;;;
;;;; MEASURED BY MUTATION against dev@11aa07a7, one edit each, 335-case suite,
;;;; fresh isolated ASDF cache per run. Every one of these left it 335/335 GREEN:
;;;;
;;;;   fleet.lisp:1224  take's STALE MACHINE GENERATION refusal   -> deletable
;;;;   fleet.lisp:1276  heartbeat on a dead/unknown allocation    -> deletable
;;;;   fleet.lisp:1304  release   on a dead/unknown allocation    -> deletable
;;;;   fleet.lisp:1308  release's generation validation           -> deletable
;;;;   fleet.lisp:290   fence-lease bumping the lease generation  -> deletable
;;;;
;;;; The suspect/expiry half IS covered, by `expiry-marks-suspect-reuse-needs-
;;;; fencing` (tests/replays-8662.lisp) -- its mutations go red. What had no
;;;; witness is the token check that runs BEFORE any deadline is consulted.
;;;;
;;;; Nothing is fixed here. These are the cases the refusals never had.

(in-package #:nova-work/tests)

(deftest "the-fleet-verbs-refuse-a-stale-token-before-any-deadline"
    "docs/SPEC-WORK.md:3646-3687,6147-6149"
    "expected=take-refuses-a-stale-machine-generation;heartbeat-and-release-refuse-a-dead-allocation;release-refuses-a-stale-allocation-generation;the-slot-is-not-moved"
  ;; 1. `take` under a STALE MACHINE GENERATION grants nothing. The machine is
  ;; registered at generation 4; a take that still believes in 3 is holding a
  ;; token the fleet has re-issued.
  (multiple-value-bind (reg ok) (fleet-machine :concurrent 2 :generation 4)
    (ok ok "machine m-a1 is registered")
    (multiple-value-bind (took line code)
        (fleet-take reg :machine "m-a1" :node "N1" :slots 1 :generation 3
                    :request "stale-gen-1" :holder "rowan" :allocation-id "alloc-stale"
                    :now 100)
      (check-equal nil took "a take under a stale machine generation was admitted")
      (check-equal 1 code "the stale-token refusal exits 1")
      (ok (search "stale token" line) "the refusal does not name the stale token: ~A" line))
    ;; and it granted nothing: no allocation, no slot consumed.
    (check-equal '() (fleet-live-allocations reg :machine "m-a1")
                 "the refused take wrote an allocation anyway")
    ;; the CURRENT generation is admitted, so the refusal is the token's and not
    ;; the take's -- without this the case would pass over a `take` that refused
    ;; everything.
    (multiple-value-bind (took line)
        (fleet-take reg :machine "m-a1" :node "N1" :slots 1 :generation 4
                    :request "stale-gen-2" :holder "rowan" :allocation-id "alloc-ok"
                    :now 100)
      (ok took "the current machine generation was refused too: ~A" line))
    (check-equal 1 (length (fleet-live-allocations reg :machine "m-a1"))
                 "the admitted take did not write its allocation"))
  ;; 2. `heartbeat` and `release` on an allocation that is not live. An id the
  ;; allocator never issued, and one it issued and has already released.
  (multiple-value-bind (reg ok) (fleet-machine :concurrent 2 :generation 4)
    (ok ok "machine m-a1 is registered")
    (ok (fleet-take reg :machine "m-a1" :node "N1" :slots 1 :generation 4
                    :request "dead-1" :holder "rowan" :allocation-id "alloc-d" :now 100)
        "the take was refused")
    ;; an id nobody issued
    (multiple-value-bind (hok hline hcode)
        (fleet-heartbeat reg :allocation "alloc-nobody" :generation 4 :now 150)
      (check-equal nil hok "a heartbeat on an unknown allocation was renewed")
      (check-equal 1 hcode "the refusal exits 1")
      (ok (search "stale token" hline) "the refusal does not name the stale token: ~A" hline))
    (multiple-value-bind (rok rline rcode)
        (fleet-release reg :allocation "alloc-nobody" :generation 4 :now 150)
      (check-equal nil rok "a release of an unknown allocation was admitted")
      (check-equal 1 rcode "the refusal exits 1")
      (ok (search "stale token" rline) "the refusal does not name the stale token: ~A" rline))
    ;; and the same id once it is released: the slot comes back once, not twice.
    (ok (fleet-release reg :allocation "alloc-d" :generation 4 :now 150)
        "the first release was refused")
    (check-equal '() (fleet-live-allocations reg :machine "m-a1")
                 "the release did not free the slot")
    (multiple-value-bind (hok hline)
        (fleet-heartbeat reg :allocation "alloc-d" :generation 4 :now 160)
      (ok (not hok) "a released allocation was renewed: ~A" hline))
    (multiple-value-bind (rok rline)
        (fleet-release reg :allocation "alloc-d" :generation 4 :now 160)
      (ok (not rok) "a released allocation was released a second time: ~A" rline)))
  ;; 3. `release` under a stale ALLOCATION generation. The allocation is live
  ;; and the id is right; the token the caller holds is not.
  (multiple-value-bind (reg ok) (fleet-machine :concurrent 2 :generation 4)
    (ok ok "machine m-a1 is registered")
    (ok (fleet-take reg :machine "m-a1" :node "N1" :slots 1 :generation 4
                    :request "ag-1" :holder "rowan" :allocation-id "alloc-g"
                    :allocation-generation "ag-current" :now 100)
        "the take was refused")
    (multiple-value-bind (rok rline rcode)
        (fleet-release reg :allocation "alloc-g" :generation 4
                       :allocation-generation "ag-old" :now 150)
      (check-equal nil rok "a release under a stale allocation generation was admitted")
      (check-equal 1 rcode "the refusal exits 1")
      (ok (search "stale token" rline) "the refusal does not name the stale token: ~A" rline))
    (check-equal 1 (length (fleet-live-allocations reg :machine "m-a1"))
                 "the refused release freed the slot anyway")
    ;; the right token still works, so the refusal is the token's.
    (ok (fleet-release reg :allocation "alloc-g" :generation 4
                       :allocation-generation "ag-current" :now 150)
        "the current allocation generation was refused too")
    (check-equal '() (fleet-live-allocations reg :machine "m-a1")
                 "the admitted release did not free the slot")))

(deftest "fencing-a-lease-retires-its-generation"
    "docs/SPEC-WORK.md:4216-4218"
    "expected=fenced-state;generation-bumped;holder-and-node-untouched"
  ;; SPEC-WORK.md:4216-4218 -- "fencing a lease retires it as a writer of O".
  ;; Retiring it is the GENERATION BUMP: the token the old holder is carrying
  ;; stops being the current one. `friend-heartbeat` refuses a fenced holder on
  ;; `lease-fenced-p` first, so the bump is reachable by no refusal path at all
  ;; and is asserted here directly -- which is why deleting the `1+` left the
  ;; whole suite green.
  (let* ((lease (make-friend-lease :holder "emma" :node "acme/work/f1/t1" :generation 7))
         (fenced (fence-lease lease)))
    (ok (held-lease-p lease) "the lease starts held")
    (ok (not (lease-fenced-p lease)) "the original is fenced before anything happened")
    (ok (lease-fenced-p fenced) "the fenced lease is not fenced")
    (check-equal 8 (friend-lease-generation fenced)
                 "fencing did not retire the generation the old holder carries")
    ;; The fence touches the lease and nothing else it names.
    (check-string= "emma" (friend-lease-holder fenced) "the fence changed the holder")
    (check-string= "acme/work/f1/t1" (friend-lease-node fenced) "the fence changed the node")
    ;; Reading a finding never changes it: the original is untouched.
    (check-equal 7 (friend-lease-generation lease) "the fence mutated the lease it read")
    (ok (held-lease-p lease) "the fence mutated the lease it read")))
