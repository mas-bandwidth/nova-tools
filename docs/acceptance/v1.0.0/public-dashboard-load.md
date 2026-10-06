# v1.0.0 acceptance: the public sprint dashboard holds a front-page load

- Requirement: the public page serves at least 2,000 requests per second for ten minutes, measured from the load host, with p99 under 200 ms and zero errors; the dashboard server (port 7390) receives none of that traffic, only the puller's cadence; the puller survives the run
- Release: v1.0.0
- Measured: 2026-10-05T23:46:02Z
- From: the load host (a bench machine far from the page host, RTT 166 ms, idle loss 0 of 300) to the page host's public address, plain HTTP
- Tool: hey 0.0.1 (two processes in parallel, ten minutes each)
- Verdict: NOT MET

The owner, 2026-10-05: "If hacker news sends a lot of people to the live demo sprint dashboard,
will it be able to handle it? We should probably add this to the v1.0.0 requirements."

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

The load came from the load host to the page host's public address and ran two hey processes at
the same time, both with `-H "Accept-Encoding: gzip"`:

- `hey -z 10m -c 800 -q 3 http://<page-host-address>/api/sprint`: the snapshot every open page
  polls, 10.5 KB gzipped, capped at 2,400 rps;
- `hey -z 10m -c 100 -q 2 http://<page-host-address>/`: the front page, 9.5 KB gzipped, capped
  at 200 rps.

The witness on the dashboard machine sampled `netstat -an -p tcp` every 5 s through the whole run
(126 samples, 23:46:03 to 23:56:32 UTC). It counted the sockets on `<dashboard-address>:7390` by
peer and by state. The dashboard server is a Python `ThreadingHTTPServer` that closes each
connection, so every connection leaves a TIME_WAIT on the dashboard machine for 2·MSL = 30 s. A
peer's TIME_WAIT count is therefore about its connections over the last 30 s. The baseline was
taken just before the run (6 samples, 23:45:25 to 23:45:51).

The puller was read with `systemctl show nova-dashboard-pull` and with the mtime of
`api/sprint.json`, before and after the run. The page host's TCP counters (`nstat TcpOutSegs
TcpRetransSegs`), its qdisc and its NIC counters were read around the runs to find where the
tail comes from.

## Results

| Gate | Target | Measured (load host, 10 min) | Met |
|---|---|---|---|
| Throughput | ≥ 2,000 rps | /api/sprint 2,062 rps (a floor, see below) + / 191 rps | yes |
| Errors | 0 | 0 (no error distribution in either run; 1,000,000 + 114,889 responses, all 200) | yes |
| p99 | < 200 ms | /api/sprint 1.209 s, / 1.240 s (p50 0.333 s / 0.336 s) | **no** |
| The dashboard server sees none of it | only the puller | the only remote peer on :7390 was the page host, with a mean of 31.0 TIME_WAIT against a baseline of 30.2 (about 1 connection/s); no socket from the load host in any sample | yes |
| The puller survives | still running | MainPID 3324482, NRestarts=0 before and after, active since 2026-10-04 22:02:19 UTC; sprint.json mtime 23:58:19 after the run | yes |

hey keeps statistics for its first 1,000,000 successful results and computes rps from them, so
2,062 rps on /api/sprint is a lower bound. Errors are counted for every result.

**What blocks the p99, as measured: the network path, not the server.**

- The server is not the limit. During the run Caddy used 0.8 to 1.5 cores of 32. Requests
  made on the page host itself under load took 0.46 to 0.55 ms.
- The floor is the distance. The load host's RTT to the page host is 166 ms (154 ms over the
  tailnet), so a response only gets under 200 ms if it arrives in exactly one round trip. At
  199 rps, with 0.04% retransmits, p99 was already 338 ms.
- The tail is loss beyond the page host's port. Under load, 1–11% of the page host's TCP segments
  were retransmitted (0.04% at 199 rps, 2.1% at 477 rps, 1.0% at 922 rps, 11% at 889 rps, 4.6%
  at 1,522 rps, 7.5% over the tailnet at 1,482 rps). The page host's own qdisc dropped 0 and its
  NIC (10 Gb/s) dropped 0, and the loss is the same over the tailnet. The second load host (RTT
  86 ms) saw the same 7% retransmits at ~213 Mbit/s of egress, and its p99 was 1.449 s while its
  p50 was 0.131 s. The segment both paths share is the page host's upstream.
- The page host runs `cubic` with `tcp_slow_start_after_idle=1`. `tcp_bbr` is present as a
  module but not loaded. Each lost segment costs an RTO or a halved window over a 166 ms path,
  and that is what produces the 0.33 s and 0.7–1.2 s modes in the histograms.

What could move it, none of it applied here (each changes the host or the deployment, so each is
for the owner): BBR with the `fq` qdisc and `tcp_slow_start_after_idle=0` on the page host, which
paces the bursts that are lost upstream; a CDN in front of the page host, which also takes the
distance out of the p99 for far-away viewers; or a gate measured from a vantage near the page
host, or stated in terms of the server, since 200 ms is within 34 ms of the load host's RTT.

The second load host's 594 errors were `connect: cannot assign requested address`: that host ran
out of local ports as the client. They are not responses from the page host.

## Raw

Load host, /api/sprint (`hey -z 10m -c 800 -q 3 -H "Accept-Encoding: gzip"`), started 2026-10-05T23:46:02Z:

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

Load host, / (`hey -z 10m -c 100 -q 2 -H "Accept-Encoding: gzip"`), run at the same time:

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

Dashboard machine witness, sockets on <dashboard-address>:7390 by peer and state. The baseline, then the run
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

Puller on the page host, `systemctl show nova-dashboard-pull`:

```
before (23:45:55Z): MainPID=3324482 NRestarts=0 ActiveEnterTimestamp=Sun 2026-10-04 22:02:19 UTC  sprint.json mtime 23:45:55.677
mid    (23:47:21Z): MainPID=3324482 NRestarts=0  sprint.json mtime 23:47:20.613  caddy %CPU 145.5
after  (23:58:19Z): MainPID=3324482 NRestarts=0 ActiveState=active  sprint.json mtime 23:58:19.607
```

The page host's TCP counters and the probes (load host unless named; [TcpOutSegs TcpRetransSegs eno1 tx_bytes]):

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

Second load host, /api/sprint (`hey -z 2m -c 800 -q 3 -H "Accept-Encoding: gzip"`), started after an earlier 3-minute run from the same host from 2026-10-05T23:58:26Z whose status lines were not kept (2,103 rps, p50 0.129, p99 1.372); a second vantage:

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
