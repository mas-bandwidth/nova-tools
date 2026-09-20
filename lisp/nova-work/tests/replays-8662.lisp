;;;; replays-8662.lisp --- the five fleet-allocation acceptance replays of
;;;; docs/SPEC-WORK.md:3622-3728 (the Fleet allocation amendment, #500), listed
;;;; in *Acceptance replays* at :6099-6140. Each deftest carries the replay name
;;;; the spec gives and drives the pure allocator model the src file
;;;; `replays-fleet-allocation.lisp` supplies.
;;;;
;;;; The live session, CLI, socket and transport wiring the command lines in
;;;; the spec imply is out of this slice: here the allocation model itself is
;;;; exercised, and the wiring owed is stated in RESULT.md.

(in-package #:nova-work/tests)

;;; ------------------------------------------------------------------
;;; helpers
;;; ------------------------------------------------------------------

(defun fleet-machine (&key (id "m-a1") (aliases '()) (concurrent 2) (cores 16)
                           (generation 4))
  "A registry holding the one physical machine ID with its alias set, the
declared concurrency and cores, its CONFIG generation and its declared facts."
  (let ((reg (make-fleet-registry)))
    (multiple-value-bind (ok line)
        (fleet-register-machine reg
                                :machine-id id :aliases aliases
                                :concurrent concurrent :cores cores :generation generation
                                :facts '(:os "macos" :declared-by "glenn"
                                         :declared-at "2026-09-15")
                                :limits '(:cores 16 :concurrent 2 :isolated-cores (2 3))
                                :connect "profile:studio"
                                :roles '("build" "test"))
      (declare (ignore line))
      (values reg ok))))

(defun fleet-kernel (&key (friends '("glenn" "rowan")) (id "m-a1")
                          (concurrent 2) (cores 16)
                          (limits '(:cores 16 :concurrent 2)))
  "A real kernel whose fleet section carries the one CONFIG member ID, so the
ACTIVE `take`, `heartbeat`, `release` and `probe` verbs have a machine to work
on. Registration goes through the real `machine` verb, never a back door."
  (let ((k (make-kernel :state (make-seed-state *seed*) :friends friends)))
    (multiple-value-bind (ok line code)
        (submit k (list :verb :machine :change :register :machine id
                        :name "studio" :owner "glenn" :connect "profile:studio"
                        :roles '(:build :test) :permits '("go-test")
                        :limits limits :facts nil :declared-by "glenn"
                        :request (format nil "mreq-~A" id)
                        :stamp "2026-09-15T01:05:00Z"))
      (unless ok (error "fleet-kernel register refused: ~A (~D)" line code)))
    k))

(defun fleet-take-request (&key (machine "m-a1") (node "N1") (slots 1)
                                (offer "offer-1") (attempt "attempt-1") (generation 1)
                                (request-ref "op-1") (batch "batch-1") (request "req-1")
                                (holder "rowan") allocation-id)
  "The request a `take` verb carries (SPEC-WORK.md:3656-3657)."
  (list :verb :take :machine machine :node node :slots slots :offer offer
        :attempt attempt :generation generation :request-ref request-ref
        :batch batch :request request :holder holder :allocation-id allocation-id))


;;; ------------------------------------------------------------------
;;; allocation-binds-machine-slot-generation   SPEC-WORK.md:3643
;;; ------------------------------------------------------------------

(deftest "allocation-binds-machine-slot-generation" "docs/SPEC-WORK.md:3643"
    "expected=binds-machine-slot-generation;active-not-config;core-pin-stays-declared;admission-before-preparation"
  (multiple-value-bind (reg ok) (fleet-machine)
    (ok ok "machine m-a1 is registered")
    (multiple-value-bind (took line code)
        (fleet-take reg :machine "m-a1" :node "schema/cpp/refuse-newer" :slots 1
                    :offer "offer-7" :attempt "attempt-2" :generation 4
                    :request-ref "opaque-1" :batch "batch-9" :request "req-take-1"
                    :holder "rowan" :allocation-id "alloc-a")
      (ok took "the take is admitted: ~A" line)
      (check-equal 0 code "take exit code")
      (ok (search "ALLOC OK" line) "ALLOC OK is printed: ~A" line)
      (ok (search "machine=m-a1" line) "the machine is named: ~A" line)
      (ok (search "allocation=alloc-a" line) "the allocation id is returned: ~A" line)
      (ok (search "slot=1" line) "the slot is bound: ~A" line)
      (ok (search "node=schema/cpp/refuse-newer" line) "the node is bound: ~A" line)
      (ok (search "batch=batch-9" line) "the batch is bound: ~A" line)
      (ok (search "offer=offer-7" line) "the offer is bound: ~A" line)
      (ok (search "attempt=attempt-2" line) "the attempt is bound: ~A" line)
      (ok (search "machine-generation=4" line) "the machine generation is bound: ~A" line)
      (ok (search "allocation-generation=" line) "the allocation generation is recorded: ~A" line)
      (let ((a (first (fleet-live-allocations reg :machine "m-a1"))))
        (ok a "one ACTIVE allocation exists")
        (check-equal "m-a1" (allocation-record-machine a) "the machine identity is referenced")
        (check-equal 4 (allocation-record-machine-generation a) "the CONFIG revision is referenced")
        (check-equal "offer-7" (allocation-record-offer a) "the offer half of the reservation key")
        (check-equal "attempt-2" (allocation-record-attempt a) "the attempt half of the reservation key")
        (check-equal :active (allocation-record-state a) "the allocation is ACTIVE data")
        (check-equal :before-preparation (allocation-record-admission-phase a)
                     "admission happens before preparation")
        (check-equal nil (allocation-record-core-pin a)
                     "the core-pin rule is not part of the allocation"))
      (check-equal '(:cores 16 :concurrent 2 :isolated-cores (2 3))
                   (fleet-declared-limits reg :machine "m-a1")
                   "the core-pin stays a declared CONFIG :limits constraint"))
    ;; The real `take` verb, over a kernel whose fleet section holds the CONFIG
    ;; member, writes the same ACTIVE record through `submit`: it binds the
    ;; machine's identity and CONFIG generation to the (offer, attempt) and
    ;; moves no work-tree count.
    (let* ((k (fleet-kernel))
           (before-open (state-open-count (kernel-state k)))
           (before-rev (state-revision (kernel-state k))))
      (multiple-value-bind (took line code)
          (submit k (fleet-take-request :allocation-id "alloc-a"))
        (ok took "the real take is admitted: ~A" line)
        (check-equal 0 code "real take exit code")
        (ok (search "ALLOC OK" line) "the real take prints ALLOC OK: ~A" line)
        (ok (search "machine=m-a1" line) "the line names the machine: ~A" line)
        (ok (search "allocation=alloc-a" line) "the line returns the allocation: ~A" line)
        (ok (search "slot=1" line) "the line binds the slot: ~A" line)
        (ok (search "offer=offer-1" line) "the line binds the offer: ~A" line)
        (ok (search "attempt=attempt-1" line) "the line binds the attempt: ~A" line)
        (ok (search "machine-generation=1" line)
            "the line binds the machine generation: ~A" line)
        (let ((a (first (fleet-live-allocations (kernel-allocations k)
                                                :machine "m-a1"))))
          (ok a "the real take writes one ACTIVE allocation")
          (check-equal :active (allocation-record-state a) "the record is ACTIVE")
          (check-equal :before-preparation (allocation-record-admission-phase a)
                       "admission happens before preparation"))
        (check-equal before-open (state-open-count (kernel-state k))
                     "a take moves no |O|")
        (check-equal before-rev (state-revision (kernel-state k))
                     "a take moves no work revision")))))

;;; ------------------------------------------------------------------
;;; allocation-take-is-atomic-and-idempotent   SPEC-WORK.md:3673
;;; ------------------------------------------------------------------

(deftest "allocation-take-is-atomic-and-idempotent" "docs/SPEC-WORK.md:3673"
    "expected=all-or-none;retry-original-line;changed-payload-refused;capacity-whole-refusal;heartbeat-stale-token;release-exact"
  (multiple-value-bind (reg ok) (fleet-machine :concurrent 2)
    (ok ok "machine m-a1 is registered")
    (let (took line code)
      (multiple-value-setq (took line code)
        (fleet-take reg :machine "m-a1" :node "N1" :slots 2 :offer "offer-1" :attempt "attempt-1"
                    :generation 4 :request-ref "op-1" :batch "batch-1" :request "req-1"
                    :holder "rowan" :allocation-id "alloc-a"))
      (ok took "both slots are granted whole: ~A" line)
      (check-equal 0 code "grant exit 0")
      (ok (search "slots=2" line) "the line names both slots: ~A" line)
    (let* ((a (first (fleet-live-allocations reg :machine "m-a1")))
           (ag (allocation-record-allocation-generation a)))
      (check-equal 2 (allocation-record-slots a) "two slots are recorded")
      ;; the retry under the same request id prints the original line and applies nothing.
      (multiple-value-bind (rretry rline rcode)
          (fleet-take reg :machine "m-a1" :node "N1" :slots 2 :offer "offer-1" :attempt "attempt-1"
                      :generation 4 :request-ref "op-1" :batch "batch-1" :request "req-1"
                      :holder "rowan" :allocation-id "alloc-a")
        (ok rretry "the retry is answered")
        (check-equal line rline "the retry prints the original ALLOC OK line")
        (check-equal 0 rcode "the retry exits 0")
        (check-equal 1 (length (fleet-live-allocations reg :machine "m-a1"))
                     "the retry applies nothing"))
      ;; a changed payload under the same request id is refused.
      (multiple-value-bind (cok cline ccode)
          (fleet-take reg :machine "m-a1" :node "N2" :slots 2 :offer "offer-1" :attempt "attempt-1"
                      :generation 4 :request-ref "op-1" :batch "batch-1" :request "req-1"
                      :holder "rowan")
        (check-equal nil cok "a changed payload under the id is refused")
        (check-equal 1 ccode "the reuse refusal exits 1")
        (ok (search "reused with a different payload" cline) "the refusal names the reuse: ~A" cline))
      ;; a take for more than the declared :concurrent admits is refused whole.
      (multiple-value-bind (fok fline fcode)
          (fleet-take reg :machine "m-a1" :node "N3" :slots 2 :offer "offer-2" :attempt "attempt-2"
                      :generation 4 :request-ref "op-2" :batch "batch-2" :request "req-2"
                      :holder "sam" :allocation-id "alloc-b")
        (check-equal nil fok "an over-capacity take is refused")
        (check-equal 1 fcode "the capacity refusal exits 1")
        (ok (search "ALLOC FAIL machine=m-a1 slots=2" fline) "the refusal names machine and slots: ~A" fline)
        (ok (search "holder=rowan" fline) "the refusal names the holder: ~A" fline)
        (ok (search "capacity" fline) "the refusal names capacity: ~A" fline)
        (check-equal 1 (length (fleet-live-allocations reg :machine "m-a1"))
                     "no partial grant"))
      ;; a heartbeat with the right generations is OK.
      (multiple-value-bind (hok hline hcode)
          (fleet-heartbeat reg :allocation "alloc-a" :generation 4 :allocation-generation ag :now 100)
        (ok hok "the heartbeat is admitted: ~A" hline)
        (check-equal 0 hcode "the heartbeat exits 0")
        (ok (search "ALLOC HEARTBEAT OK allocation=alloc-a" hline) "the heartbeat line: ~A" hline)
        (ok (search "machine-generation=4" hline) "the heartbeat carries the machine generation: ~A" hline))
      ;; a stale allocation generation is refused.
      (multiple-value-bind (sok sline scode)
          (fleet-heartbeat reg :allocation "alloc-a" :generation 4 :allocation-generation "ag-stale" :now 100)
        (check-equal nil sok "a stale allocation generation is refused")
        (check-equal 1 scode "the stale refusal exits 1")
        (ok (search "stale token" sline) "the refusal names stale token: ~A" sline)
        (ok (search "allocation=alloc-a" sline) "the refusal names the allocation: ~A" sline))
      ;; a stale machine generation is refused.
      (multiple-value-bind (sok sline)
          (fleet-heartbeat reg :allocation "alloc-a" :generation 3 :allocation-generation ag :now 100)
        (check-equal nil sok "a stale machine generation is refused")
        (ok (search "stale token" sline) "the refusal names stale token: ~A" sline))
      ;; release frees exactly that allocation's slot.
      (multiple-value-bind (rok rline rcode)
          (fleet-release reg :allocation "alloc-a" :generation 4 :allocation-generation ag :now 100)
        (ok rok "the release is admitted: ~A" rline)
        (check-equal 0 rcode "the release exits 0")
        (ok (search "ALLOC RELEASE OK allocation=alloc-a" rline) "the release line: ~A" rline)
        (ok (search "freed=true" rline) "the release names freed=true: ~A" rline)
        (check-equal 0 (length (fleet-live-allocations reg :machine "m-a1"))
                     "the released allocation is gone"))))
    ;; The real kernel verbs: `take` is atomic and idempotent, a changed payload
    ;; is refused, `heartbeat` validates both generations and `release` frees
    ;; exactly that allocation.
    (let ((k (fleet-kernel)))
      (multiple-value-bind (took line code)
          (submit k (fleet-take-request :slots 2 :offer "offer-1" :attempt "attempt-1"
                                        :request-ref "op-1" :batch "batch-1"
                                        :request "req-1" :allocation-id "alloc-a"))
        (ok took "the real kernel take grants both slots: ~A" line)
        (check-equal 0 code "real take exit code")
        (ok (search "slots=2" line) "the real line names both slots: ~A" line))
      (multiple-value-bind (rtook rline rcode)
          (submit k (fleet-take-request :slots 2 :offer "offer-1" :attempt "attempt-1"
                                        :request-ref "op-1" :batch "batch-1"
                                        :request "req-1" :allocation-id "alloc-a"))
        (ok rtook "the real retry is answered: ~A" rline)
        (check-equal 0 rcode "the real retry exits 0")
        (check-equal 1 (length (fleet-live-allocations (kernel-allocations k)
                                                        :machine "m-a1"))
                     "the real retry applies nothing"))
      (multiple-value-bind (cok cline ccode)
          (submit k (fleet-take-request :node "N2" :slots 2 :offer "offer-1"
                                        :attempt "attempt-1" :request-ref "op-1"
                                        :batch "batch-1" :request "req-1"))
        (check-equal nil cok "a changed payload under the id is refused")
        (check-equal 1 ccode "the real reuse refusal exits 1")
        (ok (search "reused with a different payload" cline)
            "the real refusal names the reuse: ~A" cline))
      (let ((ag (allocation-record-allocation-generation
                 (first (fleet-live-allocations (kernel-allocations k)
                                                :machine "m-a1")))))
        (multiple-value-bind (hok hline hcode)
            (submit k (list :verb :heartbeat :allocation "alloc-a" :generation 1
                            :allocation-generation ag :now 100))
          (ok hok "the real heartbeat is admitted: ~A" hline)
          (check-equal 0 hcode "the real heartbeat exits 0")
          (ok (search "ALLOC HEARTBEAT OK allocation=alloc-a" hline)
              "the real heartbeat line: ~A" hline))
        (multiple-value-bind (sok sline scode)
            (submit k (list :verb :heartbeat :allocation "alloc-a" :generation 1
                            :allocation-generation "ag-stale" :now 100))
          (check-equal nil sok "a stale allocation generation is refused")
          (check-equal 1 scode "the real stale refusal exits 1")
          (ok (search "stale token" sline)
              "the real refusal names stale token: ~A" sline))
        (multiple-value-bind (rok rline rcode)
            (submit k (list :verb :release :allocation "alloc-a" :generation 1
                            :allocation-generation ag :now 100))
          (ok rok "the real release is admitted: ~A" rline)
          (check-equal 0 rcode "the real release exits 0")
          (ok (search "ALLOC RELEASE OK allocation=alloc-a" rline)
              "the real release line: ~A" rline)
          (ok (search "freed=true" rline)
              "the real release names freed=true: ~A" rline)
          (check-equal 0 (length (fleet-live-allocations (kernel-allocations k)
                                                          :machine "m-a1"))
                       "the real released allocation is gone"))))))

;;; ------------------------------------------------------------------
;;; expiry-marks-suspect-reuse-needs-fencing   SPEC-WORK.md:3687
;;; ------------------------------------------------------------------

(deftest "expiry-marks-suspect-reuse-needs-fencing" "docs/SPEC-WORK.md:3687"
    "expected=suspect-blocks-renewal-and-reuse;capacity-retained;not-fenced-until-confirmed-termination"
  (multiple-value-bind (reg ok) (fleet-machine :concurrent 1)
    (ok ok "machine m-a1 is registered")
    (multiple-value-bind (took line)
        (fleet-take reg :machine "m-a1" :node "N1" :slots 1 :offer "offer-1" :attempt "attempt-1"
                    :generation 4 :request-ref "op-1" :batch "batch-1" :request "req-1"
                    :holder "rowan" :allocation-id "alloc-a" :now 100 :deadline 200)
      (ok took "the take is admitted: ~A" line))
    (let ((a (first (fleet-live-allocations reg :machine "m-a1"))))
      (check-equal nil (allocation-suspect-p a :now 150) "before the deadline it is not suspect")
      (check-equal t (allocation-suspect-p a :now 250) "past the deadline it is suspect")
      (ok (allocation-live-p a) "the suspect allocation is retained")
      (check-equal 1 (length (fleet-live-allocations reg :machine "m-a1"))
                   "the uncertain ACTIVE capacity is retained"))
    ;; a renewal past the deadline is refused suspect since the deadline.
    (multiple-value-bind (hok hline hcode)
        (fleet-heartbeat reg :allocation "alloc-a" :generation 4 :now 250)
      (check-equal nil hok "the renewal is refused")
      (check-equal 1 hcode "the suspect refusal exits 1")
      (ok (search "ALLOC FAIL machine=m-a1: suspect since=200" hline)
          "the refusal names the suspect stamp: ~A" hline))
    ;; a fresh take reusing the stale reservation token is refused.
    (multiple-value-bind (tok tline tcode)
        (fleet-take reg :machine "m-a1" :node "N2" :slots 1 :offer "offer-1" :attempt "attempt-1"
                    :generation 4 :request-ref "op-2" :batch "batch-2" :request "req-2"
                    :holder "rowan" :now 250 :deadline 350)
      (check-equal nil tok "reuse under the stale token is refused")
      (check-equal 1 tcode "the reuse refusal exits 1")
      (ok (search "suspect since=200" tline) "the reuse refusal names the suspect stamp: ~A" tline))
    ;; an expiry is not proof of termination: release needs confirmed termination.
    (multiple-value-bind (rok rline rcode)
        (fleet-release reg :allocation "alloc-a" :generation 4 :now 250)
      (check-equal nil rok "release without fencing is refused")
      (check-equal 1 rcode "the not-fenced refusal exits 1")
      (ok (search "ALLOC FAIL machine=m-a1: not fenced" rline) "the refusal names not fenced: ~A" rline)
      (check-equal 1 (length (fleet-live-allocations reg :machine "m-a1"))
                   "expiry alone does not free the slot"))
    ;; a verified stop observation allows the release and returns capacity.
    (multiple-value-bind (rok rline rcode)
        (fleet-release reg :allocation "alloc-a" :generation 4 :now 250 :stop-observed t)
      (ok rok "a verified stop frees the slot: ~A" rline)
      (check-equal 0 rcode "the fenced release exits 0")
      (check-equal 0 (length (fleet-live-allocations reg :machine "m-a1"))
                   "capacity returns only on confirmed termination"))))

;;; ------------------------------------------------------------------
;;; probe-records-observed-active-and-touches-no-config   SPEC-WORK.md:3702
;;; ------------------------------------------------------------------

(deftest "probe-records-observed-active-and-touches-no-config" "docs/SPEC-WORK.md:3702"
    "expected=observed-and-absent-active-evidence;config-unchanged;no-credential"
  (multiple-value-bind (reg ok) (fleet-machine)
    (ok ok "machine m-a1 is registered")
    (let ((facts-before (fleet-declared-facts reg :machine "m-a1"))
          (limits-before (fleet-declared-limits reg :machine "m-a1"))
          (connect-before (fleet-declared-connect reg :machine "m-a1"))
          (roles-before (fleet-declared-roles reg :machine "m-a1")))
      (multiple-value-bind (pok pline pcode)
          (fleet-probe reg :machine "m-a1" :slot 1 :source "ssh:studio" :fact :observed :at 500)
        (ok pok "the probe is recorded: ~A" pline)
        (check-equal 0 pcode "the probe exits 0")
        (ok (search "PROBE OK machine=m-a1 slot=1 fact=observed" pline)
            "the observed line: ~A" pline)
        (ok (search "at=500" pline) "the probe is dated: ~A" pline)
        (ok (search "source=ssh:studio" pline) "the probe names its source: ~A" pline))
      (multiple-value-bind (pok pline)
          (fleet-probe reg :machine "m-a1" :slot 1 :source "ssh:studio" :fact :absent :at 501)
        (ok pok "the absent observation is recorded: ~A" pline)
        (ok (search "fact=absent" pline) "what could not be established reads absent: ~A" pline))
      (let ((obs (fleet-observations reg :machine "m-a1")))
        (check-equal 2 (length obs) "two dated ACTIVE observations")
        (check-equal "ssh:studio" (getf (second obs) :source) "the source is recorded")
        (check-equal 501 (getf (second obs) :at) "the last-contact stamp is recorded")
        (ok (null (getf (second obs) :credential)) "no credential is recorded"))
      ;; declared support, verified runtime and free capacity stay three fields.
      (check-equal facts-before (fleet-declared-facts reg :machine "m-a1")
                   "the declared :facts are unchanged, declared-by and declared-at with them")
      (check-equal limits-before (fleet-declared-limits reg :machine "m-a1")
                   "the declared :limits are unchanged by a probe")
      (check-equal connect-before (fleet-declared-connect reg :machine "m-a1")
                   "the profile is never resolved and :connect is unchanged")
      (check-equal roles-before (fleet-declared-roles reg :machine "m-a1")
                   "the declared :roles are unchanged"))
    ;; The real `probe` verb writes the same dated ACTIVE observation through
    ;; `submit` and touches no CONFIG field.
    (let* ((k (fleet-kernel))
           (facts-before (fleet-declared-facts (kernel-allocations k) :machine "m-a1"))
           (limits-before (fleet-declared-limits (kernel-allocations k) :machine "m-a1"))
           (connect-before (fleet-declared-connect (kernel-allocations k) :machine "m-a1"))
           (roles-before (fleet-declared-roles (kernel-allocations k) :machine "m-a1")))
      (multiple-value-bind (pok pline pcode)
          (submit k (list :verb :probe :machine "m-a1" :slot 1 :source "ssh:studio"
                          :fact :observed :at 500))
        (ok pok "the real probe is recorded: ~A" pline)
        (check-equal 0 pcode "the real probe exits 0")
        (ok (search "PROBE OK machine=m-a1 slot=1 fact=observed" pline)
            "the real observed line: ~A" pline)
        (ok (search "at=500" pline) "the real probe is dated: ~A" pline)
        (ok (search "source=ssh:studio" pline)
            "the real probe names its source: ~A" pline))
      (multiple-value-bind (pok pline)
          (submit k (list :verb :probe :machine "m-a1" :slot 1 :source "ssh:studio"
                          :fact :absent :at 501))
        (ok pok "the real absent observation is recorded: ~A" pline)
        (ok (search "fact=absent" pline)
            "what could not be established reads absent: ~A" pline))
      (let ((obs (fleet-observations (kernel-allocations k) :machine "m-a1")))
        (check-equal 2 (length obs) "two dated ACTIVE observations")
        (check-equal 501 (getf (second obs) :at)
                     "the last-contact stamp is recorded")
        (ok (null (getf (second obs) :credential)) "no credential is recorded"))
      (check-equal facts-before (fleet-declared-facts (kernel-allocations k) :machine "m-a1")
                   "the real probe changes no declared :facts")
      (check-equal limits-before (fleet-declared-limits (kernel-allocations k) :machine "m-a1")
                   "the real probe changes no declared :limits")
      (check-equal connect-before (fleet-declared-connect (kernel-allocations k) :machine "m-a1")
                   "the real probe never resolves or changes :connect")
      (check-equal roles-before (fleet-declared-roles (kernel-allocations k) :machine "m-a1")
                   "the real probe changes no declared :roles"))))

;;; ------------------------------------------------------------------
;;; one-allocator-per-machine-aliases-share-nested-conserve   SPEC-WORK.md:3728
;;; ------------------------------------------------------------------

(deftest "one-allocator-per-machine-aliases-share-nested-conserve" "docs/SPEC-WORK.md:3728"
    "expected=second-allocator-refused;alias-one-record;nested-not-additional-slot;slots-from-concurrency"
  (multiple-value-bind (reg ok)
      (fleet-machine :concurrent 1 :cores 16 :aliases '("studio-a1"))
    (ok ok "the machine and its alias are registered")
    ;; a second allocator for the same physical machine is refused.
    (multiple-value-bind (alloc line)
        (fleet-open-allocator reg "m-a1")
      (check-equal nil alloc "a second allocator is refused")
      (check-string= "ALLOC FAIL machine=m-a1: allocator held" line
                     "the refusal names the held allocator"))
    (multiple-value-bind (alloc line)
        (fleet-open-allocator reg "studio-a1")
      (check-equal nil alloc "the alias is the same physical machine and is refused too")
      (check-string= "ALLOC FAIL machine=studio-a1: allocator held" line
                     "the alias refusal names the alias"))
    ;; both aliases resolve to one machine, and a take through one is the same record.
    (check-equal "m-a1" (fleet-resolve reg "studio-a1") "both aliases resolve to one machine")
    (multiple-value-bind (took line)
        (fleet-take reg :machine "studio-a1" :node "N1" :slots 1 :offer "offer-1" :attempt "attempt-1"
                    :generation 4 :request-ref "op-1" :batch "batch-1" :request "req-1"
                    :holder "rowan" :allocation-id "alloc-a")
      (ok took "the take through the alias is admitted: ~A" line))
    (check-equal 1 (length (fleet-live-allocations reg :machine "m-a1"))
                 "the allocation is counted once")
    (check-equal 1 (length (fleet-live-allocations reg :machine "studio-a1"))
                 "the same allocation reads under the other alias")
    (check-equal "m-a1" (allocation-record-machine
                         (first (fleet-live-allocations reg :machine "studio-a1")))
                 "the allocation references the physical machine identity")
    ;; a nested allocation draws from the same declared capacity, never an additional slot.
    (multiple-value-bind (nok nline)
        (fleet-take reg :machine "m-a1" :node "N1-child" :slots 1 :offer "offer-1" :attempt "attempt-1"
                    :generation 4 :request-ref "op-2" :batch "batch-1" :request "req-2"
                    :holder "rowan" :allocation-id "alloc-nested" :parent "alloc-a")
      (ok nok "the nested allocation is admitted: ~A" nline))
    (check-equal 1 (fleet-consumed-capacity reg :machine "m-a1")
                 "the nested allocation consumes no additional slot")
    ;; slots come from the declared concurrency, not from cores: cores sit idle.
    (multiple-value-bind (fok fline)
        (fleet-take reg :machine "m-a1" :node "N2" :slots 1 :offer "offer-2" :attempt "attempt-2"
                    :generation 4 :request-ref "op-3" :batch "batch-2" :request "req-3"
                    :holder "sam" :allocation-id "alloc-b")
      (check-equal nil fok "the third, non-nested take is refused")
      (ok (search "ALLOC FAIL machine=m-a1 slots=1" fline)
          "the refusal names the machine and slots: ~A" fline)
      (ok (search "capacity" fline) "the refusal names capacity, not cores: ~A" fline))
    ;; every live allocation is listed once.
    (let ((rows (fleet-list reg :machine "m-a1" :now 50)))
       (check-equal 2 (length rows) "each live allocation lists once")
       (ok (every (lambda (r) (search "ALLOC ROW" r)) rows)
           "every row is an ALLOC ROW"))))

;;; ------------------------------------------------------------------
;;; TestE09F04SeparateAbsorbFromDefaultLink   docs/SPEC-WORK.md:7590-7592
;;; ------------------------------------------------------------------

;;; Criterion E09-F04-01 (docs/roadmaps/nova-work.sexp:1120, subfeature
;;; "Separate absorb from default link and require selected scope/authority"),
;;; whose source section is "Link versus absorb", docs/SPEC-WORK.md:7586-7617.
;;; The controlling sentence, at :7590-7592, reads:
;;;   `absorb` ... is distinct from `link`, the default for outside
;;;   contributors. Author identity alone does not make an issue selected for
;;;   absorption; scope and intake mode must be explicit, with team
;;;   configuration identifying participating authors and applicable
;;;   repositories.
;;; So the criterion has two halves: (1) the default intake is `link`, with
;;; `absorb` a distinct second mode; and (2) absorption must require an
;;; explicitly selected scope and authority -- a participating author's
;;; identity, by itself, does not select an issue for absorption.

(deftest "TestE09F04SeparateAbsorbFromDefaultLink" "docs/SPEC-WORK.md:7590"
    "expected=outside-contributor-defaults-to-link;absorb-requires-explicit-selected-scope-and-authority"
  ;; (1) Outside contributors default to `link`: an :external, :mixed or
  ;; :unknown author's issue is retained (kept linked), never absorbed.
  (dolist (author '(:mixed :external :unknown))
    (let ((c (make-archive-capture :source-issue "acme/widget#7"
                                   :author author :gaps '())))
      (check-equal t (author-retains-source-p c)
                   "an outside contributor defaults to link (source retained)")))
  ;; (2) Absorb is separated from that default and requires a selected
  ;; scope/authority, not mere author identity. A participating (:known) author's
  ;; complete capture is NOT by itself selected for absorption: scope and intake
  ;; mode must be explicit before the issue may be absorbed. No explicit
  ;; selection is recorded on a bare capture, so absorbtion must not follow from
  ;; identity and completeness alone.
  (let ((c (make-archive-capture :source-issue "acme/widget#7"
                                 :author :known :gaps '())))
    (check-equal nil (archive-absorbable-p c)
                 "author identity alone must not select absorption; scope and intake mode must be explicit")))
