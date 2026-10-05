# Common tasks. Run inside `nix develop ./nix`.

default:
    @just --list

build:
    go build ./...

test:
    go test ./...

lint:
    go vet ./... && golangci-lint run ./... && buf lint

proto:
    buf generate

check-nix:
    nix flake check ./nix

# --- observability (docs/workstreams/10-observability.md) ------------------

# Alert rules, their promtool tests, the dashboards and their queries.
obs-check *ARGS:
    ops/check.sh {{ARGS}}

# The §5 event list against the source: every event, with its call sites, and
# a failure for any event a built component does not emit.
obs-events:
    go test ./internal/obs -run TestEventsEmitted -v

# Rewrite ops/dashboards/*.json from ops/dashboards/gen.py.
dashboards:
    python3 ops/dashboards/gen.py

# Loki, Prometheus, Grafana and Postgres locally, with plausible metrics and
# a database full of synthetic samples. Grafana answers on this machine's
# Tailscale address, port 3000 (folder "repose").
obs-dev:
    docker compose -f ops/dev/docker-compose.yml up -d
    @echo "grafana:    http://$(tailscale ip -4 | head -1):3000"
    @echo "prometheus: http://$(tailscale ip -4 | head -1):9090"
    @echo "metrics:    go run ./ops/dev/seedmetrics"
    @echo "logs:       ops/dev/seedlogs.sh"
    @echo "postgres:   ops/dev/pgcheck.sh"

obs-dev-down:
    docker compose -f ops/dev/docker-compose.yml down -v

# The greps docs/CHECKLIST.md asks for before calling anything done.
done-check paths="cmd internal":
    @echo "-- unhandled errors / panics --"; rg -n '_ = err|panic\(' {{paths}} || true
    @echo "-- leftovers --"; rg -n 'TODO|FIXME|XXX|not implemented' {{paths}} || true
    @echo "-- help nobody asked for (DECISIONS I-484, I-485) --"
    go test ./internal/cli -run 'TestSuccessOutputNamesNoCommand|TestCLIReassures' -count=1
    cd apps/web && pnpm exec vitest run src/lib/docs.test.ts src/lib/copy.test.ts

# --- dashboard -------------------------------------------------------------

# The dashboard's Vite dev server against the live api and Logto, behind
# `tailscale serve` so it has HTTPS on this machine's ts.net name: Logto's
# PKCE needs a secure context, which a plain http://100.x address is not
# (DECISIONS I-216). Sign in with GitHub as on the site; every button acts
# on that real account.
web:
    #!/usr/bin/env bash
    set -euo pipefail
    cd apps/web
    [ -e .env ] || cp .env.example .env
    [ -d node_modules ] || pnpm install
    tailscale serve --bg --https=443 http://127.0.0.1:5173 >/dev/null
    host=$(tailscale status --json | python3 -c 'import json,sys; print(json.load(sys.stdin)["Self"]["DNSName"].rstrip("."))')
    echo "Dashboard: https://$host/"
    pnpm exec vite dev --host 0.0.0.0 --port 5173 --strictPort
