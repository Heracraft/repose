# Pull-through caches for the npm registry and Docker Hub, one set per host
# (DECISIONS I-202, I-208; docs/interfaces/host-conventions.md "Caches").
#
# Guests reach both at one fixed address, ${address} (the "host services
# address"), the same on every host, so the guest base can name it without
# knowing which host it runs on. It is a /32 on br-guests; a guest routes it
# through its default gateway (this host) and the input chain admits exactly
# the two ports from br-guests. Nothing else can reach them: the input chain
# drops them on wg0 and the provider NIC.
#
#   npm     nginx on ${address}:4873 (the front, stateless) proxies to the
#           cache, a second nginx on 127.0.0.1:4874 (repose-npm-cache) with
#           a proxy_cache capped at npm.maxSize, evicting the least recently
#           used. When the cache is down or failing, the front sends the
#           request to the registry itself and logs it (tag repose_npm_fallback):
#           npm has no fallback of its own, so it lives here. Requests that
#           carry credentials are never cached.
#   docker  the distribution registry as a Docker Hub pull-through mirror on
#           ${address}:5000, blobs expiring after docker.ttl. dockerd falls
#           back to Docker Hub by itself when a mirror fails.
#
# Both keep their data on a thin volume of its own, vg-guests/repose-cache
# (volumeSize, ext4, /var/cache/repose), so a cache can never fill the OS
# disk that holds /nix/store, and never takes more than its size from the
# pool. Removing the caches is `caches.enable = false` and a switch; guests
# then fall back to upstream (dockerd natively, npm by the guest's
# ~/.npmrc line being added only when the cache answers).
{ config, lib, pkgs, ... }:
let
  cfg = config.repose.host.caches;
  dataDisk = config.repose.host.dataDisk.enable;
  address = cfg.address;
  dir = "/var/cache/repose";
  npmPort = toString cfg.npm.port;
  npmCachePort = toString cfg.npm.cachePort;

  # Access log lines carry no URL: a package name can be a private one.
  logFormat = ''log_format repose_cache '$time_iso8601 status=$status cache=$upstream_cache_status bytes=$body_bytes_sent ms=$request_time';'';

  npmCacheConf = pkgs.writeText "repose-npm-cache.conf" ''
    pid /run/repose-npm-cache/nginx.pid;
    error_log stderr error;
    worker_processes 2;
    events { worker_connections 1024; }
    http {
      ${logFormat}
      access_log syslog:server=unix:/dev/log,tag=repose_npm_cache repose_cache;
      client_body_temp_path /run/repose-npm-cache/body;
      proxy_temp_path ${dir}/npm-tmp;
      fastcgi_temp_path /run/repose-npm-cache/fastcgi;
      uwsgi_temp_path /run/repose-npm-cache/uwsgi;
      scgi_temp_path /run/repose-npm-cache/scgi;
      resolver ${cfg.resolver} valid=300s ipv6=off;
      proxy_cache_path ${dir}/npm levels=1:2 keys_zone=npm:64m max_size=${cfg.npm.maxSize} inactive=${cfg.npm.inactive} use_temp_path=off;

      map $http_authorization $repose_authed { default 1; "" 0; }

      server {
        listen 127.0.0.1:${npmCachePort};
        set $upstream ${cfg.npm.upstream};

        proxy_http_version 1.1;
        proxy_ssl_server_name on;
        proxy_set_header Host ${cfg.npm.upstreamHost};
        # Bodies are rewritten below, so ask for them uncompressed.
        proxy_set_header Accept-Encoding "";
        proxy_cache npm;
        proxy_cache_lock on;
        proxy_cache_use_stale error timeout updating http_500 http_502 http_503 http_504;
        proxy_cache_bypass $repose_authed;
        proxy_no_cache $repose_authed;
        # registry.npmjs.org answers through Cloudflare, which sets its
        # __cf_bm cookie on every response, and nginx stores nothing that
        # sets a cookie: the live cache held 4 KB after 495 misses. The
        # cookie means nothing to npm, so it is neither a reason not to
        # cache nor passed on to guests (DECISIONS I-214). Authenticated
        # requests stay uncached by the two lines above.
        proxy_ignore_headers Set-Cookie;
        proxy_hide_header Set-Cookie;

        # Tarballs never change once published.
        location ~ /-/[^/]+\.tgz$ {
          proxy_cache_key $uri;
          proxy_cache_valid 200 ${cfg.npm.inactive};
          proxy_pass $upstream;
        }

        # Package documents change on every publish: short, and served
        # stale while one request refreshes them. The tarball URLs inside
        # point back at the front, so tarballs come through the cache too
        # (npm rewrites them itself; pnpm and yarn do not).
        location / {
          proxy_cache_key $uri$http_accept;
          proxy_cache_valid 200 ${cfg.npm.metadataValid};
          sub_filter '${cfg.npm.upstream}/' 'http://$http_host/';
          sub_filter_once off;
          sub_filter_types application/json application/vnd.npm.install-v1+json;
          proxy_pass $upstream;
        }
      }
    }
  '';
