# klog

**A KQL-lite query engine for JSON/NDJSON logs.** `grep` + `jq` + `awk`, replaced by
one Kusto/KQL-style pipeline. Single static Go binary, zero dependencies.

```bash
klog 'where level=="ERROR" | summarize n=count() by service | sort by n desc' app.log
```
```
service  n
-------  -
api      2
auth     1
```

## Why

Logs are usually NDJSON (one JSON object per line). Slicing them normally means
gluing together `jq`, `grep`, `sort` and `awk`. If you already think in
[KQL](https://learn.microsoft.com/azure/data-explorer/kusto/query/) (Azure Data
Explorer), `klog` lets you write the same pipeline locally against a file or stdin.

## Install

```bash
go install github.com/likithsrinath2000/klog/cmd/klog@latest
# or build from source
make build && ./bin/klog --version
```

## Usage

```
klog [flags] '<query>' [file ...]
cat app.log | klog '<query>'
```

Input is NDJSON. Blank lines are skipped; lines that aren't valid JSON are still
queryable via the synthetic fields `_line` (number) and `_raw` (text).

### Operators

| Operator | Example | Description |
|----------|---------|-------------|
| `where` / `filter` | `where level=="ERROR" and ms > 500` | keep matching rows |
| `project` / `select` | `project ts, svc=service, ms` | pick / rename fields |
| `summarize` / `stats` | `summarize n=count(), avg(ms) by service` | group + aggregate |
| `sort` / `order` | `sort by ms desc` | order rows |
| `take` / `limit` | `take 20` | keep first n rows |
| `count` | `where level=="ERROR" \| count` | count rows |

### Expressions (`where`)

Comparisons `== != > < >= <=`, logic `and or not` (also `&& || !`), grouping `( )`,
and string matchers `contains`, `startswith`, `endswith`. String literals use
single or double quotes. Nested fields via dots: `where meta.region=="us"`.

### Aggregations (`summarize`)

`count()`, `sum(f)`, `avg(f)` (alias `mean`), `min(f)`, `max(f)`, `dcount(f)`
(distinct count). Give a column a name with `name=agg(...)`; otherwise the default
is `count` or `<fn>_<field>` (e.g. `avg_ms`), so you can filter on it downstream:

```bash
klog 'summarize avg(ms) by service | where avg_ms > 100' app.log
```

### Output

`-o table` (default), `-o json`, or `-o tsv`. Flags may appear before or after the
query and files.

## Examples

```bash
# slowest requests, projected
klog 'where ms > 300 | project ts, service, ms | sort by ms desc' app.log

# error rate per service as JSON
klog 'summarize errors=count() by service' <(grep ERROR app.log) -o json

# tail journald as NDJSON and watch a service
journalctl -o json -f | klog 'where _SYSTEMD_UNIT=="nginx.service" | project MESSAGE'
```

## Develop

```bash
make test    # unit tests
make build   # ./bin/klog
make vet
```

## License

MIT
