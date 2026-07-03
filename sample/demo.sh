#!/usr/bin/env bash
# klog interactive demo - no copy-paste needed.
#
#   ./sample/demo.sh          # guided tour, press Enter between steps
#   ./sample/demo.sh -l       # list steps
#   ./sample/demo.sh 7        # run only step 7
#   ./sample/demo.sh -a       # run all steps without pausing
set -euo pipefail

cd "$(dirname "$0")/.."

BIN=./bin/klog
APP=sample/app.log
DIM=sample/services.log

bold=$'\e[1m'; dim=$'\e[2m'; cyan=$'\e[36m'; green=$'\e[32m'; reset=$'\e[0m'

if [[ ! -x "$BIN" ]]; then
  echo "${dim}building klog...${reset}"
  PATH="$PATH:/usr/bin" go build -o "$BIN" ./cmd/klog
fi
if [[ ! -f "$APP" || ! -f "$DIM" ]]; then
  echo "${dim}generating sample logs...${reset}"
  python3 sample/gen_logs.py
fi

titles=(
  "Filter + count all ERROR events"
  "Slowest payments requests (filter + project + take)"
  "Error rate & p95 latency per service"
  "Requests + MB per minute (time buckets via bin)"
  "Top 5 slowest requests overall"
  "Time-range window with --from/--to (absolute)"
  "Relative time window (--from -8h --to now)"
  "Colorized levels (run in a terminal to see colors)"
  "Join errors to team dimension, roll up"
  "Lookup: enrich slow payments with on-call"
  "Parse msg into fields, then distinct"
  "mv-expand tag arrays -> top tags"
  "has (whole-term) vs contains (substring)"
  "regex + in-list on status codes"
  "arg_max: slowest request per service"
  "getschema: inferred column types"
  "JSON output"
  "TSV output"
  "stdin pipe + round via extend"
  "Non-JSON lines survive as _raw"
  "logfmt input (-i logfmt)"
  "CSV input (-i csv)"
  "regex input: nginx access log (-i regex --pattern ...)"
  "auto input: mixed JSON + logfmt + text (-i auto)"
  "Context: +/- 2 lines around slowest ERROR (grep -C style)"
  "Context: +/- 3s time window around slowest request"
  "Chart: errors per service (render barchart)"
  "Chart: log level mix (render piechart)"
  "Chart: requests per minute (render timechart)"
  "Chart: latency histogram (render histogram)"
  "Chart: errors per service, vertical (render columnchart)"
)

