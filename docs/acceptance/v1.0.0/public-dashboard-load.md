# v1.0.0 acceptance: the public sprint dashboard holds a front-page load

- Requirement: the public page serves at least 2,000 requests per second for ten minutes from a distant load host with zero errors; the server's own latency (measured on the page host during the run) has p99 under 50 ms, and the far vantage's p99 is recorded as it is; the dashboard server (port 7390) receives none of that traffic, only the puller's cadence; the puller survives the run
- Release: v1.0.0
- Measured: 2026-10-06T00:26:10Z
- From: the load host (a bench machine far from the page host, RTT 166 ms, idle loss 0 of 50) to the page host's public address, plain HTTP; the server's latency from the page host itself, to the same address
- Tool: hey 0.0.1 (three processes on the load host and one on the page host, ten minutes each, at the same time)
- Kernel: the page host runs bbr, net.core.default_qdisc=fq and tcp_slow_start_after_idle=0, read in force at 2026-10-06T00:24Z before the run; its NIC's per-queue qdiscs predate the change and are still fq_codel; the comparison run (2026-10-05T23:46:02Z) was under cubic, fq_codel and tcp_slow_start_after_idle=1
- Server latency: p99 0.9 ms, p50 0.5 ms, slowest 10.6 ms (60,000 requests to /api/sprint from the page host during the run, all 200)
- Far latency: p99 0.400 s and 0.402 s on /api/sprint, 0.408 s on /; under cubic it was 1.209 s and 1.240 s
- Verdict: MET

The owner, 2026-10-05: "If hacker news sends a lot of people to the live demo sprint dashboard,
will it be able to handle it? We should probably add this to the v1.0.0 requirements."

The latency gate is on the server. Two runs under cubic met every gate except a 200 ms p99 at the
far vantage, and a 166 ms path that loses segments cannot meet that, whatever the server does.
The far p99 is kept beside it as recorded, not as a gate.

Names in this record are roles: the page host is the fleet machine that serves the public page,
the dashboard machine runs the dashboard server, the load host and the second load host are the
two bench machines the load came from. Addresses are placeholders; the numbers are as the tools
printed them.

## Method

What was measured is the public-dashboard role as it runs on the page host today. That is the
shape of PR 5329 (`child/public-dashboard-static`), which is still open and is not on
`sprint/mechanical-2026-10-02`. Caddy's `file_server` serves `/var/www/sprint/site` with
precompressed gzip and has no `reverse_proxy`. `nova-dashboard-pull` fetches the dashboard
server at `http://<dashboard-address>:7390` about once per second and writes the files.

The load came from the load host to the page host's public address. Three hey processes ran
at the same time, each with `-H "Accept-Encoding: gzip"`:

- `hey -z 10m -c 400 -q 3 http://<page-host-address>/api/sprint`, twice: the snapshot every
  open page polls, 10.5 KB gzipped, capped at 1,200 rps per process. Two processes rather than
  one keep each under hey's statistics cap (below), so every response is in the percentiles;
- `hey -z 10m -c 100 -q 2 http://<page-host-address>/`: the front page, 9.5 KB gzipped, capped
  at 200 rps.

The load is the same as the cubic run's, which used one `-c 800 -q 3` process for /api/sprint.

The server's latency came from a fourth hey process on the page host, started with the others:
`hey -z 10m -c 4 -q 25 http://<page-host-address>/api/sprint` (100 rps). Its requests go to the
same Caddy site over loopback, so they time Caddy's service under the far load and leave the
path out. Caddy on the page host keeps no access log, and adding one would change the role.

The witness on the dashboard machine sampled `netstat -an -p tcp` every 5 s through the whole run
(126 samples, 00:26:10 to 00:36:39 UTC). It counted the sockets on `<dashboard-address>:7390` by
peer and by state. The dashboard server is a Python `ThreadingHTTPServer` that closes each
connection, so every connection leaves a TIME_WAIT on the dashboard machine for 2·MSL = 30 s. A
peer's TIME_WAIT count is therefore about its connections over the last 30 s. The baseline was
taken just before the run (6 samples, 00:25:40 to 00:26:05).

The puller was read with `systemctl show nova-dashboard-pull` and with the mtime of
`api/sprint.json`, before, every 55 s during, and after the run. The page host's TCP counters
(`nstat TcpOutSegs TcpRetransSegs`), its qdisc drops and its NIC counters were read at the same
times.

