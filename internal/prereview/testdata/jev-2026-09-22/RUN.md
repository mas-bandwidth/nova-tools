# The recorded Jev pass over 122 cells (2026-09-22)

The ONE paid run of `nova-decide review` (nova-tools #2565). Every answer beside this
file is that run's; the tests replay them and dial nothing.

    nova-decide review --repo mas-bandwidth/schema --batch <the 122> --dry-run --table \
      --record internal/prereview/testdata/jev-2026-09-22

Population: the 122 cell pull requests in rowan-new
`reports/false-confidence-cells-2026-09-22.md` — every PR into `mas-bandwidth/schema`
base `fixed-table-form` from a `rowan/cell-*` branch opened 2026-09-21 or later. The
`class(report)` column is that report's JOHNNY class (75 of the 122 read line-by-line by
Johnny and Emma, 47 by pattern). No card file exists for any of these cells, so PATHS
came from each pull request's own `cell: <lang>/<row>` line — `paths_from=pr-body-cell`
on every row.

`--dry-run` throughout: **nothing was posted to any pull request.**

## DONE-WHEN

| want | got |
|---|---|
| symbol=no on #1459 #1469 #1493 #1507 #1558 #1486 #1497 #1506 #1539 #1561 | **10 of 10 no** |
| symbol=yes on #1488 and #1556 | **both yes** |

Pinned as a test: `TestSymbolCheckOnTheClassifiedCells`.

## Counts

| report class | symbol=yes | symbol=no |
|---|---|---|
| runtime (108) | 98 | 10 |
| self-check (10) | 0 | 10 |
| unclear (4) | 2 | 2 |

| check | yes | no | missing |
|---|---|---|---|
| symbol | 100 | 22 | 0 |
| paths | 120 | 2 | 0 |
| done | 122 | 0 | 0 |
| claims | 120 | 2 | 0 |

Verdicts: **97 HOLD, 25 APPROVE**. Every one of the 25 APPROVE rows is `runtime` in the
report; **none of the 10 known self-checks cleared** — zero false passes against the
friend classification on this population.

Jev score distribution (1-10): 1:7, 2:6, 3:2, 4:6, 5:8, 6:26, 7:41, 8:26. Nothing scored 9 or 10.

- self-check (10): mean **2.60**, median 2
- runtime (108): mean **6.40**, median 7
- unclear (4): mean **5.75**, median 6

The score is bimodal by class even though it correlates with nothing the friends wrote
(below): Jev put the ten self-checks at a median of 2 and the runtime cells at a median
of 7.

## Where the mechanical check disagrees with the report's class

Ten rows the report calls `runtime` and the symbol check calls `no`. There is no row the
other way: no pull request the report calls `self-check` passed the symbol check.

| PR | jev | why the symbol check said no |
|---|---|---|
| #1484 | 1 | the added test simulates what the generated code does instead of calling it (schema#1459, #1482) |
| #1490 | 1 | the added test reaches no generated symbol at all: no fixed-form entry point, no generated include, import or artifact path, no generator subprocess |
| #1502 | 7 | the added test reimplements the generated routine in the test (schema#1469, #1506, #1539, #1561) |
| #1508 | 2 | the added test reimplements the generated routine in the test (schema#1469, #1506, #1539, #1561) |
| #1521 | 6 | the added test asserts a literal true, which can never go red (schema#1507) |
| #1535 | 1 | the added test simulates what the generated code does instead of calling it (schema#1459, #1482) |
| #1542 | 2 | the added test reimplements the generated routine in the test (schema#1469, #1506, #1539, #1561) |
| #1543 | 7 | the added test reimplements the generated routine in the test (schema#1469, #1506, #1539, #1561) |
| #1545 | 8 | the added test reimplements the generated routine in the test (schema#1469, #1506, #1539, #1561) |
| #1572 | 7 | the added test reimplements the generated routine in the test (schema#1469, #1506, #1539, #1561) |

Read individually (the added lines are in the pull requests):

