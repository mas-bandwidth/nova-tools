;;;; transport.lisp --- the socket endpoint, the local listener and the
;;;; resident session server daemon.
;;;;
;;;; SPEC-WORK.md:2642-2655: the engine is the resident session; the Go CLI is
;;;; a thin client over one explicitly named local endpoint (a Unix-domain
;;;; socket, or the Windows named pipe under its platform's spelling and never
;;;; a second transport). The socket and the directory that holds it belong to
;;;; the running account: the directory created 0700 and the socket 0600, both
;;;; owned by that account, and there is no network listener and no remote
;;;; evaluation protocol anywhere in this scope (replay
;;;; `endpoint-is-local-and-private`, prose :5879-5882).
;;;;
;;;; SPEC-WORK.md:268-310, :2256-2267: `session start` is the launcher and not
;;;; the session process; the process it starts is a supervised, long-lived
;;;; session that serves reads against its resident objects while a fresh CLI
;;;; process is never a fresh parse.

(in-package #:nova-work)

;;; ------------------------------------------------------------------
;;; The endpoint: a local socket path in a directory owned 0700.
;;; ------------------------------------------------------------------

(defstruct (session-endpoint
             (:constructor %make-session-endpoint
                 (directory socket-path directory-mode socket-mode owner socket-family)))
  directory socket-path directory-mode socket-mode owner socket-family)

(defun session-endpoint-dir-mode (endpoint) (session-endpoint-directory-mode endpoint))
(defun session-endpoint-file-mode (endpoint) (session-endpoint-socket-mode endpoint))

#+sbcl
(defun %file-mode (path)
  (logand (sb-posix:stat-mode (sb-posix:stat path)) #o777))

#+sbcl
(defun local-socket-family ()
  "The family of a local socket, for the endpoint assertion."
  (sb-bsd-sockets:socket-family (make-instance 'sb-bsd-sockets:local-socket :type :stream)))

#-sbcl
(defun local-socket-family () :local)

#+sbcl
(defun current-account-uid () (sb-posix:getuid))

#-sbcl
(defun current-account-uid () 0)

(defun make-session-endpoint (directory socket-path)
  "Create or reuse DIRECTORY at 0700 and validate SOCKET-PATH. A pre-existing
directory or socket with wider modes refuses rather than being reused. The
endpoint is a name for the local socket; MAKE-LOCAL-LISTENER does the binding,
so a validated endpoint is not itself a listener."
  (unless (probe-file directory)
    (sb-posix:mkdir directory #o700))
  (let ((dmode (%file-mode directory)))
    (unless (eql dmode #o700)
      (error 'unsupported-input
             :what (format nil "endpoint: pre-existing directory ~A has mode ~O, not 0700"
                           directory dmode))))
  (when (probe-file socket-path)
    (let ((smode (%file-mode socket-path)))
      (unless (eql smode #o600)
        (error 'unsupported-input
               :what (format nil "endpoint: pre-existing socket ~A has mode ~O, not 0600"
                             socket-path smode)))))
  (%make-session-endpoint directory socket-path #o700 #o600 (sb-posix:getuid)
                          (local-socket-family)))

(defun endpoint-network-listener-p (endpoint)
  "True iff the endpoint's socket family is a network family. The listener's
family is AF_UNIX, so this is false; an AF_INET family would make it true."
  (let ((family (session-endpoint-socket-family endpoint)))
    (and family (not (eql family (local-socket-family))))))

;;; ------------------------------------------------------------------
;;; The local listener: bind, listen, accept.
;;; ------------------------------------------------------------------

(defstruct (local-listener (:constructor %make-local-listener))
  endpoint socket (open-p t))

(defun make-local-listener (endpoint)
  "Bind and listen on ENDPOINT's local socket, chmod the bound path 0600 and
answer a LISTENER. A bound local socket is never a network listener."
  (let* ((socket (make-instance 'sb-bsd-sockets:local-socket :type :stream))
         (path (session-endpoint-socket-path endpoint)))
    (sb-bsd-sockets:socket-bind socket path)
    (sb-bsd-sockets:socket-listen socket 16)
    (sb-posix:chmod path #o600)
    (%make-local-listener :endpoint endpoint :socket socket :open-p t)))

(defun listener-open-p (listener)
  (and listener (local-listener-open-p listener)))

(defun listener-socket-family (listener)
  "The family of the listening socket: AF_UNIX for a local endpoint."
  (sb-bsd-sockets:socket-family (local-listener-socket listener)))

(defun listener-accept (listener)
  "Accept one connection on LISTENER and answer the connected socket."
  (values (sb-bsd-sockets:socket-accept (local-listener-socket listener))))

(defun listener-close (listener)
  "Close LISTENER's socket and unlink its path."
  (when (local-listener-open-p listener)
    (setf (local-listener-open-p listener) nil)
    (ignore-errors (sb-bsd-sockets:socket-close (local-listener-socket listener))))
  (ignore-errors (sb-posix:unlink (session-endpoint-socket-path
                                   (local-listener-endpoint listener))))
  listener)

;;; ------------------------------------------------------------------
;;; The resident session server daemon.
;;; ------------------------------------------------------------------

(defstruct (session-server (:constructor %make-session-server))
  session listener thread (running-p t) (path "") (owner "") (foreground t))

(defun session-identity-line (session)
  "The `SESSION OK` identity line a running session prints and serves: identity,
bounds and clip cadence are explicit and read, never remembered
(SPEC-WORK.md:304-308)."
  (let ((base (session-base session)))
    (format nil "SESSION OK owner=~A generation=~D token=~A state=~(~A~) until=~A base=~A every=~A skew=~A max-bytes=~D max-depth=~D max-nodes=~D index-cache=~D page-bytes=~D page-records=~D closed-window=~A"
            (session-owner session)
            (session-generation session)
            (session-token session)
            (session-state session)
            (session-until session)
            (if (or (null base) (zerop (length base))) "nil" base)
            (session-every session)
            (session-skew session)
            (session-max-bytes session)
            (session-max-depth session)
            (session-max-nodes session)
            (session-index-cache session)
            (session-page-bytes session)
            (session-page-records session)
            (session-closed-window session))))

(defun endpoint-directory-for (socket-path)
  "The directory that holds SOCKET-PATH, as a directory namestring."
  (namestring (make-pathname :name nil :type nil :defaults (pathname socket-path))))

(defun default-session-request-handler (server request)
  "The one in-process implementation of the serve seam. It answers the
read-only session requests from the resident session; mutations and the wire
protocol arrive in a later slice through *SESSION-REQUEST-HANDLER*."
  (cond
    ((member request '("status" "session status" "ping" "session ping")
             :test #'string=)
     (values t (session-identity-line (session-server-session server)) 0))
    (t
     (values nil (format nil "FAIL request=~A: unsupported in-process request"
                         request)
             2))))

(defvar *session-request-handler* nil
  "The serve seam: NIL selects DEFAULT-SESSION-REQUEST-HANDLER, the one
in-process implementation. A later wire slice binds the framed protocol here.")

(defun serve-session-request (server request)
  "Answer (values OK LINE EXIT) for one request line against SERVER."
  (funcall (or *session-request-handler* #'default-session-request-handler)
           server request))

(defun %serve-connection (server connection)
  "Serve every request line on one accepted CONNECTION until the peer closes."
  (unwind-protect
       (handler-case
           (let ((stream (sb-bsd-sockets:socket-make-stream
                          connection :input t :output t
                          :element-type 'character :external-format :utf-8)))
             (loop for request = (read-line stream nil :eof)
                   until (eq request :eof)
                   do (multiple-value-bind (ok answer code)
                          (serve-session-request server request)
                        (declare (ignore ok code))
                        (write-line answer stream)
                        (finish-output stream))))
         (error () nil))
    (ignore-errors (sb-bsd-sockets:socket-close connection))))

(defun %session-server-loop (server)
  "The daemon's accept loop: one thread per accepted local connection, until
the server is stopped."
  (loop while (session-server-running-p server)
        do (handler-case
               (multiple-value-bind (connection peer)
                   (sb-bsd-sockets:socket-accept
                    (local-listener-socket (session-server-listener server)))
                 (declare (ignore peer))
                 (sb-thread:make-thread
                  (lambda () (%serve-connection server connection))
                  :name "nova-work-session-connection"))
             (error () (return)))))

(defun start-session-server (session &key socket-path (foreground t))
  "Make SESSION the resident process: bind the local listener at SOCKET-PATH
and spawn the accept thread. Answer (values SERVER LINE EXIT). With FOREGROUND
NIL the caller is the launcher: it receives the SESSION OK line and returns
while the daemon stays up and serves (SPEC-WORK.md:268-280)."
  (let* ((endpoint (make-session-endpoint (endpoint-directory-for socket-path)
                                          socket-path))
         (listener (make-local-listener endpoint))
         (server (%make-session-server :session session
                                       :listener listener
                                       :running-p t
                                       :path socket-path
                                       :owner (session-owner session)
                                       :foreground foreground)))
    (setf (session-server-thread server)
          (sb-thread:make-thread (lambda () (%session-server-loop server))
                                 :name "nova-work-session-server"))
    (values server (session-identity-line session) 0)))

(defun session-server-stop (server)
  "Stop the daemon: close the listener, unlink its path and join the accept
thread. The session is stopped explicitly and inspectably (SPEC-WORK.md:308)."
  (when (session-server-running-p server)
    (setf (session-server-running-p server) nil)
    (listener-close (session-server-listener server))
    (let ((thread (session-server-thread server)))
      (when thread
        (ignore-errors (sb-thread:join-thread thread :timeout 5 :default nil)))))
  t)