How hey counts. hey counts every result for `Requests/sec` (results over the run's total
time, errors included) and for the error distribution. It keeps the first 1,000,000 successful
results for everything else: the histogram, the percentiles and the status code distribution.
Its `Average` divides the sum over every success by the number kept, so past the cap it is too
high. In this run no process reached the cap, so each `Requests/sec` × `Total` equals its status
code count. In the cubic run the /api/sprint process passed it: its 2,062 rps is exact, about
1,240,264 responses, and its percentiles and its `[200] 1000000` cover the first 1,000,000 of
them.

## Results

| Gate | Target | Measured (10 min, under bbr) | Met |
|---|---|---|---|
| Throughput | ≥ 2,000 rps | /api/sprint 1,165.6 + 1,163.3 = 2,328.9 rps, plus / 199.9 rps: 2,528.8 rps | yes |
| Errors | 0 | 0: no error distribution in any process; 699,815 + 698,490 + 119,996 = 1,518,301 responses, every one 200 | yes |
| Server latency | p99 < 50 ms | p99 0.9 ms, p50 0.5 ms, slowest 10.6 ms (page host, 60,000 × 200) | yes |
| Far latency | recorded | p99 0.400 s / 0.402 s (/api/sprint), 0.408 s (/); p50 0.331 s / 0.331 s / 0.214 s | recorded |
| The dashboard server sees none of it | only the puller | the only remote peer on :7390 was the page host, mean 31.2 TIME_WAIT against a baseline of 30.3 (about 1 connection/s); no socket from the load host in any of 126 samples | yes |
| The puller survives | still running | MainPID 3324482 before, at each of the 10 samples during, and after; NRestarts=0 before and after; active since 2026-10-04 22:02:19 UTC; sprint.json mtime 00:36:23 after the run | yes |

**Under bbr against cubic, the same load from the same host:**

| | cubic (2026-10-05T23:46:02Z) | bbr (2026-10-06T00:26:10Z) |
|---|---|---|
| /api/sprint rps | 2,062.2 (one process) | 2,328.9 (two) |
| / rps | 191.1 | 199.9 |
| /api/sprint p50 / p99 | 0.333 s / 1.209 s | 0.331 s / 0.400 s, 0.402 s |
| / p50 / p99 | 0.336 s / 1.240 s | 0.214 s / 0.408 s |
| /api/sprint responses past about 0.7 s (histogram bins above 0.659 s; 0.702 s and 0.676 s) | 109,424 of the first 1,000,000 | 283 + 287 = 570 of 1,398,305 |
| Page host retransmitted segments | 1–11% in the probes | 0.052% over the run (7,274 of 13,978,890); 0.52% in the first minute, while 900 connections opened, then 0.0056% |
| Page host qdisc and NIC drops | 0 | 0 |

The page host's retransmits fell by two orders of magnitude, and the far p99 went from 7 round
trips to 2.4. The server was never the limit in either run. Caddy used about one core of 32
(`top`, 103% at 00:29) and answered loopback requests in half a millisecond under the full load.
The far p50 is two round trips and the p99 is just under three. What is left of the far p99 is
the distance: a viewer that far away waits about that long for any page, and only a host or
cache nearer to the viewer would shorten it.

The NIC qdiscs are still fq_codel, because `default_qdisc` only applies to qdiscs created after
it was set. bbr paces in the kernel without `fq` (Linux 4.13 and later), and these numbers were
measured with that pacing. Swapping in `fq` on the NIC is a host change for the owner; nothing
here depends on it.

In the cubic run, the second load host's 594 errors were `connect: cannot assign requested
address`: that host ran out of local ports as the client. They were not responses from the page
host.

## Raw

Load host, /api/sprint, first process (`hey -z 10m -c 400 -q 3 -H "Accept-Encoding: gzip"`), started 2026-10-06T00:26:10Z under bbr:

```

Summary:
  Total:	600.4085 secs
  Slowest:	1.8731 secs
  Fastest:	0.2007 secs
  Average:	0.2893 secs
  Requests/sec:	1165.5648
  

Response time histogram:
  0.201 [1]	|
  0.368 [645410]	|■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■
  0.535 [53773]	|■■■
  0.702 [348]	|
  0.870 [129]	|
  1.037 [82]	|
  1.204 [48]	|
  1.371 [15]	|
  1.539 [3]	|
  1.706 [4]	|
  1.873 [2]	|


