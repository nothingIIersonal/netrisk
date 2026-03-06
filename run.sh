#!/bin/bash

go mod tidy

go run . \
  -target 192.168.2.104 -port 161 -v 2c -community public \
  -listen 127.0.0.1:8080 \
  -health-interval 250ms -full-interval 5s \
  -timeout 800ms -retries 0 \
  -down-after 2 -down-hold 2200ms \
  -up-after 3 -up-hold 2200ms \
  -stale-after 12s \
  -state-file data/state.json -snapshot-file data/last_snapshot.json \
  -events 200 -save-every 10s \
  -force-down-risk=true \
  -forecast-horizon 5m -forecast-alpha 3.0 -lambda-prior 0.0000116 \
  -min-samples 30 \
  -ewma-alpha 0.15 -ewma-var-alpha 0.10 -zscale 3.0 \
  -hw-alpha 0.20 -hw-beta 0.05 -hw-gamma 0.10 -hw-season 12 \
  -thr-cpu 90 -thr-linkdown 0.30 -thr-err 5 -thr-disc 5
