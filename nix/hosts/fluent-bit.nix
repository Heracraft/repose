# Fluent Bit on a host: journald for the host's own units and a tail of every
# guest's console log, shipped to the Loki on the owner's personal server over
# WireGuard, with the labels docs/ops/OBSERVABILITY.md documents (`host`,
# `component`, and `guest_id` for console lines).
#
# docs/workstreams/10-observability.md §2 names this file. §6 says what
# happens when Loki is unreachable: chunks buffer to disk up to 1 GB and the
# shipper retries forever, so a Loki outage costs log lines only if it lasts
# past the buffer. That failure is visible because Fluent Bit's own metrics
# are scraped (`repose` has no metric of its own for a shipper it does not
# write); the FluentBitStuck rule in ops/alerts.yaml is the alert.
#
# Nothing here reads a guest's filesystem. A console log is what the guest
# wrote to its serial device and hostd captured; it can contain anything a
# tenant printed, which is why Loki keeps console streams 30 days and
# component streams 90 (ops/loki/retention.yaml).
{ config, lib, pkgs, ... }:
let
  cfg = config.repose.host;
  hostEnv = "/run/repose/host.env";

  # component: the unit name without .service for host units, `console` for
  # guest console logs, plus guest_id parsed from the tail path.
  labelsLua = pkgs.writeText "repose-labels.lua" ''
    function host_unit(tag, ts, record)
      local unit = record["_SYSTEMD_UNIT"] or record["SYSLOG_IDENTIFIER"] or "kernel"
      unit = string.gsub(unit, "%.service$", "")
      -- A templated unit's instance is an id (guest@<guest id>,
      -- virtiofsd@<guest id>) and a login scope is numbered
      -- (session-42.scope): one label value per guest or login would be
      -- a stream per id, so the instance moves to guest_id and the
      -- number is dropped.
      local base, inst = string.match(unit, "^([^@]+)@(.+)$")
      if base ~= nil then
        unit = base
        if string.match(inst, "^%x+%-%x+%-%x+%-%x+%-%x+$") then record["guest_id"] = inst end
      end
      unit = string.gsub(unit, "^session%-%d+%.scope$", "session")
      record["component"] = unit
      record["unit"] = unit
      record["service_name"] = unit
      return 2, ts, record
    end

    function console(tag, ts, record)
      local path = record["path"] or ""
      local id = string.match(path, "/guests/([^/]+)/console%.log")
      record["component"] = "console"
      record["service_name"] = "console"
      if id ~= nil then record["guest_id"] = id end
      record["path"] = nil
      return 2, ts, record
    end
  '';