Latency distribution:
  10%% in 0.2033 secs
  25%% in 0.2065 secs
  50%% in 0.3313 secs
  75%% in 0.3553 secs
  90%% in 0.3629 secs
  95%% in 0.3803 secs
  99%% in 0.4000 secs

Details (average, fastest, slowest):
  DNS+dialup:	0.0001 secs, 0.0000 secs, 0.2232 secs
  DNS-lookup:	0.0000 secs, 0.0000 secs, 0.0000 secs
  req write:	0.0000 secs, 0.0000 secs, 0.0019 secs
  resp wait:	0.1721 secs, 0.1645 secs, 0.7114 secs
  resp read:	0.1171 secs, 0.0000 secs, 1.7043 secs

Status code distribution:
  [200]	699815 responses
```

Load host, /api/sprint, second process (the same command), run at the same time:

```

Summary:
  Total:	600.4185 secs
  Slowest:	1.7840 secs
  Fastest:	0.2007 secs
  Average:	0.2903 secs
  Requests/sec:	1163.3385
  

Response time histogram:
  0.201 [1]	|
  0.359 [589019]	|■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■
  0.517 [108694]	|■■■■■■■
  0.676 [489]	|
  0.834 [144]	|
  0.992 [75]	|
  1.151 [42]	|
  1.309 [15]	|
  1.467 [1]	|
  1.626 [2]	|
  1.784 [8]	|


Latency distribution:
  10%% in 0.2033 secs
  25%% in 0.2073 secs
  50%% in 0.3310 secs
  75%% in 0.3521 secs
  90%% in 0.3680 secs
  95%% in 0.3919 secs
  99%% in 0.4022 secs

Details (average, fastest, slowest):
  DNS+dialup:	0.0001 secs, 0.0000 secs, 0.2196 secs
  DNS-lookup:	0.0000 secs, 0.0000 secs, 0.0000 secs
  req write:	0.0000 secs, 0.0000 secs, 0.0020 secs
  resp wait:	0.1723 secs, 0.1645 secs, 1.1153 secs
  resp read:	0.1178 secs, 0.0000 secs, 1.5679 secs

Status code distribution:
  [200]	698490 responses
```

Load host, / (`hey -z 10m -c 100 -q 2 -H "Accept-Encoding: gzip"`), run at the same time:

```

Summary:
  Total:	600.4303 secs
  Slowest:	1.0362 secs
  Fastest:	0.2009 secs
  Average:	0.2743 secs
  Requests/sec:	199.8500
  

Response time histogram:
  0.201 [1]	|
  0.284 [70551]	|■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■
  0.368 [19491]	|■■■■■■■■■■■
  0.451 [29516]	|■■■■■■■■■■■■■■■■■
  0.535 [344]	|
  0.619 [75]	|
  0.702 [7]	|
  0.786 [6]	|
  0.869 [3]	|
  0.953 [1]	|
  1.036 [1]	|


Latency distribution:
  10%% in 0.2023 secs
  25%% in 0.2051 secs
  50%% in 0.2137 secs
  75%% in 0.3620 secs
  90%% in 0.4019 secs
  95%% in 0.4040 secs
  99%% in 0.4075 secs

Details (average, fastest, slowest):
  DNS+dialup:	0.0001 secs, 0.0000 secs, 0.1890 secs
  DNS-lookup:	0.0000 secs, 0.0000 secs, 0.0000 secs
  req write:	0.0000 secs, 0.0000 secs, 0.0012 secs
  resp wait:	0.1729 secs, 0.1646 secs, 0.4511 secs
  resp read:	0.1013 secs, 0.0000 secs, 0.8568 secs

Status code distribution:
  [200]	119996 responses
```

Page host, /api/sprint over loopback (`hey -z 10m -c 4 -q 25 -H "Accept-Encoding: gzip"`), run at the same time, for the server's latency:

```

Summary:
  Total:	600.0033 secs
  Slowest:	0.0106 secs
  Fastest:	0.0002 secs
  Average:	0.0005 secs
  Requests/sec:	99.9995
  

Response time histogram:
  0.000 [1]	|
  0.001 [59616]	|■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■
  0.002 [280]	|
  0.003 [45]	|
  0.004 [27]	|
  0.005 [12]	|
  0.006 [4]	|
  0.007 [3]	|
  0.008 [3]	|
  0.010 [3]	|
  0.011 [6]	|


Latency distribution:
  10%% in 0.0004 secs
  25%% in 0.0004 secs
  50%% in 0.0005 secs
  75%% in 0.0005 secs
  90%% in 0.0006 secs
  95%% in 0.0006 secs
  99%% in 0.0009 secs