in
{
  options.repose.host.caches = {
    enable = lib.mkOption {
      type = lib.types.bool;
      default = true;
      description = "Run the npm and Docker Hub pull-through caches for guests (DECISIONS I-202).";
    };
    address = lib.mkOption {
      type = lib.types.str;
      default = "10.63.255.254";
      description = ''
        The host services address: a /32 on br-guests, the same on every
        host, outside 10.64.0.0/12, which the guest base names for its
        caches. Changing it is a guest base change too.
      '';
    };
    volumeSize = lib.mkOption {
      type = lib.types.str;
      default = "64G";
      description = "Size of the vg-guests/repose-cache thin volume both caches live on.";
    };
    resolver = lib.mkOption {
      type = lib.types.str;
      default = "168.63.129.16";
      description = "DNS resolver nginx uses for the upstream registry (Azure's by default).";
    };
    npm = {
      port = lib.mkOption { type = lib.types.port; default = 4873; };
      cachePort = lib.mkOption { type = lib.types.port; default = 4874; };
      maxSize = lib.mkOption { type = lib.types.str; default = "40g"; description = "nginx proxy_cache max_size; least recently used entries go first."; };
      inactive = lib.mkOption { type = lib.types.str; default = "30d"; };
      metadataValid = lib.mkOption { type = lib.types.str; default = "5m"; };
      upstream = lib.mkOption { type = lib.types.str; default = "https://registry.npmjs.org"; };
      upstreamHost = lib.mkOption { type = lib.types.str; default = "registry.npmjs.org"; };
    };
    docker = {
      port = lib.mkOption { type = lib.types.port; default = 5000; };
      ttl = lib.mkOption { type = lib.types.str; default = "168h"; description = "How long the mirror keeps a blob it fetched."; };
      remote = lib.mkOption { type = lib.types.str; default = "https://registry-1.docker.io"; };
    };
  };

  config = lib.mkIf cfg.enable {
    # The address exists whether or not the host is registered yet.
    systemd.network.networks."20-br-guests".address = [ "${address}/32" ];
    # nginx and the registry bind the address even before networkd has put
    # it on the bridge: kernel.nix already sets net.ipv4.ip_nonlocal_bind.

    # The volume needs the pool, which repose-guest-pool activates
    # (DECISIONS I-643): required and ordered after it, and started again
    # whenever it is, so a pool brought back by hand brings the caches back
    # with it. npm-cache and the registry are wanted by this unit for the
    # same reason; on 2026-10-10 the registry stayed down for a day after
    # the pool came back.
    systemd.services.repose-cache-volume = {
      description = "The caches' thin volume, vg-guests/repose-cache on ${dir}";
      wantedBy = [ "multi-user.target" ] ++ lib.optional dataDisk "repose-guest-pool.service";
      after = [ "local-fs.target" ] ++ lib.optional dataDisk "repose-guest-pool.service";
      requires = lib.optional dataDisk "repose-guest-pool.service";
      path = [ (lib.getBin config.services.lvm.package) pkgs.e2fsprogs pkgs.util-linux pkgs.coreutils ];
      serviceConfig = {
        Type = "oneshot";
        RemainAfterExit = true;
        # The pool unit is active long after the pool may have gone; a
        # condition, not a failure, so the unit is only skipped.
        ExecCondition = pkgs.writeShellScript "repose-pool-active" ''
          [ "$(${lib.getBin config.services.lvm.package}/bin/lvs --noheadings -o lv_active vg-guests/thin 2>/dev/null | ${pkgs.coreutils}/bin/tr -d ' ')" = active ]
        '';
      };
      script = ''
        dev=/dev/vg-guests/repose-cache
        if ! lvs vg-guests/repose-cache >/dev/null 2>&1; then
          lvcreate -q -V ${cfg.volumeSize} -T vg-guests/thin -n repose-cache
        fi
        lvchange -q -ay vg-guests/repose-cache
        if ! blkid -o value -s TYPE "$dev" >/dev/null 2>&1; then
          mkfs.ext4 -q -L repose-cache -E lazy_itable_init=1,lazy_journal_init=1 "$dev"
        fi
        mkdir -p ${dir}
        mountpoint -q ${dir} || mount -o noatime,discard "$dev" ${dir}
        install -d -o nginx -g nginx -m 0750 ${dir}/npm ${dir}/npm-tmp
        install -d -o docker-registry -g docker-registry -m 0750 ${dir}/docker
      '';
    };

    # The front: always up, stateless, falls back to the registry itself.
    services.nginx = {
      enable = true;
      commonHttpConfig = ''
        ${logFormat}
      '';
      virtualHosts."repose-npm" = {
        listen = [ { addr = address; port = cfg.npm.port; } ];
        extraConfig = ''
          access_log syslog:server=unix:/dev/log,tag=repose_npm repose_cache;
          # Package names are not logged, and nginx's own error lines
          # would carry them: only the fallback's access line says it ran.
          error_log stderr crit;
          client_max_body_size 64m;
          resolver ${cfg.resolver} valid=300s ipv6=off;
          # The cache fetches package documents uncompressed to rewrite
          # their tarball URLs, so the front compresses them for a guest
          # that asks (DECISIONS I-217): `next`'s document is 25.5 MB
          # plain and about 2 MB gzipped. Level 1 because the link is the
          # host's own bridge and a large document is compressed on every
          # request; JSON gets most of its ratio there. Tarballs
          # (application/octet-stream) are gzip already and not listed;
          # an answer that is already encoded (the fallback's, straight
          # from the registry) is never compressed again.
          gzip on;
          gzip_proxied any;
          gzip_vary on;
          gzip_comp_level 1;
          gzip_min_length 1024;
          gzip_types application/json application/vnd.npm.install-v1+json;
        '';
        locations."/" = {
          extraConfig = ''
            proxy_pass http://127.0.0.1:${npmCachePort};
            proxy_http_version 1.1;
            # The cache writes this back into tarball URLs.
            proxy_set_header Host $host:$server_port;
            proxy_connect_timeout 2s;
            error_page 502 504 = @direct;
          '';
        };
        locations."@direct" = {
          extraConfig = ''
            access_log syslog:server=unix:/dev/log,tag=repose_npm_fallback repose_cache;
            set $upstream ${cfg.npm.upstream};
            proxy_pass $upstream;
            proxy_http_version 1.1;
            proxy_ssl_server_name on;
            proxy_set_header Host ${cfg.npm.upstreamHost};
            # A registry that cannot be reached fails fast, not after
            # nginx's 30 s resolver and 60 s connect defaults.
            resolver_timeout 5s;
            proxy_connect_timeout 5s;
          '';
        };
      };
    };
    systemd.services.nginx = {
      after = [ "repose-cache-volume.service" ];
      serviceConfig.Restart = lib.mkForce "always";
    };

    systemd.services.repose-npm-cache = {
      description = "npm registry cache for guests (behind the nginx front on ${address}:${npmPort})";
      wantedBy = [ "multi-user.target" "repose-cache-volume.service" ];
      after = [ "network-online.target" "repose-cache-volume.service" ];
      wants = [ "network-online.target" ];
      requires = [ "repose-cache-volume.service" ];
      # Without the volume (no data disk yet) the cache does not start and
      # the front falls back to the registry.
      unitConfig.ConditionPathIsMountPoint = dir;
      serviceConfig = {
        ExecStart = "${pkgs.nginx}/bin/nginx -e stderr -c ${npmCacheConf} -g 'daemon off;'";
        ExecReload = "${pkgs.coreutils}/bin/kill -HUP $MAINPID";
        User = "nginx";
        Group = "nginx";
        RuntimeDirectory = "repose-npm-cache";
        Restart = "always";
        RestartSec = "2s";
        NoNewPrivileges = true;
        ProtectSystem = "strict";
        ProtectHome = true;
        ReadWritePaths = [ dir ];
        PrivateTmp = true;
      };
    };

    services.dockerRegistry = {
      enable = true;
      listenAddress = address;
      port = cfg.docker.port;
      storagePath = "${dir}/docker";
      enableDelete = true;
      extraConfig = {
        proxy = {
          remoteurl = cfg.docker.remote;
          ttl = cfg.docker.ttl;
        };
        log.accesslog.disabled = true;
        log.level = "warn";
      };
    };
    systemd.services.docker-registry = {
      wantedBy = [ "repose-cache-volume.service" ];
      after = [ "repose-cache-volume.service" ];
      requires = [ "repose-cache-volume.service" ];
      # Without the volume, no mirror: dockerd goes to Docker Hub.
      unitConfig.ConditionPathIsMountPoint = dir;
      # The proxy asks Docker Hub for its auth challenge at start and exits
      # (a panic) when it cannot reach it; keep trying, slowly, forever,
      # rather than hitting the start limit and staying down after a
      # network blip at boot. dockerd uses Docker Hub meanwhile.
      unitConfig.StartLimitIntervalSec = 0;
      serviceConfig.Restart = lib.mkForce "always";
      serviceConfig.RestartSec = "10s";
      # distribution v3 exports OpenTelemetry traces to localhost:4318 by
      # default; nothing listens there, so it logged "traces export ...
      # connection refused" about six times a minute.
      environment = {
        OTEL_TRACES_EXPORTER = "none";
        OTEL_SDK_DISABLED = "true";
      };
    };
  };
}
