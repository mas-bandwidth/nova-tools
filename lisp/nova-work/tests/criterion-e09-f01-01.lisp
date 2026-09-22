;;;; criterion-e09-f01-01.lisp --- proof of E09-F01-01.
;;;;
;;;; nova-work criterion E09-F01-01 of E09-F01 (Non-destructive issue
;;;; inventory and capture): "Capture stable provider/repository/issue
;;;; identity, revision and URL" (docs/SPEC-WORK.md:7569: "Store a stable
;;;; provider/repository/issue identity plus its current URL and last
;;;; observed remote revision"; :7760: intake "keyed by the immutable forge
;;;; repository/issue identity so retries do not duplicate tasks").
;;;; The production path is src/capture.lisp: `capture-stage-input` stages
;;;; the triple with its URL and remote revision, and `capture-admit-result`
;;;; retains them on the admitted row.

(in-package #:nova-work/tests)

(deftest "e09-f01-01-capture-stable-provider-repository" "docs/SPEC-WORK.md:7569"
    "expected=stable-provider/repository/issue-identity+revision+url;repeat-intake-updates"
  (let ((stage (nova-work::make-capture-stage)))
    (multiple-value-bind (id line code)
        (nova-work::begin-source-capture stage :id "op-e09-f01-01" :request "req-e09-f01-01")
      (declare (ignore id))
      (check-equal 0 code "the source capture was not acknowledged")
      (ok (search "OPERATION OK" line) "the capture ack is not an OPERATION OK: ~A" line))
    ;; One issue stages with its stable identity, revision and URL.
    (multiple-value-bind (ok line)
        (nova-work::capture-stage-input stage :id "issue-7" :kind :issue
                                        :expected-revision 7 :bytes 128
                                        :provider "github"
                                        :repository "acme/widget"
                                        :issue "7"
                                        :url "https://github.com/acme/widget/issues/7"
                                        :remote-revision "rev-1")
      (ok ok "the issue input did not stage: ~A" line)
      (ok (search "github/acme/widget#7" line)
          "the staged line does not carry the stable identity: ~A" line))
    (check-equal "github/acme/widget#7"
                 (nova-work::capture-issue-key "github" "acme/widget" "7")
                 "the stable identity is not provider/repository#issue")
    (let ((staged (find "issue-7" (nova-work::capture-stage-inputs stage)
                        :key #'nova-work::capture-input-id :test #'equal)))
      (ok staged "the staged issue input is missing")
      (check-equal "github/acme/widget#7" (nova-work::capture-input-identity staged)
                   "the staged input does not carry the stable identity")
      (check-string= "https://github.com/acme/widget/issues/7"
                     (nova-work::capture-input-url staged)
                     "the staged input does not carry the URL")
      (check-string= "rev-1" (nova-work::capture-input-remote-revision staged)
                     "the staged input does not carry the remote revision"))
    ;; The admitted result retains the identity, revision and URL.
    (multiple-value-bind (ok line)
        (nova-work::capture-admit-result stage 7 :id "issue-7" :result :mapped)
      (ok ok "the staged issue result was not admitted: ~A" line))
    (let ((row (nova-work::capture-result-of stage "issue-7")))
      (ok row "the admitted issue result is missing")
      (check-equal "github/acme/widget#7" (nova-work::capture-result-identity row)
                   "the admitted row does not carry the stable identity")
      (check-string= "https://github.com/acme/widget/issues/7"
                     (nova-work::capture-result-url row)
                     "the admitted row does not carry the URL")
      (check-string= "rev-1" (nova-work::capture-result-remote-revision row)
                     "the admitted row does not carry the remote revision")
      (check-equal 7 (getf row :revision)
                   "the admitted row does not carry the revision"))
    ;; Repeated intake of the same identity updates instead of duplicating.
    (multiple-value-bind (ok line)
        (nova-work::capture-stage-input stage :id "issue-7" :kind :issue
                                        :expected-revision 8 :bytes 128
                                        :provider "github"
                                        :repository "acme/widget"
                                        :issue "7"
                                        :url "https://github.com/acme/widget/issues/7#comment-2"
                                        :remote-revision "rev-2")
      (ok ok "the repeated intake did not stage: ~A" line)
      (ok (search "github/acme/widget#7" line)
          "the repeated intake does not carry the stable identity: ~A" line))
    (check-equal 1 (nova-work::capture-input-count stage)
                 "the repeated intake duplicated the staged work")
    (let ((staged (find "issue-7" (nova-work::capture-stage-inputs stage)
                        :key #'nova-work::capture-input-id :test #'equal)))
      (check-string= "https://github.com/acme/widget/issues/7#comment-2"
                     (nova-work::capture-input-url staged)
                     "the repeated intake did not update the URL")
      (check-string= "rev-2" (nova-work::capture-input-remote-revision staged)
                     "the repeated intake did not update the remote revision"))))
