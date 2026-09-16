;;;; replays-l2400-2.lisp --- the eight SPEC-WORK.md:2400-3600 replays named in
;;;; card 1501 batch 2. Each names its line of docs/SPEC-WORK.md and asserts what
;;;; the spec says that replay must show, against the pure functions of
;;;; src/replays-l2400-2.lisp.

(in-package #:nova-work/tests)

;;; ------------------------------------------------------------------
;;; Configuration exchange, bounded (SPEC-WORK.md:3409-3422)
;;; ------------------------------------------------------------------

(deftest "unchanged-config-is-one-bounded-answer" "docs/SPEC-WORK.md:3409"
    "expected=one-answer:unchanged-with-identity-when-equal,else-manifest"
  (let* ((routes '((:route :id "r1") (:route :id "r2")))
         (hash (sha256-hex (canonical-string routes)))
         (manifest (nova-work::make-config-manifest
                    :schema 2 :friend "glenn" :revision 7
                    :routes routes :content-hash hash)))
    (let ((answer (nova-work::config-exchange
                   (list :friend "glenn" :last-revision 7 :last-content-hash hash)
                   manifest)))
      (check-equal :unchanged (first answer) "an equal identity answers UNCHANGED")
      (check-equal (list :friend "glenn" :revision 7 :content-hash hash)
                   (second answer) "UNCHANGED carries that identity"))
    (let ((answer (nova-work::config-exchange
                   (list :friend "glenn" :last-revision 6 :last-content-hash hash)
                   manifest)))
      (check-equal :manifest (first answer) "a changed revision answers with the manifest"))))

(deftest "an-invalid-delta-leaves-the-old-config" "docs/SPEC-WORK.md:3411"
    "expected=invalid-base-returns-old-config;exact-base-applies-fragment"
  (let* ((routes '((:route :id "r1")))
         (hash (sha256-hex (canonical-string routes)))
         (manifest (nova-work::make-config-manifest
                    :schema 2 :friend "glenn" :revision 7
                    :routes routes :content-hash hash))
         (new-routes '((:route :id "r1") (:route :id "r2"))))
    (let ((result (nova-work::apply-config-delta
                   (list :base-revision 6 :base-content-hash hash :routes new-routes)
                   manifest)))
      (ok (equalp manifest result) "a delta on a stale base returns the old config"))
    (let ((result (nova-work::apply-config-delta
                   (list :base-revision 7 :base-content-hash "deadbeef" :routes new-routes)
                   manifest)))
      (ok (equalp manifest result) "a delta on a wrong hash returns the old config"))
    (let ((result (nova-work::apply-config-delta
                   (list :base-revision 7 :base-content-hash hash :routes new-routes)
                   manifest)))
      (check-equal 8 (nova-work::manifest-revision result) "a valid delta bumps the revision")
      (check-equal new-routes (nova-work::manifest-routes result) "a valid delta applies the fragment"))))

(deftest "a-partial-manifest-is-refused" "docs/SPEC-WORK.md:3415"
    "expected=partial-not-admitted-as-complete-replacement"
  (let* ((routes '((:route :id "r1") (:route :id "r2") (:route :id "r3")))
         (full-hash (sha256-hex (canonical-string routes)))
         (current (nova-work::make-config-manifest
                   :schema 2 :friend "glenn" :revision 7
                   :routes routes :content-hash full-hash))
         (complete (nova-work::make-config-manifest
                    :schema 2 :friend "glenn" :revision 8
                    :routes routes :content-hash full-hash))
         (partial (list :schema 2 :friend "glenn" :revision 8
                        :routes (subseq routes 0 2)
                        :completeness-hash full-hash)))
    (ok (nova-work::manifest-partial-p partial) "the truncated manifest is partial")
    (ok (not (nova-work::manifest-partial-p complete)) "the full manifest is complete")
    (check-equal current (nova-work::replace-config current partial)
                 "a partial manifest is refused and the old config stays")
    (check-equal complete (nova-work::replace-config current complete)
                 "a complete manifest is admitted")))

;;; ------------------------------------------------------------------
;;; The fleet (SPEC-WORK.md:3437-3566)
;;; ------------------------------------------------------------------

(deftest "fleet-is-static-config" "docs/SPEC-WORK.md:3563"
    "expected=machine-event-moves-no-count-no-roadmap;heartbeat-observe-probe-change-no-member"
  (let* ((m1 (nova-work::make-fleet-member
              :id "m-a1" :name "studio" :owner "glenn" :connect "profile:studio"
              :roles '(:build :test) :limits '(:concurrent 4)))
         (fleet (list m1))
         (work-open 5)
         (roadmap '((:row "t1") (:row "t2"))))
    (let ((m2 (nova-work::make-fleet-member
               :id "m-b2" :name "profiling host" :owner "glenn" :connect "profile:space"
               :roles '(:build :test :profile))))
      (multiple-value-bind (new-fleet next-open next-roadmap)
          (nova-work::fleet-transition fleet (list :kind :machine :member m2)
                                       work-open roadmap)
        (check-equal 2 (length new-fleet) "a :machine register adds a member")
        (check-equal work-open next-open "a :machine event moves no count")
        (check-equal roadmap next-roadmap "a :machine event moves no roadmap")))
    (dolist (kind '(:heartbeat :observe :probe))
      (let ((after (nova-work::fleet-observe fleet (list :kind kind))))
        (ok (equalp fleet after) "~A changes no member" kind)))))

(deftest "no-machine-name-in-the-tool" "docs/SPEC-WORK.md:3457"
    "expected=no-hostname-alias-user-path-arch-core-count-is-a-constant"
  (ok (null nova-work::*product-machine-names*)
      "the tool hardcodes no machine name")
  (let ((unnamed (nova-work::make-fleet-member :id "m-x" :owner "glenn")))
    (ok (null (nova-work::fleet-member-name unnamed))
        "a member with no :name has no name in the tool"))
  (let ((named (nova-work::make-fleet-member :id "m-a1" :name "studio" :owner "glenn")))
    (check-string= "studio" (nova-work::fleet-member-name named)
                   "the only :name is the team's own configuration")))

(deftest "one-profile-one-unit" "docs/SPEC-WORK.md:3564"
    "expected=connect-held-by-existing-id"
  (let ((fleet (list (nova-work::make-fleet-member
                      :id "m-a1" :name "studio" :owner "glenn" :connect "profile:studio"
                      :roles '(:build)))))
    (multiple-value-bind (okp result reason)
        (nova-work::register-fleet-member
         fleet
         (nova-work::make-fleet-member
          :id "m-b2" :name "other" :owner "glenn" :connect "profile:studio"
          :roles '(:build))
         '("glenn"))
      (ok (not okp) "a second member on one profile is refused")
      (check-equal fleet result "the refused register changes nothing")
      (check-equal "connect held by m-a1" reason "the refusal names the holder"))))

(deftest "no-credential-in-a-member" "docs/SPEC-WORK.md:3565"
    "expected=credential-in-record-refused"
  (dolist (bad '((:kind :machine :id "m-x" :owner "glenn" :connect "host:box")
                 (:kind :machine :id "m-y" :owner "glenn" :connect "profile:box"
                  :token "sekrit")))
    (ok (nova-work::fleet-member-has-credential-p bad)
        "a record with a non-profile :connect or a secret field is a credential"))
  (let ((clean (nova-work::make-fleet-member
                :id "m-a1" :name "studio" :owner "glenn" :connect "profile:studio")))
    (ok (not (nova-work::fleet-member-has-credential-p clean))
        "a profile reference with no secret field is no credential"))
  (multiple-value-bind (okp result reason)
      (nova-work::register-fleet-member
       '() (list :kind :machine :id "m-y" :owner "glenn" :connect "profile:box"
                 :token "sekrit")
       '("glenn"))
    (ok (not okp) "the credential record is refused whole")
    (check-equal "credential in record" reason "the refusal is `credential in record`")
    (check-equal '() result "nothing written")))

(deftest "unknown-owner-is-refused" "docs/SPEC-WORK.md:3565"
    "expected=unknown-owner-no-owner-no-id-refused-nothing-written"
  (let ((member (nova-work::make-fleet-member
                 :id "m-a1" :name "studio" :owner "stranger" :connect "profile:studio"
                 :roles '(:build))))
    (multiple-value-bind (okp result reason)
        (nova-work::register-fleet-member '() member '("glenn"))
      (ok (not okp) "an owner who is not a friend is refused")
      (check-equal "unknown owner" reason "the refusal is `unknown owner`")
      (check-equal '() result "nothing written")))
  (multiple-value-bind (okp-no-owner result-no-owner reason-no-owner)
      (nova-work::register-fleet-member
       '() (nova-work::make-fleet-member :id "m-x") '("glenn"))
    (ok (not okp-no-owner) "a member with no owner is refused")
    (check-equal "no owner" reason-no-owner "the refusal is `no owner`")
    (check-equal '() result-no-owner "nothing written"))
  (multiple-value-bind (okp-no-id result-no-id reason-no-id)
      (nova-work::register-fleet-member
       '() (nova-work::make-fleet-member :owner "glenn") '("glenn"))
    (ok (not okp-no-id) "a member with no id is refused")
    (check-equal "no id" reason-no-id "the refusal is `no id`")
    (check-equal '() result-no-id "nothing written")))
