#!/bin/sh
# Start plugin sidecar in background (SDK registration + health endpoint)
infra-redis-plugin &
# Redis as PID 1 — if it dies, container exits, kernel auto-restarts
exec redis-server --dir /data --appendonly yes --save ""
