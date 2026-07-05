#!/usr/bin/env python3
"""Generate realistic dummy NDJSON logs for klog demos and benchmarks.

Usage:
  python3 sample/gen_logs.py                 # 5000 rows -> sample/app.log
  python3 sample/gen_logs.py 1000000 big.log # N rows   -> big.log
"""
import json, random, sys, datetime as dt

random.seed(42)
N = int(sys.argv[1]) if len(sys.argv) > 1 else 5000
OUT = sys.argv[2] if len(sys.argv) > 2 else "sample/app.log"
services = ["auth","api","payments","search","cache","notifications"]
teams = {
    "auth":"identity","api":"gateway","payments":"billing",
    "search":"discovery","cache":"platform","notifications":"comms",
}
tiers = {"auth":1,"api":1,"payments":1,"search":2,"cache":2,"notifications":3}
regions = ["us-east","us-west","eu-west","ap-south"]
levels = ["INFO","INFO","INFO","INFO","WARN","WARN","ERROR"]
routes = ["/login","/checkout","/search","/items","/health","/notify","/refresh"]
users = [f"user{n:03d}" for n in range(1,41)]

start = dt.datetime(2026,7,2,9,0,0, tzinfo=dt.timezone.utc)

def latency(level, svc):
    base = {"payments":180,"search":120,"api":60,"auth":40,"cache":8,"notifications":90}[svc]
    j = random.expovariate(1/base)
    if level=="ERROR": j *= random.uniform(2,6)
    if level=="WARN":  j *= random.uniform(1.3,2.5)
    return round(base*0.4 + j, 1)

with open(OUT,"w") as f:
    t = start
    for i in range(N):
        t = t + dt.timedelta(milliseconds=random.randint(200, 1400))
        svc = random.choice(services)
        lvl = random.choice(levels)
        # cache rarely errors, payments errors a bit more
        if svc=="cache" and lvl=="ERROR" and random.random()<0.7: lvl="INFO"
        if svc=="payments" and random.random()<0.15: lvl="ERROR"
        rec = {
            "ts": t.isoformat().replace("+00:00","Z"),
            "level": lvl,
            "service": svc,
            "route": random.choice(routes),
            "status": random.choice([200,200,200,201,204,400,404,500,503]) if lvl!="INFO" else random.choice([200,200,201,204]),
            "ms": latency(lvl, svc),
            "user": random.choice(users),
            "bytes": random.randint(120, 48000),
            "meta": {"region": random.choice(regions), "host": f"{svc}-{random.randint(1,8):02d}"},
            "tags": random.sample(["retry","cold","canary","cached","slow","auth","p1"], k=random.randint(0,3)),
            "msg": f"user={random.choice(users)} ip=10.{random.randint(0,3)}.{random.randint(0,255)}.{random.randint(1,254)} route={random.choice(routes)}",
        }
        f.write(json.dumps(rec)+"\n")
    # a couple of non-JSON lines to prove _raw handling
    f.write("==== log rotated ====\n")

# dimension table for join/lookup (only for the default sample)
if OUT == "sample/app.log":
    with open("sample/services.log","w") as f:
        for svc in services:
            f.write(json.dumps({"service":svc,"team":teams[svc],"tier":tiers[svc],"oncall":f"{teams[svc]}-oncall"})+"\n")

print(f"wrote {N} rows to {OUT}")

