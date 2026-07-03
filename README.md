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
Output: `-o table` (default), `-o json`, `-o ndjson`, `-o tsv`. Flags may appear before or
after the query and files.

### Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-o`, `--output` | `table` | output format: `table`, `json`, `ndjson`, `tsv` |
| `-i`, `--input` | `json` | input format: `json`, `auto`, `logfmt`, `csv`, `tsv`, `regex`, `raw` |
| `--pattern` | | regex with named groups for `--input regex` |
| `--from` | | keep rows with time-field `>=` this bound |
| `--to` | | keep rows with time-field `<` this bound |
| `--time-field` | `ts` | field used by `--from`/`--to` |
| `--color` | `auto` | colorize the level column: `auto`, `always`, `never` |

`--from`/`--to` accept an absolute datetime (`2026-07-02T09:30:00Z`) or a
relative offset from now (`-1h`, `-15m`, `+30m`, or `now`). They apply a
`todatetime(<time-field>)` range filter before the rest of the pipeline:

```bash
klog --from -1h --to now 'summarize count() by service' app.log
klog --from '2026-07-02T09:00:00Z' --to '2026-07-02T09:05:00Z' 'count' app.log
klog --time-field Timestamp --from -30m 'where level=="ERROR"' app.log
```

`--color auto` (the default) colorizes a `level`/`severity` column when stdout is
a terminal (respecting `NO_COLOR`): ERROR red, FATAL bold-red, WARN yellow, INFO
green, DEBUG gray, TRACE cyan. JSON/TSV output is never colorized.

## Input formats

klog reads NDJSON by default, but `-i/--input` handles other log shapes. JSON with
**varying schemas** already works — columns are the union of keys across rows.
Lines that don't parse are still queryable via `_raw` and `_line`. Text formats
auto-upgrade obvious numbers/bools so `where ms > 500` and `summarize avg(ms)`
work without casts.

| `--input` | Parses | Notes |
|-----------|--------|-------|
| `json` (default) | one JSON object per line | non-JSON lines → `_raw` |
| `auto` | JSON **or** logfmt **or** plain text, per line | best for mixed files |
| `logfmt` | `key=value key2="quoted val"` | bare keys → `true`; keeps `_raw` |
| `csv` | comma-separated, first row = header | via `encoding/csv` |
| `tsv` | tab-separated, first row = header | |
| `regex` | one line, `--pattern` with `(?P<name>…)` groups | named groups → columns |
| `raw` | each line as `{_line, _raw}` | grep-style |

```bash
# logfmt
klog -i logfmt 'where level=="error" | summarize count() by service' app.logfmt

# CSV with a header row
klog -i csv 'summarize errs=countif(level=="ERROR") by service' app.csv

# nginx access log via regex named groups
klog -i regex --pattern '(?P<ip>\S+)\s+\S+\s+\S+\s+\[(?P<t>[^]]+)\]\s+"(?P<method>\S+)\s+(?P<path>\S+)[^"]*"\s+(?P<status>\d+)\s+(?P<bytes>\d+)' \
     'summarize hits=count(), errs=countif(status>=500) by path | sort by hits desc' access.log

# mixed JSON + logfmt + plain text in one file
klog -i auto 'summarize count() by level' mixed.log
```

The chosen `--input` also applies to `union`/`join`/`lookup` file sources.

## Context (surrounding rows)

Find matches, then see what happened around them — like `grep -C`, but line- or
time-based. Your query selects **anchor** rows; klog then pulls their neighbors
from the original log. Matches are marked with `>` in a leading `_m` column, and
distinct incidents are separated by `--`.

| Flag | Meaning |
|------|---------|
| `-C`, `--context N` | ± N lines around each match |
| `-A`, `--after N` | N lines after |
| `-B`, `--before N` | N lines before |
| `-T`, `--context-time D` | ± time window (`30s`, `2m`, `1h`) using `--time-field` |

```bash
# 3 lines either side of every ERROR
klog -C 3 'where level=="ERROR"' app.log

# everything within 2 minutes of each 500
klog -T 2m 'where status==500' app.log

# narrow the columns: emit ndjson, then project with a second klog
klog -T 30s 'where ms > 5000' app.log -o ndjson | klog 'project ts, level, service, ms, _m'
```

Context needs a **row-preserving** query (`where`, `sort`, `take`, `top`,
`sample`). Queries that collapse or reshape rows (`summarize`, `project`) can't
be anchored back to the log — use the `-o ndjson | klog '...'` trick above to
shape the output afterwards.

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
| `render` | `... \| render barchart` | terminal chart (bar/pie/line/time) |

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

## Charts

`render` draws terminal charts. Your query produces a table; `render` turns it
into bars or a line plot (Unicode, scaled to your terminal width).

| Chart | Rendering |
|-------|-----------|
| `render barchart` / `columnchart` | horizontal Unicode bars with values |
| `render piechart` | bars annotated with each slice's `%` |
| `render timechart` / `linechart` / `areachart` / `scatterchart` | ASCII line plot, multi-series with a legend |

By default the first column is the x-axis and every numeric column is a series.
Override via `with (...)`:

```bash
klog 'where level=="ERROR" | summarize errors=count() by service | sort by errors desc | render barchart' app.log
klog 'summarize n=count() by level | render piechart with (title="Level mix")' app.log
klog 'extend t=todatetime(ts) | summarize hits=count(), errors=countif(level=="ERROR") by bin(t,1m) | render timechart' app.log
```

`with` options: `title`, `xcolumn`, `ycolumns` (`;`-separated), `kind`. Charts
render in table mode; `-o json/ndjson/tsv` emit the underlying data instead.

## Not (yet) supported

Operators tied to the ADX service rather than local files: `make-series`,
`evaluate <plugin>`, geospatial/ML functions, and cluster/database management.
Contributions welcome.

## Develop

```bash
make test    # unit tests
make build   # ./bin/klog
make vet
```

## License

MIT
