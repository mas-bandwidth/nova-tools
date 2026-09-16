;;;; s6-operation.lisp --- the clip's transport is one long operation.
;;;;
;;;; docs/SPEC-WORK.md:2697-2721 — "Slow work returns a durable operation id, and
;;;; the control plane never waits behind it." The spelling is
;;;; `operation status|list|wait|cancel`, and a *clip* is that shape and not a
;;;; second synchronous one: `clip` prints `OPERATION OK … op=clip state=<queued|running>`
;;;; at once, and the terminal `CLIP OK` line carrying `operation=<id>` is what
;;;; `operation wait --id <id>` prints. `session stop` is the one caller that
;;;; waits for its own clip.
;;;;
;;;; These are records and pure functions only. A wiring card carries the accept
;;;; record to the local recovery journal and connects these to the command
;;;; thread; nothing here appends to a journal, fsyncs or serves a socket.

(in-package #:nova-work)

(defstruct (operation (:constructor %make-operation))
  id op state request author stamp updated staged rev pushed shown result external)

(defun operation-state-name (state)
  (ecase state
    (:queued "queued")
    (:running "running")
    (:done "done")
    (:cancelling "cancelling")
    (:cancelled "cancelled")
    (:failed "failed")))

(defun %op-name (op)
  (string-downcase (symbol-name (operation-op op))))

(defun make-operation (&key id op request author stamp)
  (%make-operation :id id :op op :state :queued :request request :author author
                   :stamp stamp :updated stamp :staged 0 :rev nil :pushed nil
                   :shown 0 :result nil :external "none"))

(defun operation-ack-line (operation &key (emitted 0))
  "`OPERATION OK id=<id> op=<kind> state=<queued|running> …`: the acknowledgement
the clip prints at once, before any transport."
  (format nil "OPERATION OK id=~A op=~A state=~A started=~A updated=~A staged=~D rev=~A pushed=~A shown=~D emitted=~D"
          (operation-id operation)
          (%op-name operation)
          (operation-state-name (operation-state operation))
          (operation-stamp operation)
          (operation-updated operation)
          (operation-staged operation)
          (or (operation-rev operation) "-")
          (or (operation-pushed operation) "-")
          (operation-shown operation)
          emitted))

(defun operation-row-line (operation)
  "`OPERATION ROW …`: one row of `operation list`, and one per event of a wait's cursor."
  (format nil "OPERATION ROW id=~A op=~A state=~A started=~A updated=~A external=~A"
          (operation-id operation) (%op-name operation)
          (operation-state-name (operation-state operation))
          (operation-stamp operation) (operation-updated operation)
          (operation-external operation)))

(defun operation-note-waiting (operation timeout after)
  "`OPERATION NOTE waiting …`: a wait that timed out; the operation is still running."
  (format nil "OPERATION NOTE waiting id=~A timeout=~A after=~A"
          (operation-id operation) timeout after))

(defun operation-no-such-line (id)
  "`OPERATION FAIL id=<id> op=- state=-: no such operation`, exit 2 — never an
invented state."
  (format nil "OPERATION FAIL id=~A op=- state=-: no such operation" id))

(defun operation-fail-line (operation reason)
  (format nil "OPERATION FAIL id=~A op=~A state=~A: ~A"
          (operation-id operation) (%op-name operation)
          (operation-state-name (operation-state operation)) reason))

(defun operation-terminal (operation)
  "The terminal result line, present only once the operation is done."
  (operation-result operation))

(defun operation-wait (operation &key (timeout nil) (after "-"))
  "Answer the terminal result line when the operation has settled, else the
waiting note. A wait that times out leaves the operation running."
  (if (member (operation-state operation) '(:done :cancelled :failed))
      (operation-terminal operation)
      (operation-note-waiting operation (or timeout "-") after)))

(defun operation-cancel (operation reason)
  "A cancellation is a request with its own disposition, not an erasure. It can
neither erase an accepted mutation nor undo an external effect."
  (declare (ignore reason))
  (setf (operation-state operation) :cancelling)
  (setf (operation-updated operation) (operation-stamp operation))
  operation)
