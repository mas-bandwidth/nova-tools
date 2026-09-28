;;;; slice-10-routes.lisp --- the four model-route acceptance replays of
;;;; docs/SPEC-WORK.md:3732-3799 and the replay rows at :6287-6314 (E09 row 2,
;;;; `query --ask routes`).
;;;;
;;;; Loaded by ../acceptance.lisp; an amendment edits one slice file. Each
;;;; replay sets up the route registry the paragraph describes and asserts the
;;;; outcome the paragraph promises: the listing prints the key by path or env
;;;; name and never its value, the projection orders flat before free before
;;;; metered, an unprobed route carries no card, and three abstains bench until
;;;; a probe passes.

(in-package #:nova-work/tests)

(defun probed-registry (&key (plan :flat) (cost nil) (id "r-1") (pass :true))
  "One registered route with one probe record."
  (let ((reg (make-route-registry)))
    (route-register reg :id id :provider "acme" :endpoint "https://api.example.com"
                    :key-location '(:env "ACME_KEY") :plan plan
                    :cost-per-mtok cost :capabilities '(:text :yes :code :yes)
                    :owner "glenn")
    (when pass (route-probe reg id :card "card-1" :pass pass
                            :at "2026-09-16T00:00:00Z" :source "pointer:probe-1"))
    reg))

;;; ------------------------------------------------------------------
;;; route-config-lists-key-by-path-never-value     SPEC-WORK.md:6292
;;; ------------------------------------------------------------------

(deftest "route-config-lists-key-by-path-never-value" "docs/SPEC-WORK.md:6292"
    "expected=listing-prints-path-or-env-name-never-value;bad-key-location-refused-credential-in-record;no-count-moves"
  (let* ((reg (make-route-registry))
         (route (route-register
                 reg :id "anthropic/claude-3.7-sonnet" :provider "anthropic"
                 :endpoint "https://api.example.com"
                 :key-location '(:env "ANTHROPIC_KEY") :plan :metered
                 :cost-per-mtok 15
                 :capabilities '(:text :yes :code :yes :tool-calls :yes)
                 :owner "glenn")))
    (ok route "a well-formed route is registered")
    (let ((ask (query-routes reg)))
      (ok (null (routes-ask-fail ask)) "the listing is not a refusal")
      (check-equal 1 (length (routes-ask-rows ask)) "one configured route is listed")
      (let ((row (first (routes-ask-rows ask))))
        (check-equal '(:env "ANTHROPIC_KEY") (getf row :key-location)
                     "the key is listed by its env name")
        (ok (search "key-location=(:env \"ANTHROPIC_KEY\")" (getf row :line))
            "the row prints the key location by env name")
        (ok (not (search "sk-" (getf row :line)))
            "no key value is ever printed in the row")))
    ;; A path location is listed by path, and never resolved.
    (let ((reg (make-route-registry)))
      (route-register reg :id "r-path" :provider "acme" :endpoint "https://x"
                      :key-location '(:path "/etc/nova/acme.key") :plan :flat
                      :capabilities '(:text :yes) :owner "glenn")
      (let* ((row (first (routes-ask-rows (query-routes reg)))))
        (check-equal '(:path "/etc/nova/acme.key") (getf row :key-location)
                     "the key is listed by path")
        (ok (search "/etc/nova/acme.key" (getf row :line))
            "the row prints the path")))
    ;; A key location that is neither a path nor an env name is a credential.
    (dolist (bad (list "sk-live-3f9a" '(:value "sk-live-3f9a") '(:file "x")))
      (multiple-value-bind (r line code)
          (route-register reg :id "r-bad" :provider "acme" :endpoint "https://x"
                          :key-location bad :plan :flat :owner "glenn")
        (ok (null r) "a raw key is refused: ~A" line)
        (check-equal 1 code "credential refusal exit code")
        (ok (search "credential in record" line) "the refusal names the rule: ~A" line)
        (ok (not (search "sk-live-3f9a" line)) "the refused value is never echoed")))
    ;; A route is CONFIG and never a work node: no count moves.
    (let* ((k (make-kernel :state (make-seed-state *seed*) :friends '("glenn")))
           (before (state-open-count (kernel-state k))))
      (route-register reg :id "r-config" :provider "acme" :endpoint "https://x"
                      :key-location '(:env "K") :plan :flat :owner "glenn")
      (check-equal before (state-open-count (kernel-state k))
                   "writing a route moves no |O|")
      (check-equal +absent+ (route-node-field (route-member reg "r-config"))
                   "a route event writes :node (:absent)")
      (check-equal '(:section :routes :kind :route :id "r-config")
                   (route-config-section (route-member reg "r-config"))
                   "the record is a :kind :route member of the routes section")
      ;; The real `route` verb writes the CONFIG member through `submit`.
      (multiple-value-bind (ok line code event)
          (submit k (list :verb :route :change :register
                          :route "anthropic/claude-3.7-sonnet"
                          :provider "anthropic" :endpoint "https://api.example.com"
                          :key-location '(:env "ANTHROPIC_KEY") :plan :metered
                          :cost-per-mtok 15
                          :capabilities '(:text :yes :code :yes :tool-calls :yes)
                          :owner "glenn" :request "rreq-1"))
        (ok ok "the real route verb admits a well-formed route: ~A" line)
        (check-equal 0 code "route register exit code")
        (ok (search "ROUTE OK" line) "the verb prints a ROUTE OK line")
        (check-equal t (absentp (getf event :node))
                     "the :route event writes :node (:absent)")
        (check-equal +absent+ (getf event :node) "the node field is absent")
        (let ((member (route-member (kernel-routes k)
                                    "anthropic/claude-3.7-sonnet")))
          (check-equal '(:section :routes :kind :route
                         :id "anthropic/claude-3.7-sonnet")
                       (route-config-section member)
                       "the record is a :kind :route member of the routes section")
          (check-equal '(:env "ANTHROPIC_KEY") (route-key-location member)
                       "the listing stores the key by env name, never value")))
      (check-equal before (state-open-count (kernel-state k))
                   "the real route verb moves no |O|")
      ;; The real verb refuses a raw credential and echoes no value.
      (multiple-value-bind (ok line code)
          (submit k (list :verb :route :change :register :route "r-bad"
                          :provider "acme" :endpoint "https://x"
                          :key-location "sk-live-3f9a" :plan :flat
                          :owner "glenn" :request "rreq-2"))
        (ok (not ok) "the real verb refuses a raw key: ~A" line)
        (check-equal 1 code "real credential refusal exit code")
        (ok (search "credential in record" line) "the refusal names the rule: ~A" line)
        (ok (not (search "sk-live-3f9a" line)) "the refused value is never echoed"))
      (check-equal 1 (route-member-count (kernel-routes k))
                   "a credential refusal writes no second route")
      (ok (null (route-member (kernel-routes k) "r-bad"))
          "the refused route is not in the registry"))))

;;; ------------------------------------------------------------------
;;; routing-picks-flat-before-metered              SPEC-WORK.md:6302
;;; ------------------------------------------------------------------

(deftest "routing-picks-flat-before-metered" "docs/SPEC-WORK.md:6302"
    "expected=flat-before-free-before-metered;metered-by-cost-then-id;repeated-route-is-a-share;projection-at-scope-revision"
  (let ((reg (make-route-registry)))
    (dolist (spec '(("r-flat" :flat nil)
                    ("r-free" :free nil)
                    ("r-meter" :metered 20)
                    ("r-meter2" :metered 5)
                    ("r-a" :metered 5)
                    ("r-b" :metered 5)))
      (destructuring-bind (id plan cost) spec
        (route-register reg :id id :provider "acme" :endpoint "https://x"
                        :key-location '(:env "K") :plan plan :cost-per-mtok cost
                        :owner "glenn")
        (route-probe reg id :card "c" :pass :true :at "2026-09-16T00:00:00Z")))
    (route-class-add reg "code" '("r-meter" "r-free" "r-flat" "r-meter2" "r-b" "r-a"))
    (let ((ask (query-routes reg :class "code" :scope-revision 9)))
      (check-equal '("r-flat" "r-free" "r-a" "r-b" "r-meter2" "r-meter")
                   (routes-ask-rows ask)
                   "flat, then free, then metered by cost ascending and id")
      (check-equal 9 (routes-ask-scope-revision ask)
                   "the projection names the scope revision it was derived at"))
    ;; A route listed n times gets n shares of the class's routing.
    (route-class-add reg "dup" '("r-flat" "r-flat" "r-free"))
    (check-equal '("r-flat" "r-flat" "r-free")
                 (routes-ask-rows (query-routes reg :class "dup"))
                 "a route listed twice is two shares")))

;;; ------------------------------------------------------------------
;;; unprobed-route-carries-no-card                 SPEC-WORK.md:6307
;;; ------------------------------------------------------------------

(deftest "unprobed-route-carries-no-card" "docs/SPEC-WORK.md:6307"
    "expected=no-probe-false-live-bench-absent;card-builder-refuses;passing-probe-carries-again"
  (let ((reg (make-route-registry)))
    ;; No probe record.
    (route-register reg :id "r-none" :provider "acme" :endpoint "https://x"
                    :key-location '(:env "K") :plan :flat :owner "glenn")
    ;; A probe whose pass is false.
    (route-register reg :id "r-false" :provider "acme" :endpoint "https://x"
                    :key-location '(:env "K") :plan :flat :owner "glenn")
    (route-probe reg "r-false" :card "c" :pass :false :at "2026-09-16T00:00:00Z")
    ;; A live bench.
    (route-register reg :id "r-bench" :provider "acme" :endpoint "https://x"
                    :key-location '(:env "K") :plan :flat :owner "glenn")
    (dotimes (i 3)
      (route-probe reg "r-bench" :card "c" :pass :absent
                   :at "2026-09-16T00:00:00Z" :benched-until "2030-01-01T00:00:00Z"))
    ;; A passing probe.
    (route-register reg :id "r-pass" :provider "acme" :endpoint "https://x"
                    :key-location '(:env "K") :plan :flat :owner "glenn")
    (route-probe reg "r-pass" :card "c" :pass :true :at "2026-09-16T00:00:00Z")
    (route-class-add reg "code" '("r-none" "r-false" "r-bench" "r-pass"))
    (check-equal '("r-pass") (routes-ask-rows (query-routes reg :class "code"))
                 "only the passing route projects a card")
    ;; The card builder refuses to cut a card for an unprobed route.
    (multiple-value-bind (card fail) (build-route-card reg "r-none")
      (ok (null card) "the card builder refuses an unprobed route")
      (ok (search "no card" fail) "the refusal says no card: ~A" fail))
    (multiple-value-bind (card fail) (build-route-card reg "r-pass")
      (declare (ignore fail))
      (ok card "the card builder cuts a card for a passing route"))
    ;; Once a probe passes, the route carries cards again.
    (route-probe reg "r-none" :card "c" :pass :true :at "2026-09-16T01:00:00Z")
    (ok (member "r-none" (routes-ask-rows (query-routes reg :class "code"))
                :test #'equal)
        "a route that passes again carries cards")))

;;; ------------------------------------------------------------------
;;; three-abstains-bench-until-probe               SPEC-WORK.md:6311
;;; ------------------------------------------------------------------

(deftest "three-abstains-bench-until-probe" "docs/SPEC-WORK.md:6311"
    "expected=three-abstains-bench;probe-before-date-changes-nothing;pass-clears-and-carries"
  (let ((reg (make-route-registry)))
    (route-register reg :id "r-1" :provider "acme" :endpoint "https://x"
                    :key-location '(:env "K") :plan :flat :owner "glenn")
    (route-class-add reg "code" '("r-1"))
    (route-probe reg "r-1" :card "c" :pass :absent :at "2026-09-16T00:00:00Z")
    (route-probe reg "r-1" :card "c" :pass :absent :at "2026-09-16T00:01:00Z")
    (ok (not (route-benched-p reg "r-1")) "two abstains do not bench")
    (route-probe reg "r-1" :card "c" :pass :absent :at "2026-09-16T00:02:00Z"
                 :benched-until "2030-01-01T00:00:00Z")
    (ok (route-benched-p reg "r-1") "the third abstain benches the route")
    (check-equal "2030-01-01T00:00:00Z" (route-benched-until reg "r-1")
                 "the bench carries its until date")
    (check-equal '() (routes-ask-rows (query-routes reg :class "code"))
                 "a benched route carries no card")
    ;; A further probe before that date changes nothing.
    (route-probe reg "r-1" :card "c" :pass :absent :at "2026-09-16T00:03:00Z"
                 :benched-until "2030-01-01T00:00:00Z")
    (ok (route-benched-p reg "r-1" :now "2029-01-01T00:00:00Z")
        "a probe before the bench date changes nothing")
    (check-equal '() (routes-ask-rows (query-routes reg :class "code"))
                 "the route still carries no card")
    ;; The next probe that passes clears the bench.
    (route-probe reg "r-1" :card "c" :pass :true :at "2026-09-17T00:00:00Z")
    (ok (not (route-benched-p reg "r-1")) "a passing probe clears the bench")
    (check-equal '("r-1") (routes-ask-rows (query-routes reg :class "code"))
                 "the cleared route carries cards again")))