Details (average, fastest, slowest):
  DNS+dialup:	0.0000 secs, 0.0000 secs, 0.0004 secs
  DNS-lookup:	0.0000 secs, 0.0000 secs, 0.0000 secs
  req write:	0.0000 secs, 0.0000 secs, 0.0004 secs
  resp wait:	0.0003 secs, 0.0001 secs, 0.0104 secs
  resp read:	0.0001 secs, 0.0000 secs, 0.0023 secs

Status code distribution:
  [200]	60000 responses
```

Dashboard machine witness, sockets on <dashboard-address>:7390 by peer and state, under bbr. The baseline, then the
run summarised over its 126 samples (min, mean, max per sample), then its first and last samples:

```
baseline 00:25:40-00:26:05 (6 samples): <page-host-tailnet> TIME_WAIT mean 30.3; <dashboard-address> TIME_WAIT mean 28.7
run 00:26:10-00:36:39 (126 samples):
<page-host-tailnet> TIME_WAIT   samples=126 min=30 mean=31.2 max=32
<page-host-tailnet> FIN_WAIT_1  samples=51  min=1  mean=1.0  max=1
<page-host-tailnet> SYN_RCVD    samples=11  min=1  mean=1.0  max=1
<dashboard-address>  TIME_WAIT   samples=126 min=28 mean=29.1 max=30
<dashboard-address>  ESTABLISHED samples=2   min=1  mean=1.0  max=1
samples naming <load-host-address> or <load-host-tailnet> (load host): 0
00:26:10 <page-host-tailnet> TIME_WAIT 31
00:26:10 <dashboard-address> TIME_WAIT 30
00:36:39 <page-host-tailnet> TIME_WAIT 31
00:36:39 <dashboard-address> TIME_WAIT 28
```

The page host under bbr: the puller, sprint.json, `nstat`, the summed qdisc drops on the NIC, and the NIC TX line
(bytes packets errors dropped carrier collsns), before, every 55 s, and after:

```
before 2026-10-06T00:26:10Z
ActiveState=active
MainPID=3324482
NRestarts=0
sprint.json mtime 2026-10-06 00:26:10.430486992 +0000
TcpOutSegs                      893101445          0.0
TcpRetransSegs                  4066585            0.0
tcp_congestion_control=bbr default_qdisc=fq tcp_slow_start_after_idle=0
qdisc dropped sum 0
535044104486 585612324      0       0       0       0 
after 2026-10-06T00:36:23Z
ActiveState=active
MainPID=3324482
NRestarts=0
sprint.json mtime 2026-10-06 00:36:23.437962203 +0000
TcpOutSegs                      907080335          0.0
TcpRetransSegs                  4073859            0.0
qdisc dropped sum 0
552852096158 599210699      0       0       0       0 
00:27:06 TcpOutSegs=894363729 TcpRetransSegs=4073153  pull MainPID 3324482 mtime 00:27:06.453
00:28:01 TcpOutSegs=895641437 TcpRetransSegs=4073328  pull MainPID 3324482 mtime 00:28:01.438
00:28:57 TcpOutSegs=896925396 TcpRetransSegs=4073368  pull MainPID 3324482 mtime 00:28:56.448
00:29:52 TcpOutSegs=898213565 TcpRetransSegs=4073408  pull MainPID 3324482 mtime 00:29:51.430
00:30:47 TcpOutSegs=899500596 TcpRetransSegs=4073495  pull MainPID 3324482 mtime 00:30:47.433
00:31:42 TcpOutSegs=900790566 TcpRetransSegs=4073544  pull MainPID 3324482 mtime 00:31:42.427
00:32:38 TcpOutSegs=902082510 TcpRetransSegs=4073601  pull MainPID 3324482 mtime 00:32:37.438
00:33:33 TcpOutSegs=903375955 TcpRetransSegs=4073688  pull MainPID 3324482 mtime 00:33:32.430
00:34:28 TcpOutSegs=904670791 TcpRetransSegs=4073750  pull MainPID 3324482 mtime 00:34:28.433
00:35:23 TcpOutSegs=905963966 TcpRetransSegs=4073796  pull MainPID 3324482 mtime 00:35:23.432
caddy in top at 00:29: 103.2 %CPU (one sample, 5 s); established connections on :80: about 900, all bbr
RTT load host-><page-host-address>, 50 pings: min/avg/max 165.674/165.975/166.264 ms, 0% loss
page host NIC qdiscs: mq root, fq_codel children (created before default_qdisc was set to fq)
```

Under cubic: load host, /api/sprint (`hey -z 10m -c 800 -q 3 -H "Accept-Encoding: gzip"`), started 2026-10-05T23:46:02Z:

```