# Format per entry: "FLAGS>>>QUERY"  (FLAGS may be empty; STDIN as flags = pipe)
specs=(
  '>>>where level=="ERROR" | count'
  '>>>where service=="payments" and ms > 1000 | project ts, route, ms | take 10'
  '>>>summarize reqs=count(), errs=countif(level=="ERROR"), p95=percentile(ms,95) by service | extend err_pct=round(100.0*errs/reqs,2), p95=round(p95,1) | project service, reqs, err_pct, p95 | sort by err_pct desc'
  '>>>extend t=todatetime(ts) | summarize hits=count(), mb=sum(bytes) by minute=bin(t,1m) | extend mb=round(mb/1048576.0,2) | sort by minute asc | take 8'
  '>>>top 5 by ms | project ts, service, route, ms, level'
  '--from 2026-07-02T09:30:00Z --to 2026-07-02T09:35:00Z>>>summarize count() by service | sort by count desc'
  '--from -8h --to now>>>count'
  '--color always>>>where status >= 500 | project ts, level, service, ms | take 8'
  '>>>where level=="ERROR" | join kind=inner (sample/services.log) on service | summarize errors=count() by team, tier | sort by errors desc'
  '>>>where service=="payments" and ms > 1500 | lookup (sample/services.log) on service | project ts, ms, oncall | sort by ms desc | take 5'
  '>>>parse msg with "user=" u " ip=" ip " route=" r | distinct ip, r | take 10'
  '>>>mv-expand tags | where isnotempty(tags) | summarize n=count() by tags | sort by n desc'
  '>>>where msg has "route" | count'
  '>>>where route matches regex "^/(checkout|login)$" and status in (500,503) | summarize count() by route, status | sort by count desc'
  '>>>summarize arg_max(ms, ts, route, user) by service | sort by ms desc'
  '>>>getschema'
  '-o json>>>summarize n=count() by level'
  '-o tsv>>>summarize n=count() by service | sort by n desc'
  'STDIN>>>where service=="cache" | summarize avg_ms=avg(ms) | extend avg_ms=round(avg_ms,2)'
  '>>>where isnotempty(_raw) | project _line, _raw'
  '-i logfmt>>>where level=="error" | summarize n=count(), avg_ms=avg(ms) by service>>>sample/app.logfmt'
  '-i csv>>>summarize errs=countif(level=="ERROR"), maxms=max(ms) by service | sort by maxms desc>>>sample/app.csv'
  '-i regex --pattern (?P<ip>\S+)\s+\S+\s+\S+\s+\[(?P<t>[^]]+)\]\s+"(?P<method>\S+)\s+(?P<path>\S+)[^"]*"\s+(?P<status>\d+)\s+(?P<bytes>\d+)>>>summarize hits=count(), errs=countif(status>=500), bytes=sum(bytes) by path | sort by hits desc>>>sample/access.txt'
  '-i auto>>>summarize n=count() by level>>>sample/mixed.log.txt'
  '-C 2>>>where level=="ERROR" and ms > 5000'
  '-T 3s>>>where ms > 5000'
  '>>>where level=="ERROR" | summarize errors=count() by service | sort by errors desc | render barchart'
  '>>>where isnotempty(level) | summarize n=count() by level | render piechart with (title="Level mix")'
  '>>>extend t=todatetime(ts) | where isnotempty(ts) | summarize hits=count() by minute=bin(t,1m) | sort by minute asc | render timechart'
  '>>>where service=="payments" | project ms | render histogram with (title="Payments latency", bins=10)'
  '>>>where level=="ERROR" | summarize errors=count() by service | sort by errors desc | render columnchart'
)

run_step() {
  local i=$1
  local spec="${specs[$i]}"
  local flags="${spec%%>>>*}"
  local rest="${spec#*>>>}"
  local q="${rest%%>>>*}"
  local file="$APP"
  [[ "$rest" == *">>>"* ]] && file="${rest##*>>>}"
  printf '\n%s[%2d] %s%s\n' "$bold" "$((i+1))" "${titles[$i]}" "$reset"
  if [[ "$flags" == "STDIN" ]]; then
    printf '%s  cat %s | klog '\''%s'\''%s\n' "$cyan" "$file" "$q" "$reset"
    "$BIN" "$q" < "$file"
  else
    printf '%s  klog %s '\''%s'\'' %s%s\n' "$cyan" "$flags" "$q" "$file" "$reset"
    # shellcheck disable=SC2086
    "$BIN" $flags "$q" "$file"
  fi
}

if [[ "${1:-}" == "-l" || "${1:-}" == "--list" ]]; then
  for i in "${!titles[@]}"; do printf '%s%2d%s  %s\n' "$green" "$((i+1))" "$reset" "${titles[$i]}"; done
  exit 0
fi
if [[ "${1:-}" =~ ^[0-9]+$ ]]; then
  idx=$(( $1 - 1 ))
  (( idx >= 0 && idx < ${#titles[@]} )) || { echo "no such step: $1"; exit 1; }
  run_step "$idx"; exit 0
fi

nopause=0
[[ "${1:-}" == "-a" || "${1:-}" == "--all" ]] && nopause=1

echo "${bold}klog demo${reset} - ${#titles[@]} steps. ${dim}Press Enter to advance, Ctrl-C to quit.${reset}"
for i in "${!titles[@]}"; do
  run_step "$i"
  if (( nopause == 0 && i < ${#titles[@]}-1 )); then
    read -r -p "$(printf '%s  -- Enter for next --%s' "$dim" "$reset")" _ </dev/tty || break
  fi
done
printf '\n%sDone.%s Re-run one step:  ./sample/demo.sh <N>   (list: -l)\n' "$green" "$reset"
