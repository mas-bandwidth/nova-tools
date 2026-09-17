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
               (:file "src/dependencies")
               (:file "src/lock")
               (:file "src/journal")
               (:file "src/kernel")
               (:file "src/command-thread")
               (:file "src/session")
               (:file "src/state-export")
               (:file "src/fleet")
               (:file "src/closed-history")
               (:file "src/node-verbs")
               (:file "src/edit-undo")
               (:file "src/undo")
               (:file "src/pricing")
               (:file "src/operations")
               (:file "src/scheduler")
               (:file "src/assignment")
               (:file "src/control")
               (:file "src/roadmap")
               (:file "src/savepoint")
               (:file "src/new-verbs")))

(defsystem "nova-work/tests"
  :description "The slice-1 acceptance cases, each naming its line of docs/SPEC-WORK.md."
  :depends-on ("nova-work")
  :serial t
  :components ((:file "tests/harness")
               (:file "tests/acceptance")
               (:file "tests/replays-8680")
               (:file "tests/replays-applicable-delegation")
               (:file "tests/replays-8646")
               (:file "tests/replays-8662")
               (:file "tests/replays-8681")
               (:file "tests/replays-8683")
               (:file "tests/replays-8640")
               (:file "tests/replays-8641")
               (:file "tests/replays-8642")
               (:file "tests/replays-8643")
               (:file "tests/replays-8644")
               (:file "tests/replays-8645")
               (:file "tests/replays-8647")
               (:file "tests/replays-8648")
               (:file "tests/replays-8649")
               (:file "tests/replays-8650")
               (:file "tests/replays-8651")
               (:file "tests/replays-8660")
               (:file "tests/replays-8661")
               (:file "tests/replays-8663")
                (:file "tests/replays-8664")))
