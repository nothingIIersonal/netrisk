import csv, math, random, pathlib
from datetime import datetime, timedelta
out = pathlib.Path(__file__).resolve().parent / 'historical_metrics.csv'
start = datetime(2026,1,1)
rows=[]
for i in range(60*24*30):
    ts = start + timedelta(minutes=i)
    cyc_d = math.sin(2*math.pi*(i%1440)/1440.0)
    cyc_h = math.sin(2*math.pi*(i%60)/60.0)
    anomaly = 1 if (i % 911 in range(700, 718) or i % 541 in range(400, 412) or i % 3001 in range(2500,2522)) else 0
    hard_down = 1 if anomaly and i % 47 == 0 else 0
    rows.append([
        ts.isoformat(),
        24 + 12*(1+cyc_d)/2 + 3*cyc_h + random.gauss(0,2) + (18 if anomaly else 0),
        0.45 + 0.11*(1+cyc_d)/2 + random.gauss(0,0.02) + (0.12 if anomaly else 0),
        40 + 6*(1+cyc_d)/2 + random.gauss(0,0.9) + (7 if anomaly else 0),
        5_000_000 + 1_600_000*(1+cyc_d)/2 + random.gauss(0,220_000) + (3_000_000 if anomaly else 0),
        4_700_000 + 1_300_000*(1+cyc_d)/2 + random.gauss(0,220_000) + (2_600_000 if anomaly else 0),
        abs(random.gauss(0.02,0.01)) + (1.6 if anomaly else 0),
        abs(random.gauss(0.01,0.005)) + (0.8 if anomaly else 0),
        0.25 if anomaly and i % 7 == 0 else 0.0,
        1.0 if hard_down else 0.0,
        anomaly
    ])
with out.open('w', newline='', encoding='utf-8') as f:
    w=csv.writer(f)
    w.writerow(['timestamp','cpu_load','mem_util','temp_celsius','traffic_in_rate','traffic_out_rate','if_error_rate','if_discard_rate','if_down_ratio','reboot_indicator','label'])
    w.writerows(rows)
print(out)
