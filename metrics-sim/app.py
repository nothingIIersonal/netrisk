from fastapi import FastAPI
from fastapi.responses import PlainTextResponse, JSONResponse
from prometheus_client import CollectorRegistry, Gauge, generate_latest
import time
import math
import random
import numpy as np

app = FastAPI(title="Metrics simulator (SNMP-like)")
START = time.time()
INTERFACES = [f"eth{i}" for i in range(1, 9)]
STATE = {iface: {
    "in_octets": random.randint(10_000_000, 50_000_000),
    "out_octets": random.randint(10_000_000, 50_000_000),
    "in_errors": 0,
    "out_errors": 0,
    "in_discards": 0,
    "out_discards": 0,
    "oper_status": 1,
} for iface in INTERFACES}


@app.get('/health')
async def health():
    return {"status": "ok", "interfaces": INTERFACES}


def update_state():
    now = time.time()
    t = now - START
    cyc_h = math.sin(2*math.pi*(t % 3600)/3600.0)
    cyc_d = math.sin(2*math.pi*(t % 86400)/86400.0)
    anomaly = (int(t)//240) % 11 in (7, 8)
    hard_down = anomaly and random.random() < 0.15
    cpu = max(1, min(99, 22 + 15*(1+cyc_h)/2 + 5*(1+cyc_d)/2 +
              np.random.normal(0, 2) + (20 if anomaly else 0)))
    mem_used = 900_000 + 350_000 * \
        (1+cyc_h)/2 + np.random.normal(0, 25_000) + (180_000 if anomaly else 0)
    mem_total = 2_000_000
    temp = 39 + 7*(1+cyc_h)/2 + np.random.normal(0, 1.0) + \
        (7 if anomaly else 0)
    up = 0.0 if hard_down else 1.0

    for i, iface in enumerate(INTERFACES, start=1):
        s = STATE[iface]
        base = 700_000 + 220_000*math.sin(2*math.pi*(t + i*17)/600.0)
        if anomaly and i in (2, 3, 6):
            base *= 2.4
        s['in_octets'] += max(1_000, int(base + np.random.normal(0, 50_000)))
        s['out_octets'] += max(1_000, int(base*0.9 +
                               np.random.normal(0, 50_000)))
        s['in_errors'] += int(np.random.poisson(
            0.03 if not anomaly else (1.4 if i in (2, 3) else 0.2)))
        s['out_errors'] += int(np.random.poisson(
            0.02 if not anomaly else (1.0 if i in (2, 3) else 0.1)))
        s['in_discards'] += int(np.random.poisson(
            0.01 if not anomaly else (0.8 if i in (3, 6) else 0.08)))
        s['oper_status'] = 2 if hard_down else (
            2 if anomaly and i in (3, 6) and random.random() < 0.04 else 1)

    return {
        "cpu": cpu,
        "mem_used": mem_used,
        "mem_total": mem_total,
        "temp": temp,
        "up": up,
        "anomaly": int(anomaly),
        "uptime_ticks": int((now-START)*100) if up else 0,
    }


@app.get('/metrics')
async def metrics():
    reg = CollectorRegistry()
    g_up = Gauge('up', 'Target availability', registry=reg)
    g_sysuptime = Gauge('sysUpTime', 'System uptime timeticks', registry=reg)
    g_cpu = Gauge('hrProcessorLoad',
                  'Host Resources Processor Load', registry=reg)
    g_mem_total = Gauge('hrMemorySize', 'Host total memory', registry=reg)
    g_mem_used = Gauge('hrStorageUsed', 'Host used memory/storage',
                       ['hrStorageDescr'], registry=reg)
    g_mem_size = Gauge(
        'hrStorageSize', 'Host total storage/memory size', ['hrStorageDescr'], registry=reg)
    g_temp = Gauge('entPhySensorValue', 'Entity sensor value',
                   ['sensor'], registry=reg)

    labels = ['ifName']
    if_in_octets = Gauge('ifHCInOctets', 'Inbound octets',
                         labels, registry=reg)
    if_out_octets = Gauge(
        'ifHCOutOctets', 'Outbound octets', labels, registry=reg)
    if_in_err = Gauge('ifInErrors', 'Inbound errors', labels, registry=reg)
    if_out_err = Gauge('ifOutErrors', 'Outbound errors', labels, registry=reg)
    if_in_disc = Gauge('ifInDiscards', 'Inbound discards',
                       labels, registry=reg)
    if_oper = Gauge('ifOperStatus', 'Operational status', labels, registry=reg)

    cur = update_state()
    g_up.set(cur['up'])
    g_sysuptime.set(cur['uptime_ticks'])
    g_cpu.set(cur['cpu'])
    g_mem_total.set(cur['mem_total'])
    g_mem_used.labels('memory').set(cur['mem_used'])
    g_mem_size.labels('memory').set(cur['mem_total'])
    g_temp.labels('chassis_temp').set(cur['temp'])

    for iface in INTERFACES:
        s = STATE[iface]
        if_in_octets.labels(iface).set(s['in_octets'])
        if_out_octets.labels(iface).set(s['out_octets'])
        if_in_err.labels(iface).set(s['in_errors'])
        if_out_err.labels(iface).set(s['out_errors'])
        if_in_disc.labels(iface).set(s['in_discards'])
        if_oper.labels(iface).set(s['oper_status'])

    return PlainTextResponse(generate_latest(reg).decode('utf-8'))

if __name__ == '__main__':
    import uvicorn
    uvicorn.run(app, host='0.0.0.0', port=9117)