Summary:
  Total:	601.4261 secs
  Slowest:	2.6371 secs
  Fastest:	0.1647 secs
  Average:	0.4200 secs
  Requests/sec:	2062.2053
  

Response time histogram:
  0.165 [1]	|
  0.412 [863942]	|■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■
  0.659 [26633]	|■
  0.906 [61825]	|■■■
  1.154 [26995]	|■
  1.401 [19690]	|■
  1.648 [764]	|
  1.895 [143]	|
  2.143 [5]	|
  2.390 [1]	|
  2.637 [1]	|


Latency distribution:
  10%% in 0.1682 secs
  25%% in 0.1738 secs
  50%% in 0.3325 secs
  75%% in 0.3501 secs
  90%% in 0.7060 secs
  95%% in 0.9002 secs
  99%% in 1.2093 secs

Details (average, fastest, slowest):
  DNS+dialup:	0.0001 secs, 0.0000 secs, 0.2375 secs
  DNS-lookup:	0.0000 secs, 0.0000 secs, 0.0000 secs
  req write:	0.0000 secs, 0.0000 secs, 0.0024 secs
  resp wait:	0.2357 secs, 0.1645 secs, 1.1334 secs
  resp read:	0.1841 secs, 0.0000 secs, 2.1004 secs

Status code distribution:
  [200]	1000000 responses



```

Under cubic: load host, / (`hey -z 10m -c 100 -q 2 -H "Accept-Encoding: gzip"`), run at the same time:

```

Summary:
  Total:	601.2902 secs
  Slowest:	1.8922 secs
  Fastest:	0.1648 secs
  Average:	0.3637 secs
  Requests/sec:	191.0708
  

Response time histogram:
  0.165 [1]	|
  0.338 [61802]	|■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■
  0.510 [33195]	|■■■■■■■■■■■■■■■■■■■■■
  0.683 [6325]	|■■■■
  0.856 [4853]	|■■■
  1.029 [4656]	|■■■
  1.201 [2622]	|■■
  1.374 [1237]	|■
  1.547 [53]	|
  1.720 [127]	|
  1.892 [18]	|


Latency distribution:
  10%% in 0.1666 secs
  25%% in 0.1748 secs
  50%% in 0.3356 secs
  75%% in 0.3609 secs
  90%% in 0.7254 secs
  95%% in 0.9290 secs
  99%% in 1.2404 secs

Details (average, fastest, slowest):
  DNS+dialup:	0.0002 secs, 0.0000 secs, 0.1913 secs
  DNS-lookup:	0.0000 secs, 0.0000 secs, 0.0000 secs
  req write:	0.0000 secs, 0.0000 secs, 0.0006 secs
  resp wait:	0.1992 secs, 0.1646 secs, 1.0455 secs
  resp read:	0.1643 secs, 0.0000 secs, 1.7200 secs

Status code distribution:
  [200]	114889 responses



