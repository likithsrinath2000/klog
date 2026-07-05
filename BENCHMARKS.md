# Benchmarks

How klog performs at scale, what was optimized, and where the limits are.

## Environment

- CPU: AMD EPYC 7763, 2 vCPU
- RAM: 7.7 GB (no swap)
- OS: Ubuntu 24.04, Go 1.22
- Method: `/usr/bin/time -v` (wall clock + peak RSS), page cache warm, best of a
  few runs. Data generated with `sample/gen_logs.py` (rich JSON: 11 fields, a
  nested object, and an array per row).

Reproduce:

```bash
python3 sample/gen_logs.py 1000000 /tmp/bench1m.log
/usr/bin/time -v ./bin/klog 'summarize n=count() by service' /tmp/bench1m.log
```

## Micro-benchmark: JSON parsing

The hand-written single-pass parser (`jsonlite.go`) vs `encoding/json` into a
`map[string]any`, on one representative log line:

```
BenchmarkJSONLite-2      2710 ns/op    98 MB/s    1818 B/op    47 allocs/op
BenchmarkStdlibJSON-2    7162 ns/op    37 MB/s    2210 B/op    66 allocs/op
```

About 2.6x faster with fewer allocations. Run it with:

```bash
go test ./cmd/klog/ -run=^$ -bench JSON -benchmem
```

## End-to-end: 1,000,000 rows (274 MB)

Before is klog v0.8.0 (`encoding/json` + `Scanner.Text`). After is the current
build (custom parser with key interning + `Scanner.Bytes`).

| Query | Before (wall) | Before (RSS) | After (wall) | After (RSS) |
|-------|--------------:|-------------:|-------------:|------------:|
| `count` | 10.2 s | 1784 MB | 5.1 s | 1547 MB |
| `where level=="ERROR" \| count` | 10.2 s | 1968 MB | 5.0 s | 1698 MB |
| `summarize n=count() by service` | 10.0 s | 1814 MB | 4.7 s | 1581 MB |
| `summarize count(),percentile(ms,95),avg(ms) by service,route` | 10.5 s | 1978 MB | 5.5 s | 1693 MB |
| `sort by ms desc \| take 20` | 15.0 s | 1818 MB | 9.5 s | 1552 MB |

Roughly 2x faster across the board (about 210k rows/s, 58 MB/s for a group-by),
with lower peak memory.

## End-to-end: 3,000,000 rows (822 MB)

| Query | Wall | RSS |
|-------|-----:|----:|
| `summarize n=count() by service` | 17.7 s | 4.7 GB |
| `where level=="ERROR" \| count` | 18.4 s | 5.1 GB |
| `sort by ms desc \| take 10` | 32.0 s | 4.8 GB |

## What the profiler showed

Before, a CPU profile of a group-by was dominated by `encoding/json`
(`decodeState.object` ~45% cumulative) and the garbage collector
(`scanobject` + `gcDrain` ~35%). The heap profile attributed ~2.0 GB to
`json.Unmarshal`, ~850 MB to `reflect.mapassign`, and ~300 MB to
`Scanner.Text`.

The fixes target exactly those:

- A single-pass JSON parser that builds maps directly, avoiding the reflection
  path (`reflect.mapassign`, `reflect.New`) and `encoding/json`'s separate
  validation pass.
- Object keys are interned; log keys repeat on every line, so this collapses
  millions of identical key strings to a handful.
- Scanning uses `Scanner.Bytes` and parses the bytes in place, removing the
  per-line string allocation.

Profile any query yourself:

```bash
./bin/klog --cpuprofile /tmp/cpu.prof --memprofile /tmp/mem.prof \
  'summarize n=count() by service' /tmp/bench1m.log
go tool pprof -top /tmp/cpu.prof
go tool pprof -top -sample_index=alloc_space /tmp/mem.prof
```

## Scaling characteristics and guidance

klog loads the whole input into memory and materializes every row as a
`map[string]any`, so **peak memory scales linearly with row count**, at roughly
1.6 GB per 1M rows for this rich schema (narrower rows use less). On a 7.7 GB
box that is a ceiling of about 3.5 to 4M rows before memory runs out.

To work with very large inputs today:

- Shrink the input first: a time window (`--from/--to`), an upstream `grep`, or
  splitting the file all reduce how much is held at once.
- Blocking operators (`summarize`, `sort`, `join`, `distinct`) need all rows;
  filter as early as possible so later stages see fewer rows.
- CPU scales with input size; parsing is the dominant cost, so narrower rows and
  fewer fields parse faster.

### Planned: streaming execution

The largest remaining win is a streaming fast-path for pipelines built only from
per-row operators plus a terminal `count`/`take` (for example
`where ... | count`). Those can run in constant memory regardless of file size,
removing the ceiling for the most common triage queries. Blocking operators
would continue to buffer. This is the next optimization on the roadmap.