- **#1521 looks like a real finding, not an over-fire.** Its assertion is
  `check(true, "TableFixedKnown.hash is present, a u64, and first in its role")` — the
  same literal-true shape as #1507, which landed on a 10/10. The report classified this
  row `heuristic`, not read.
- **#1484 and #1535** say `// Simulate the report structure ...` and `// Simulate what a
  generated scatter function does ...`. Both deserve a friend's eye.
- **#1502, #1508, #1542, #1543, #1545, #1572** each DEFINE fnv1a64 locally and say why:
  it is an INDEPENDENT oracle, written out longhand so the test does not borrow the hash
  from the code it is checking, and the generated code's own hash is then compared against
  it. That is a legitimate technique and the check is wrong about all six. This is its
  clearest over-fire: it costs six good cards a HOLD, and the fix is to ask whether the
  reimplementation is COMPARED AGAINST generated output or STANDS IN FOR it -- which is
  not something a regular expression can see.
- **#1490** reaches no generated symbol the patterns can see.

## What the other three checks found

| PR | check | finding |
|---|---|---|
| #1477 | claims | 2 of 3 files the RESULT claims are not in the diff: internal/codegen/gotable/fixed_r26_test.go,test/conformance/go/rows/go.mod |
| #1488 | claims | 1 of 2 files the RESULT claims are not in the diff: test/conformance/go/go.mod |
| #1560 | paths | 1 of 2 changed files outside test/conformance/c/**, first notes.txt |
| #1569 | paths | 2 of 3 changed files outside test/conformance/rust/**, first serialize-stub/Cargo.toml |

`done` passed on all 122: every harvested cell carries a bare DONE on line 2.

#1569 is the one the report's own method notes flagged by hand ("adds a whole extra stub
crate beside the one test file"); the paths check found it without being told. #1560 drops
a `notes.txt` beside its cell. Neither friend read caught either at the time.

## Jev's score against the friends' highest score

n = 115 (the 115 rows whose report `max score` column is a number).

- Pearson r = **0.056**. Spearman rho = 0.022. Within 1 of the friend score: **6 of 115**.
- mean friend max = **9.53**, mean jev = **6.07**.
- friend max distribution: 2:2, 3:2, 4:1, 6:2, 7:2, 9:4, 10:102

**The correlation is ~0 and the reason is the target, not the model.** The friends' highest
score on this population is 10 on 102 of 115 rows — including all four landed self-checks.
A column that is one value 89% of the time cannot be correlated with. On the 13 rows where
a friend scored below 10, r = 0.384.

So: Jev's score does not predict what a friend wrote. It does separate the class the
friends' *reads* were wrong about (self-check median 2 vs runtime median 7). Calibration
should be against the JOHNNY class and against the landed/recut outcome, not against the
score column — and the weekly false-pass number Stella owns wants pairs at the same head,
which the ledger now writes.

## The 122 rows

| PR | cell | class(report) | read? | symbol | paths | done | claims | jev | verdict |
|---|---|---|---|---|---|---|---|---|---|
| #1459 | dart/r32 | self-check | read | no | yes | yes | yes | 3 | HOLD |
| #1460 | c/r6 | runtime | heuristic | yes | yes | yes | yes | 2 | HOLD |
| #1461 | cpp/c12 | runtime | heuristic | yes | yes | yes | yes | 7 | HOLD |
| #1462 | dart/r9 | runtime | read | yes | yes | yes | yes | 7 | HOLD |
| #1463 | c/r22 | runtime | heuristic | yes | yes | yes | yes | 6 | HOLD |
| #1464 | elixir/p3 | runtime | read | yes | yes | yes | yes | 7 | HOLD |
| #1465 | java/c5 | runtime | read | yes | yes | yes | yes | 8 | APPROVE |
| #1466 | cpp/c3 | runtime | heuristic | yes | yes | yes | yes | 7 | HOLD |
| #1467 | c/c2 | runtime | heuristic | yes | yes | yes | yes | 8 | APPROVE |
| #1468 | dart/w8 | runtime | heuristic | yes | yes | yes | yes | 8 | APPROVE |
| #1469 | c/w11 | self-check | read | no | yes | yes | yes | 2 | HOLD |
| #1470 | elixir/f12 | runtime | read | yes | yes | yes | yes | 7 | HOLD |
| #1471 | elixir/r22 | runtime | read | yes | yes | yes | yes | 7 | HOLD |
| #1472 | elixir/w1 | runtime | heuristic | yes | yes | yes | yes | 4 | HOLD |
| #1473 | elixir/r27 | runtime | read | yes | yes | yes | yes | 6 | HOLD |
| #1474 | cpp/w11 | runtime | heuristic | yes | yes | yes | yes | 8 | APPROVE |
| #1475 | cpp/c7 | runtime | heuristic | yes | yes | yes | yes | 8 | APPROVE |
| #1476 | c/w1 | runtime | heuristic | yes | yes | yes | yes | 6 | HOLD |
| #1477 | go/r26 | runtime | heuristic | yes | yes | yes | no | 7 | HOLD |
| #1478 | dart/r1 | runtime | heuristic | yes | yes | yes | yes | 6 | HOLD |
| #1479 | dart/f4 | runtime | read | yes | yes | yes | yes | 4 | HOLD |
| #1480 | go/f8 | runtime | heuristic | yes | yes | yes | yes | 7 | HOLD |
| #1481 | elixir/r26 | runtime | heuristic | yes | yes | yes | yes | 4 | HOLD |
| #1482 | js/r26 | unclear | read | no | yes | yes | yes | 6 | HOLD |
| #1483 | rust/w2 | runtime | read | yes | yes | yes | yes | 8 | APPROVE |
| #1484 | java/r13 | runtime | heuristic | no | yes | yes | yes | 1 | HOLD |
| #1485 | cs/w14 | runtime | heuristic | yes | yes | yes | yes | 5 | HOLD |
| #1486 | go/r31 | self-check | read | no | yes | yes | yes | 6 | HOLD |
| #1487 | cs/r28 | runtime | heuristic | yes | yes | yes | yes | 2 | HOLD |
| #1488 | go/r6 | runtime | read | yes | yes | yes | no | 6 | HOLD |
| #1489 | elixir/w5 | runtime | heuristic | yes | yes | yes | yes | 6 | HOLD |
| #1490 | cs/r27 | runtime | heuristic | no | yes | yes | yes | 1 | HOLD |
| #1491 | dart/w9 | runtime | heuristic | yes | yes | yes | yes | 5 | HOLD |
| #1492 | rust/e9 | unclear | read | yes | yes | yes | yes | 7 | HOLD |
| #1493 | js/w6 | self-check | read | no | yes | yes | yes | 3 | HOLD |
| #1494 | js/r16 | runtime | read | yes | yes | yes | yes | 7 | HOLD |
| #1495 | rust/w8 | runtime | read | yes | yes | yes | yes | 7 | HOLD |
| #1496 | dart/r16 | runtime | read | yes | yes | yes | yes | 5 | HOLD |
| #1497 | c/w13 | self-check | read | no | yes | yes | yes | 6 | HOLD |
| #1498 | cpp/p1 | runtime | heuristic | yes | yes | yes | yes | 7 | HOLD |
| #1499 | go/f12 | runtime | heuristic | yes | yes | yes | yes | 7 | HOLD |
| #1500 | go/w3 | runtime | heuristic | yes | yes | yes | yes | 7 | HOLD |
| #1501 | cpp/r16 | runtime | heuristic | yes | yes | yes | yes | 7 | HOLD |
| #1502 | rust/w12 | runtime | read | no | yes | yes | yes | 7 | HOLD |
| #1503 | java/w7 | runtime | read | yes | yes | yes | yes | 4 | HOLD |
| #1504 | rust/f8 | runtime | read | yes | yes | yes | yes | 7 | HOLD |
| #1505 | js/w14 | runtime | read | yes | yes | yes | yes | 8 | APPROVE |
| #1506 | go/r19 | self-check | read | no | yes | yes | yes | 1 | HOLD |
| #1507 | java/p4 | self-check | read | no | yes | yes | yes | 1 | HOLD |
| #1508 | go/w12 | runtime | read | no | yes | yes | yes | 2 | HOLD |
| #1509 | js/f10 | runtime | read | yes | yes | yes | yes | 7 | HOLD |
| #1510 | rust/w7 | runtime | read | yes | yes | yes | yes | 7 | HOLD |
| #1511 | js/f4 | runtime | read | yes | yes | yes | yes | 8 | APPROVE |
| #1512 | js/c9 | runtime | heuristic | yes | yes | yes | yes | 7 | HOLD |
| #1513 | cpp/r1 | runtime | read | yes | yes | yes | yes | 8 | APPROVE |
| #1514 | java/r7 | runtime | read | yes | yes | yes | yes | 7 | HOLD |
| #1515 | rust/c8 | runtime | read | yes | yes | yes | yes | 7 | HOLD |
| #1516 | go/p3 | runtime | read | yes | yes | yes | yes | 6 | HOLD |
| #1517 | elixir/f8 | runtime | read | yes | yes | yes | yes | 7 | HOLD |
| #1518 | js/r15 | runtime | read | yes | yes | yes | yes | 6 | HOLD |
| #1519 | java/w1 | runtime | read | yes | yes | yes | yes | 8 | APPROVE |
| #1521 | rust/r23 | runtime | read | no | yes | yes | yes | 6 | HOLD |
| #1522 | cpp/w2 | runtime | heuristic | yes | yes | yes | yes | 7 | HOLD |
| #1523 | cpp/e5 | runtime | heuristic | yes | yes | yes | yes | 7 | HOLD |
| #1524 | c/c9 | runtime | heuristic | yes | yes | yes | yes | 6 | HOLD |
| #1525 | js/r23 | runtime | heuristic | yes | yes | yes | yes | 7 | HOLD |
| #1526 | js/p4 | runtime | read | yes | yes | yes | yes | 7 | HOLD |
| #1527 | rust/c3 | runtime | heuristic | yes | yes | yes | yes | 6 | HOLD |
| #1528 | java/w4 | runtime | read | yes | yes | yes | yes | 6 | HOLD |
| #1529 | js/p3 | runtime | read | yes | yes | yes | yes | 7 | HOLD |
| #1530 | go/w4 | runtime | heuristic | yes | yes | yes | yes | 5 | HOLD |
| #1531 | dart/c3 | runtime | heuristic | yes | yes | yes | yes | 7 | HOLD |
| #1532 | dart/c13 | runtime | read | yes | yes | yes | yes | 8 | APPROVE |
| #1533 | dart/w13 | runtime | read | yes | yes | yes | yes | 7 | HOLD |
| #1534 | cpp/w4 | runtime | heuristic | yes | yes | yes | yes | 8 | APPROVE |
| #1535 | rust/r27 | runtime | read | no | yes | yes | yes | 1 | HOLD |
| #1536 | dart/c12 | runtime | read | yes | yes | yes | yes | 5 | HOLD |
| #1537 | java/w12 | runtime | read | yes | yes | yes | yes | 8 | APPROVE |
| #1538 | rust/c10 | runtime | heuristic | yes | yes | yes | yes | 7 | HOLD |
| #1539 | rust/r8 | self-check | read | no | yes | yes | yes | 2 | HOLD |
| #1540 | cpp/r20 | runtime | heuristic | yes | yes | yes | yes | 7 | HOLD |
| #1541 | rust/r30 | runtime | heuristic | yes | yes | yes | yes | 5 | HOLD |
| #1542 | rust/r4 | runtime | heuristic | no | yes | yes | yes | 2 | HOLD |
| #1543 | rust/e6 | runtime | read | no | yes | yes | yes | 7 | HOLD |
| #1544 | cpp/w12 | runtime | read | yes | yes | yes | yes | 6 | HOLD |
| #1545 | java/e6 | runtime | read | no | yes | yes | yes | 8 | HOLD |
| #1546 | java/f7 | runtime | read | yes | yes | yes | yes | 7 | HOLD |
| #1547 | dart/p3 | runtime | heuristic | yes | yes | yes | yes | 7 | HOLD |
| #1548 | rust/w14 | runtime | read | yes | yes | yes | yes | 4 | HOLD |
| #1549 | dart/c5 | runtime | read | yes | yes | yes | yes | 7 | HOLD |
| #1551 | rust/w1 | runtime | read | yes | yes | yes | yes | 7 | HOLD |
| #1555 | js/w13 | unclear | read | no | yes | yes | yes | 6 | HOLD |
| #1556 | cpp/w6 | runtime | read | yes | yes | yes | yes | 6 | HOLD |
| #1557 | dart/p2 | runtime | read | yes | yes | yes | yes | 8 | APPROVE |
| #1558 | c/r30 | self-check | read | no | yes | yes | yes | 1 | HOLD |
| #1559 | go/r22 | runtime | read | yes | yes | yes | yes | 8 | APPROVE |
| #1560 | c/r9 | runtime | read | yes | no | yes | yes | 6 | HOLD |
| #1561 | java/r4 | self-check | read | no | yes | yes | yes | 1 | HOLD |
| #1562 | dart/f8 | runtime | heuristic | yes | yes | yes | yes | 5 | HOLD |
| #1563 | dart/c2 | runtime | heuristic | yes | yes | yes | yes | 7 | HOLD |
| #1565 | c/f10 | runtime | heuristic | yes | yes | yes | yes | 8 | APPROVE |
| #1566 | c/c10 | runtime | read | yes | yes | yes | yes | 8 | APPROVE |
| #1567 | elixir/r29 | runtime | read | yes | yes | yes | yes | 6 | HOLD |
| #1568 | dart/r22 | runtime | read | yes | yes | yes | yes | 8 | APPROVE |
| #1569 | rust/p4 | runtime | read | yes | no | yes | yes | 6 | HOLD |
| #1570 | cpp/r6 | runtime | heuristic | yes | yes | yes | yes | 7 | HOLD |
| #1571 | c/p2 | runtime | heuristic | yes | yes | yes | yes | 6 | HOLD |
| #1572 | dart/w12 | runtime | read | no | yes | yes | yes | 7 | HOLD |
| #1573 | dart/c1 | runtime | read | yes | yes | yes | yes | 6 | HOLD |
| #1574 | js/r22 | runtime | read | yes | yes | yes | yes | 8 | APPROVE |
| #1575 | java/r30 | runtime | read | yes | yes | yes | yes | 6 | HOLD |
| #1576 | elixir/r13 | runtime | heuristic | yes | yes | yes | yes | 8 | APPROVE |
| #1577 | c/r23 | runtime | heuristic | yes | yes | yes | yes | 6 | HOLD |
| #1578 | c/e5 | runtime | read | yes | yes | yes | yes | 8 | APPROVE |
| #1579 | elixir/w8 | runtime | read | yes | yes | yes | yes | 5 | HOLD |
| #1580 | rust/r26 | runtime | read | yes | yes | yes | yes | 8 | APPROVE |
| #1581 | elixir/w9 | runtime | heuristic | yes | yes | yes | yes | 8 | APPROVE |
| #1582 | js/r9 | runtime | read | yes | yes | yes | yes | 8 | APPROVE |
| #1583 | cs/e5 | runtime | read | yes | yes | yes | yes | 6 | HOLD |
| #1584 | java/r27 | unclear | read | yes | yes | yes | yes | 4 | HOLD |
| #1585 | java/f10 | runtime | heuristic | yes | yes | yes | yes | 8 | APPROVE |
| #1586 | c/f9 | runtime | read | yes | yes | yes | yes | 7 | HOLD |