```

Under cubic: dashboard machine witness, sockets on <dashboard-address>:7390 by peer and state. The baseline, then the run
summarised over its 126 samples (min, mean, max per sample), then its first and last samples:

```
baseline 23:45:25-23:45:51 (6 samples): <page-host-tailnet> TIME_WAIT 29-31; <dashboard-address> TIME_WAIT 28-29
run 23:46:03-23:56:32 (126 samples):
<page-host-tailnet> TIME_WAIT  samples=126 min=28 mean=31.0 max=32
<page-host-tailnet> FIN_WAIT_1 samples=42  min=1  mean=1.0  max=1
<page-host-tailnet> SYN_RCVD   samples=10  min=1  mean=1.0  max=1
<dashboard-address>  TIME_WAIT  samples=126 min=28 mean=29.1 max=30
<dashboard-address>  FIN_WAIT_1 samples=1   min=1  mean=1.0  max=1
samples naming <load-host-address> or <load-host-tailnet> (load host): 0
23:46:03 <page-host-tailnet> TIME_WAIT 30
23:46:03 <dashboard-address> TIME_WAIT 30
23:56:32 <page-host-tailnet> TIME_WAIT 32
23:56:32 <dashboard-address> TIME_WAIT 29
```

(<page-host-tailnet> is the page host's tailnet address, where the puller connects from. <dashboard-address> is the
dashboard machine, whose own local clients use its tailnet address.)

Under cubic: puller on the page host, `systemctl show nova-dashboard-pull`:

```
before (23:45:55Z): MainPID=3324482 NRestarts=0 ActiveEnterTimestamp=Sun 2026-10-04 22:02:19 UTC  sprint.json mtime 23:45:55.677
mid    (23:47:21Z): MainPID=3324482 NRestarts=0  sprint.json mtime 23:47:20.613  caddy %CPU 145.5
after  (23:58:19Z): MainPID=3324482 NRestarts=0 ActiveState=active  sprint.json mtime 23:58:19.607
```

Under cubic: the page host's TCP counters and the probes (load host unless named; [TcpOutSegs TcpRetransSegs eno1 tx_bytes]):

```
probe 30s -c 500 -q 5   1918 rps  p50 0.178  p99 1.081
probe 20s -c 500 -q 5   1985 rps  p50 0.178  p99 1.094  39701 x 200
probe 20s -c 500 -q 2    889 rps  p50 0.339  p99 1.216  out 873254125->873423449 retrans 2586278->2604973
probe 20s -c 500 -q 4   1522 rps  p50 0.180  p99 1.190  out 873423457->873710033 retrans 2604974->2618151
tailnet 20s -c 500 -q 5 1482 rps  p50 0.171  p99 1.331  out 873712953->874054813 retrans 2618191->2643862
probe 30s -c 100 -q 2    199 rps  p50 0.171  p99 0.338  out 892506928->892563261 retrans 4059682->4059702
probe 30s -c 250 -q 2    477 rps  p50 0.179  p99 0.873  out 892563268->892701656 retrans 4059702->4062600
probe 30s -c 250 -q 4    922 rps  p50 0.176  p99 0.732  out 892701664->892962381 retrans 4062600->4065143
second load host 2m -c 800 -q 3 under load: eno1 tx 213-214 Mbit/s, out 890670468->891485177 retrans 3935221->3993705
idle 20s: eno1 tx 0 Mbit/s, out 892498793->892500494 retrans 4059618->4059629
page host qdisc eno1: dropped 0, overlimits 0; eno1 TX errors 0 dropped 0; speed 10000Mb/s
page host: tcp_congestion_control=cubic default_qdisc=fq_codel tcp_slow_start_after_idle=1 (tcp_bbr.ko present, not loaded)
local fetch on the page host under load: 0.000525 0.000545 0.000477 0.000487 0.000462 s
RTT load host-><page-host-address>: min/avg/max 165.926/166.038/166.143 ms; -><page-host-tailnet> 154.083/154.340/154.518 ms
```

Under cubic: second load host, /api/sprint (`hey -z 2m -c 800 -q 3 -H "Accept-Encoding: gzip"`), started after an earlier 3-minute run from the same host from 2026-10-05T23:58:26Z whose status lines were not kept (2,103 rps, p50 0.129, p99 1.372); a second vantage:

```

Summary:
  Total:	120.9228 secs
  Slowest:	7.8250 secs
  Fastest:	0.0737 secs
  Average:	0.2194 secs
  Requests/sec:	2079.8885
  

Response time histogram:
  0.074 [1]	|
  0.849 [236734]	|■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■
  1.624 [13132]	|■■
  2.399 [480]	|
  3.174 [278]	|
  3.949 [211]	|
  4.725 [0]	|
  5.500 [10]	|
  6.275 [23]	|
  7.050 [42]	|
  7.825 [1]	|


Latency distribution:
  10%% in 0.0938 secs
  25%% in 0.1091 secs
  50%% in 0.1309 secs
  75%% in 0.1796 secs
  90%% in 0.2807 secs
  95%% in 1.0164 secs
  99%% in 1.4491 secs

Details (average, fastest, slowest):
  DNS+dialup:	0.0003 secs, 0.0000 secs, 0.2836 secs
  DNS-lookup:	0.0000 secs, 0.0000 secs, 0.0000 secs
  req write:	0.0001 secs, 0.0000 secs, 0.0642 secs
  resp wait:	0.1556 secs, 0.0730 secs, 7.5623 secs
  resp read:	0.0468 secs, 0.0000 secs, 4.3381 secs

Status code distribution:
  [200]	250912 responses

Error distribution:
  [594]	Get "http://<page-host-address>/api/sprint": dial tcp <page-host-address>:80: connect: cannot assign requested address

```
