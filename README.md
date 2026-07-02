# klog

**A KQL-lite query engine for JSON/NDJSON logs.** Write [Kusto/KQL](https://learn.microsoft.com/azure/data-explorer/kusto/query/)-style
pipelines against local log files instead of gluing together `jq`, `grep`, `sort`
and `awk`. Single static Go binary, zero dependencies.

```bash
klog 'where level=="ERROR" | summarize n=count() by service | sort by n desc' app.log
```
```
service  n
-------  -
api      2
auth     1
```

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

Input is NDJSON (one JSON object per line). Blank lines are skipped; lines that
aren't valid JSON are still queryable via `_line` (number) and `_raw` (text).
Output: `-o table` (default), `-o json`, `-o tsv`. Flags may appear before or
after the query and files.

## Tabular operators

| Operator | Example | Notes |
|----------|---------|-------|
| `where` / `filter` | `where level=="ERROR" and ms > 500` | keep matching rows |
| `extend` | `extend sec = ms/1000.0, slow = ms > 500` | add computed columns |
| `project` / `select` | `project ts, svc=service, ms*1.0` | pick / rename / compute |
| `project-away` | `project-away _raw, _line` | drop columns (supports `col*`) |
| `project-keep` | `project-keep ts, level*` | keep only matching columns |
| `project-rename` | `project-rename svc=service` | rename in place |
| `project-reorder` | `project-reorder ts, level` | change column order |
| `summarize` / `stats` | `summarize count() by service` | group + aggregate |
| `count` | `... \| count` | row count |
| `distinct` | `distinct service, level` | unique combinations (`distinct *`) |
| `sort` / `order` | `sort by ms desc, ts asc` | order (default **desc**, like KQL) |
| `top` | `top 10 by ms` | largest N by expression |
| `take` / `limit` | `take 20` | first N rows |
| `sample` | `sample 100` | random N rows |
| `sample-distinct` | `sample-distinct 5 of user` | up to N distinct values |
| `serialize` | `serialize rn = row_number()` | add a sequence column |
| `parse` | `parse msg with "user=" user " ip=" ip` | extract fields from a string |
| `mv-expand` | `mv-expand tags` | one row per array element |
| `getschema` | `getschema` | inferred column names/types |
| `print` | `print x = 2+3, now()` | constant row, no input |
| `union` | `union other.log` | append rows from files/subqueries |
| `join` | `join kind=inner (dims.log) on service` | relational join |
| `lookup` | `lookup (dims.log) on service` | dimension enrichment (left) |

`join` kinds: `inner`, `leftouter`, `rightouter`, `fullouter`, `leftsemi`,
`rightsemi`, `leftanti`, `rightanti`. Right-side sources are NDJSON files, or a
subquery: `join (dims.log | where active==true | project service, team) on service`.

## Expressions

Used by `where`, `extend`, `project`, `summarize` args/keys, `top`, `sort`, `print`.

- **Arithmetic**: `+ - * / %`
- **Comparison**: `== != > < >= <=`, `=~`/`!~` (case-insensitive equals)
- **Logic**: `and or not` (also `&& || !`), grouping `( )`
- **String**: `contains`, `startswith`, `endswith` (case-insensitive), `has`
  (whole-term), `matches regex "…"`
- **Sets/ranges**: `x in ("a","b")`, `x between (lo .. hi)`
- **Nested fields**: `meta.region`, array indexing `tags[0]`
- **Literals**: numbers, strings, `true`/`false`/`null`, timespans `1d 2h 30m 45s 100ms`

### Datetime & timespan

Timestamps in logs are strings; convert with `todatetime(ts)`. Timespan literals
and arithmetic work naturally:

```bash
klog 'where todatetime(ts) > ago(15m) | summarize count() by bin(todatetime(ts), 1m)' app.log
```

`datetime − datetime → timespan`, `datetime ± timespan → datetime`,
`timespan ± timespan`, `timespan * number`.

## Aggregations (`summarize`)

`count()`, `countif(pred)`, `sum(x)`, `sumif(x,pred)`, `avg(x)`, `avgif(x,pred)`,
`min(x)`, `max(x)`, `dcount(x)`, `make_list(x)`, `make_set(x)`, `any(x)`,
`percentile(x, p)`, `stdev(x)`, `stdevp(x)`, `variance(x)`, `varp(x)`,
`arg_max(x, cols…|*)`, `arg_min(x, cols…|*)`.

Name a result with `name=agg(...)`; otherwise the default is `count` or
`<fn>_<field>` (e.g. `avg_ms`) so you can filter it downstream. To apply a scalar
function to an aggregate (e.g. rounding), use a following `extend`:

```bash
klog 'summarize avg(ms) by service | extend avg_ms = round(avg_ms, 1)' app.log
```

## Scalar functions

**String**: `strlen`, `strcat`, `strcat_delim`, `substring`, `split`, `replace`,
`tolower`, `toupper`, `trim`/`trim_start`/`trim_end`, `indexof`, `reverse`,
`extract`, `isempty`, `isnotempty`.
**Type**: `toint`, `tolong`, `todouble`, `tostring`, `tobool`, `todatetime`,
`totimespan`, `gettype`.
**Conditional**: `iff`/`iif`, `case`, `coalesce`, `isnull`, `isnotnull`,
`isnan`, `isfinite`.
**Math**: `abs`, `round`, `floor`, `ceiling`, `sqrt`, `exp`, `log`, `log10`,
`log2`, `sign`, `pow`, `min_of`, `max_of`, `bin`.
**Datetime**: `now`, `ago`, `bin`, `getyear`, `getmonth`, `dayofmonth`,
`dayofweek`, `dayofyear`, `hourofday`, `startofday`/`startofhour`/`startofminute`,
`endofday`, `datetime_part`, `datetime_diff`, `datetime_add`, `format_datetime`.
**Dynamic**: `array_length`, `pack_array`.

## Examples

```bash
# error budget: p95 latency per service, slow ones only
klog 'summarize p95=percentile(ms,95) by service | where p95 > 200 | sort by p95' app.log

# enrich with a dimension table, then roll up
klog 'lookup (teams.log) on service | summarize reqs=count() by team' app.log

# parse an unstructured message into fields
klog 'parse msg with "user=" user " ip=" ip | distinct user, ip' app.log

# tail journald and watch one unit
journalctl -o json -f | klog 'where _SYSTEMD_UNIT=="nginx.service" | project MESSAGE'
```

## Not (yet) supported

Operators tied to the ADX service rather than local files: chart `render`,
`make-series`, `evaluate <plugin>`, geospatial/ML functions, and cluster/database
management. Contributions welcome.

## Develop

```bash
make test    # unit tests
make build   # ./bin/klog
make vet
```

## License

MIT
