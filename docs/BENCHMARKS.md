# PFAEngine — Hot-Path Benchmarks (Tranche 9, NFR perf)

Reproduce with:

```sh
go test -run='^$' -bench=. -benchtime=1x ./internal/ledger/... ./internal/recon/... ./internal/contribution/...
```

Benchmarks never run under plain `go test ./...` (Go runs benchmarks only
when `-bench` is passed), so they add zero time to CI/default test runs.

## Environment

- Go 1.23.4, linux/amd64, `-benchtime=1x` (smoke) and default benchtime (stable)
- CPU: AMD EPYC, 2 vCPU (GOMAXPROCS=2) — containerized CI sandbox
- Money is integer kobo (`internal/money.Kobo`, int64) — no floats anywhere
  on the measured paths.

## Results

### `-benchtime=1x` (as specified for this tranche)

| Benchmark | Fixture | ns/op |
|---|---|---|
| `BenchmarkPost-2` (internal/ledger) | post 1 entry onto a 1,000-entry ledger | 1,010 |
| `BenchmarkBalance-2` (internal/ledger) | balance projection over 1,000 entries | 12,960 |
| `BenchmarkTrialBalance-2` (internal/ledger) | full trial balance, 1,000 entries | 10,950 |
| `BenchmarkReplay-2` (internal/ledger) | deterministic replay, 1,000 entries (FR-306) | 11,020 |
| `BenchmarkReconcile10K-2` (internal/recon) | 10,000 schedule lines vs 10,000 receipts, 97% auto-match (BR-03 ≥95% asserted in-benchmark) | 8,587,798 |
| `BenchmarkParseCSV10K-2` (internal/contribution) | parse a 10,000-row employer CSV schedule (FR-203) | 4,312,334 |

### Default benchtime (stability check)

| Benchmark | Iterations | ns/op |
|---|---|---|
| `BenchmarkPost-2` | 1,000,000 | 1,106 |
| `BenchmarkBalance-2` | 258,052 | 4,615 |
| `BenchmarkTrialBalance-2` | 119,775 | 9,462 |
| `BenchmarkReplay-2` | 130,346 | 9,237 |
| `BenchmarkReconcile10K-2` | 170 | 6,955,894 |
| `BenchmarkParseCSV10K-2` | 380 | 3,191,953 |

## Reading the numbers

- A ledger post is ~1.1 µs — dominated by mutex + balance validation; the
  journal append itself is O(1).
- Balance/trial-balance/replay are linear scans over the in-memory journal
  (~9–13 µs per 1,000 entries); the Rust hot-path mirror in `ledger-core/`
  exists for when journals outgrow this.
- Reconciliation of a 10k×10k file completes in ~7 ms because receipts are
  bucketed by `PIN|amount` for O(1) exact-match lookup (the amount-mismatch
  fallback scan only runs for the ≤5% exception lines).
- CSV ingestion parses 10k rows in ~3.2 ms (~320 ns/row) including the
  8%/10% statutory split per line.