in
{
  options.repose.host.observability = {
    fluentBitMetricsPort = lib.mkOption {
      type = lib.types.port;
      default = 2021;
      description = ''
        Port Fluent Bit serves its own Prometheus metrics on, bound to the
        WireGuard address. Prometheus scrapes
        `/api/v1/metrics/prometheus` there for
        `fluentbit_output_retries_failed_total`, which is the only way to
        see that a host has stopped shipping logs
        (docs/workstreams/10-observability.md §6).
      '';
    };
    logLevel = lib.mkOption {
      type = lib.types.enum [ "error" "warn" "info" "debug" "trace" ];
      default = "info";
      description = "Fluent Bit's own log level, in the host journal.";
    };
    bufferLimit = lib.mkOption {
      type = lib.types.str;
      default = "1G";
      description = ''
        Disk buffer for the host journal's output while Loki is unreachable
        (docs/workstreams/10-observability.md §6). Filling it drops the
        oldest chunks; it does not fill the data disk, which is the tenants'.
      '';
    };
    consoleBufferLimit = lib.mkOption {
      type = lib.types.str;
      default = "1G";
      description = ''
        Disk buffer for the guests' console output, separate from the host
        journal's so a guest's console cannot evict host lines (DECISIONS
        I-452).
      '';
    };
  };

  config = {
    services.fluent-bit = {
      enable = true;
      settings = {
        service = {
          flush = 5;
          grace = 10;
          log_level = cfg.observability.logLevel;
          "storage.path" = "/var/lib/fluent-bit/storage";
          "storage.sync" = "normal";
          "storage.max_chunks_up" = 64;
          "storage.backlog.mem_limit" = "64M";
          # Its own metrics, on WireGuard only like every other exporter.
          http_server = "on";
          http_listen = "\${WG_ADDR}";
          http_port = cfg.observability.fluentBitMetricsPort;
        };
        pipeline = {
          inputs = [
            {
              name = "systemd";
              tag = "host.*";
              read_from_tail = "on";
              strip_underscores = "off";
              db = "/var/lib/fluent-bit/journald.db";
              "storage.type" = "filesystem";
            }
            {
              name = "tail";
              tag = "console.*";
              path = "/var/lib/repose/guests/*/console.log";
              path_key = "path";
              db = "/var/lib/fluent-bit/console.db";
              # A console log is wanted from its first line; the db keeps the
              # offset so nothing is shipped twice.
              read_from_head = "on";
              refresh_interval = 5;
              rotate_wait = 10;
              skip_long_lines = "on";
              "storage.type" = "filesystem";
            }
          ];
          filters = [
            {
              name = "lua";
              match = "host.*";
              script = "${labelsLua}";
              call = "host_unit";
            }
            {
              name = "lua";
              match = "console.*";
              script = "${labelsLua}";
              call = "console";
            }
            {
              # Keep the journal fields that carry diagnostic value; drop the
              # rest (cmdline, uid, and everything else the never-log list
              # forbids or that is noise as a JSON line).
              name = "record_modifier";
              match = "host.*";
              allowlist_key = [ "MESSAGE" "PRIORITY" "component" "unit" "_PID" "SYSLOG_IDENTIFIER" ];
            }
          ];
          # Two outputs with a buffer each (DECISIONS I-452): while Loki is
          # unreachable, guests' console lines fill only their own buffer
          # and can never drop the host journal's oldest chunks. Each
          # guest's console is rate-limited by hostd before it is written.
          outputs = map (o: {
            name = "loki";
            host = "\${LOKI_HOST}";
            port = "\${LOKI_PORT}";
            labels = "host=\${HOST_ID}";
            label_keys = "$component,$service_name,$guest_id";
            line_format = "json";
            drop_single_key = "off";
            retry_limit = "no_limits";
          } // o) [
            { match = "host.*"; "storage.total_limit_size" = cfg.observability.bufferLimit; }
            { match = "console.*"; "storage.total_limit_size" = cfg.observability.consoleBufferLimit; }
          ];
        };
      };
    };

    systemd.services.fluent-bit = {
      # Two conditions, not one. host.env exists as soon as the host has
      # registered, but LOKI_HOST in it is empty until the api has a Loki
      # recorded (`repose-admin edge loki`, DECISIONS I-95); starting with
      # an empty output host makes Fluent Bit retry a connection to
      # nothing for ever, which looks in the journal exactly like a Loki
      # that is down. `ConditionEnvironment` is not a thing, so the check
      # is a one-line ExecCondition reading the same file the unit does.
      unitConfig.ConditionPathExists = hostEnv;
      after = [ "repose-host-net.service" "wg-quick-wg0.service" ];
      partOf = [ "repose-host-net.service" ];
      serviceConfig = {
        ExecCondition = pkgs.writeShellScript "repose-fluent-bit-has-loki" ''
          . ${hostEnv}
          if [ -z "''${LOKI_HOST:-}" ]; then
            echo "no LOKI_HOST in ${hostEnv}: nothing to ship to; see repose-admin edge loki" >&2
            exit 1
          fi
        '';
        EnvironmentFile = hostEnv;
        StateDirectory = "fluent-bit";
        # Console logs are written by hostd (root) under a 0700 directory;
        # read-only search is all the shipper needs.
        AmbientCapabilities = [ "CAP_DAC_READ_SEARCH" ];
        CapabilityBoundingSet = [ "CAP_DAC_READ_SEARCH" ];
      };
    };
  };
}
