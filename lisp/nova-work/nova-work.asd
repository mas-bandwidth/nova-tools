;;;; nova-work.asd --- slice 1 of the nova-work engine.
;;;;
;;;; The spec fixes the engine's language (Common Lisp, docs/SPEC-WORK.md:290)
;;;; and fixes no directory for it. `lisp/nova-work/` with this ASDF system is a
;;;; layout choice for review, beside the Go client's existing `cmd/` and
;;;; `internal/`.

(defsystem "nova-work"
  :description "nova-work slice 1: the internal C/O transition kernel with a write-maintained |O|."
  :author "Rowan <rowan@mas-bandwidth.com>"
  :license "See LICENSE at the repository root"
  :serial t
  :components ((:file "src/package")
               (:file "src/conditions")
               (:file "src/sha256")
               (:file "src/value")
               (:file "src/event")
               (:file "src/state")
               (:file "src/journal")
               (:file "src/kernel")
               (:file "src/replays-l3600-3")))

(defsystem "nova-work/tests"
  :description "The slice-1 acceptance cases, each naming its line of docs/SPEC-WORK.md."
  :depends-on ("nova-work")
  :serial t
  :components ((:file "tests/harness")
               (:file "tests/acceptance")
               (:file "tests/replays-l3600-3")))
