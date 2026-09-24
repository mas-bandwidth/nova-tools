# shellcheck shell=bash
# rowan-tools/bin/redis-pipe.bash -- the ONE pipelined Redis connection, sourced by friend-queue and
# blocked-resolve (nova-tools #3219; moved out of friend-queue unchanged, 2026-09-23, after
# blocked-resolve's one-redis-cli-per-call sweep crashed redis-cli with "Bus error: 10" at load 60 on
# the Studio). Replaced with them by nova-tools #3219 PR 4 (the Go verbs). Never run directly.
#
# The caller sets R (host), P (port), exports REDISCLI_AUTH, optionally RP_NAME (its own name for
# the lost-connection line and the FIFO dir), sets `trap disconnect EXIT` (or calls disconnect in its
# own trap), then: connect; q <cmd> <arg>...; go; rd (or awk "$AWK_HDR" over "$RF").
NBATCH=${NBATCH:-0}
# ---------------------------------------------------------------------------------------------
# The one connection. rq (perl, core modules only) reads command batches from its stdin -- one
# command per line, arguments TAB-separated, each argument escaped (\\ \t \n \r), a batch ended by
# a line "." -- sends the whole batch in one write (AUTH first on the first batch), then writes
# every reply FRAMED to the reply file (its 3rd argument): a header line (the number of value
# lines that follow, or "E" for an error reply whose one line is the message), then the lines: one
# per bulk/status/integer, an empty line for nil, arrays flattened depth-first (an empty or nil
# array is zero lines), CR/LF inside a value replaced by a space. When the file is complete it
# prints ONE line on stdout, ". <ms>" (the batch's server round trip). A read stalled past 60 s, a
# failed connect or AUTH, or EOF from the server exits 1; bash sees EOF on the reply FIFO and stops.
RQ='
  use IO::Socket::INET; use Time::HiRes qw(time);
  my ($h, $p, $rf) = @ARGV; $| = 1; my $s; my $first = 1;
  sub enc { my $r = "*" . scalar(@_) . "\r\n"; $r .= "\$" . length($_) . "\r\n" . $_ . "\r\n" for @_; $r }
  sub unesc { my $a = shift; $a =~ s/\\(.)/ $1 eq "t" ? "\t" : $1 eq "n" ? "\n" : $1 eq "r" ? "\r" : $1 /ge; $a }
  sub line { my $l = <$s>; defined $l or exit 1; $l =~ s/\r\n\z//; $l }
  sub bulk { my $n = shift; my $d = ""; while (length($d) < $n + 2) { my $got = read($s, $d, $n + 2 - length($d), length($d)); exit 1 unless $got } substr($d, -2) = ""; $d }
  sub flat {   # one reply -> list of value lines; sets $main::err on an error reply
    my $l = line(); my ($t, $v) = (substr($l, 0, 1), substr($l, 1));
    if ($t eq "+" || $t eq ":") { $v =~ s/[\r\n]/ /g; return ($v) }
    if ($t eq "-") { $main::err = 1; $v =~ s/[\r\n]/ /g; return ($v) }
    if ($t eq "\$") { return ("") if $v < 0; my $d = bulk($v); $d =~ s/[\r\n]/ /g; return ($d) }
    if ($t eq "*") { return () if $v <= 0; my @o; push @o, flat() for 1 .. $v; return @o }
    exit 1;
  }
  $SIG{ALRM} = sub { exit 1 };
  my @cmds;
  while (defined(my $l = <STDIN>)) {
    chomp $l;
    if ($l ne ".") { push @cmds, [ map { unesc($_) } split /\t/, $l, -1 ]; next }
    my $buf = "";
    if ($first) {
      $s = IO::Socket::INET->new(PeerAddr => $h, PeerPort => $p, Proto => "tcp", Timeout => 10) or exit 1;
      binmode $s;
      $buf = enc("AUTH", "bench", defined $ENV{REDISCLI_AUTH} ? $ENV{REDISCLI_AUTH} : "");
    }
    $buf .= enc(@$_) for @cmds;
    alarm 60;
    my $t0 = time;
    print $s $buf or exit 1;
    if ($first) { my $a = line(); exit 1 unless $a =~ /^\+/; $first = 0 }
    my $out = "";
    for (@cmds) { $main::err = 0; my @o = flat(); $out .= ($main::err ? "E" : scalar(@o)) . "\n"; $out .= "$_\n" for @o }
    my $ms = (time - $t0) * 1000;
    alarm 0;
    open(my $fh, ">", $rf) or exit 1; print $fh $out; close($fh) or exit 1;
    printf ". %.0f\n", $ms;
    @cmds = ();
  }
'
FIFODIR=""; RQPID=""; RF=""
connect(){
  FIFODIR=$(mktemp -d "${TMPDIR:-/tmp}/${RP_NAME:-redis-pipe}.XXXXXX") || return 1
  mkfifo "$FIFODIR/to" "$FIFODIR/from" || return 1
  RF="$FIFODIR/replies"
  perl -e "$RQ" "$R" "$P" "$RF" <"$FIFODIR/to" >"$FIFODIR/from" &
  RQPID=$!
  exec 3>"$FIFODIR/to" 4<"$FIFODIR/from"
  kill -0 "$RQPID" 2>/dev/null
}
disconnect(){
  # never `exec N>&- 2>/dev/null`: exec with no command makes EVERY redirection on the line
  # permanent, so that form sent the script's own stderr to /dev/null (found 2026-09-22 9:25 PM)
  [ -n "$RQPID" ] && exec 3>&- 4<&-
  [ "${FD5:-0}" = 1 ] && exec 5<&-
  FD5=0
  # reap the reader in the same group as the kill so bash's "Terminated: 15" job notice never
  # reaches stderr (it was on every call's output; nova-tools #3219 controls compare exact output)
  [ -n "$RQPID" ] && { kill "$RQPID"; wait "$RQPID"; } 2>/dev/null
  [ -n "$FIFODIR" ] && rm -rf "$FIFODIR"
  RQPID=""; FIFODIR=""
}
lost(){ echo "${RP_NAME:-redis-pipe}: redis connection lost ($1)" >&2; exit 3; }

# q <cmd> <arg>... -- append one command to the current batch (tab-joined, escaped; pure bash).
q(){
  local a out="" first=1
  for a in "$@"; do
    a=${a//\\/\\\\}; a=${a//$'\t'/\\t}; a=${a//$'\n'/\\n}; a=${a//$'\r'/\\r}
    if [ "$first" = 1 ]; then out=$a; first=0; else out="$out	$a"; fi
  done
  printf '%s\n' "$out" >&3 || lost write
}
# go -- end the batch, read every framed reply into $RF (the file the awk helpers below read),
# and open fd 5 on it for rd. One round trip. go = send, then collect.
go(){ send; collect; }
# send -- end the batch without waiting for it (the reader works it while the caller does something
# else: friend-row sends its row write and sleeps). The caller MUST collect before its next send.
send(){ printf '.\n' >&3 || lost write; }
# collect -- wait for the batch sent last, then open fd 5 on its replies for rd.
collect(){
  local l
  [ "${FD5:-0}" = 1 ] && exec 5<&-
  # one line back per batch: ". <ms>" once the replies are in $RF. Never the replies themselves
  # through this FIFO: bash reads a pipe one byte per syscall, and an 800-task list is ~150 KB
  # (MEASURED 2026-09-22 9:30 PM at load 50: 5-12 s inside two batches while PING was 350 ms).
  IFS= read -r l <&4 || lost read
  case "$l" in ". "*) ;; *) lost frame ;; esac
  BATCH_MS="$BATCH_MS${BATCH_MS:+,}${l#. }"
  NBATCH=$((NBATCH + 1))
  exec 5<"$RF"; FD5=1
}
# rd -- the next reply from fd 5: RN = line count, RE = 1 on an error reply, RL[0..RN-1] lines.
# shellcheck disable=SC2034  # RE is for callers that care about an error reply
rd(){
  local i l
  RN=0; RE=0; RL=()
  IFS= read -r l <&5 || lost frame
  if [ "$l" = E ]; then RE=1; RN=1; else RN=$l; fi
  i=0; while [ "$i" -lt "$RN" ]; do IFS= read -r l <&5 || lost frame; RL[$i]=$l; i=$((i + 1)); done
}
# awk prologue that walks $RF: k = reply number (1-based), pos = line position within it (1-based).
AWK_HDR='left==0 { k++; err=($0=="E"); left=err?1:$0+0; pos=0; next } { pos++; left-- }'
# xids_in <k> <id> -- every stream entry id in XRANGE reply k that carries task <id> (each XADD
# here writes exactly one field, "id", so an entry is a fixed 3-line block: eid, "id", value).
xids_in(){ awk -v K="$1" -v T="$2" "$AWK_HDR"' k==K { if (pos%3==1) eid=$0; else if (pos%3==0 && $0==T) print eid }' "$RF"; }
BATCH_MS=""
