# Chromium, Playwright's browsers, and the two MCP servers every guest has
# (docs/features/browser.md). The agents' browser is one headed Chromium on
# the desktop's X display :99 (DECISIONS I-246): repose-browser.socket on
# 127.0.0.1:9224 starts it, with Xvnc (the display) and the window manager, on the first
# DevTools connection, and both MCP servers attach to it there, so
# `repose browser` shows what the agent is doing and the user can take
# over in the same window. Its profile persists in
# ~/.local/share/repose/browser. While `repose browser bridge` is on
# (DECISIONS I-296), the same endpoint is served by
# repose-browser-bridge.socket instead, whose proxy reaches the laptop's
# own Chrome through the CLI's reverse tunnel on 127.0.0.1:9226; the MCP
# servers notice nothing but a reconnect. That browser runs in the system slice
# repose-browser.slice; the MCP servers and any chromium a user starts run
# in the per-user slice of the same name, so a runaway page cannot take the
# agent down with it. The ceiling is a share of the guest's memory, which
# systemd resolves at boot: 37.5 percent is 1.5 GB small, 3 GB large, 6 GB
# xl, so one system closure serves every class (DECISIONS I-34, I-43).
{ config, lib, pkgs, ... }:
let
  browserMemory = "37.5%";
  slice = {
    MemoryMax = browserMemory;
    MemoryHigh = browserMemory;
  };
  display = ":99";
  # The DevTools endpoint the MCP servers use (socket-activated) and the
  # port Chromium itself listens on behind it. Not 9222, which stays free
  # for a Chromium of the user's own; the CLI never auto-forwards either
  # (internal/cli/forward.go).
  cdpPort = 9224;
  backendPort = 9225;
  # The laptop's end of `repose browser bridge` (DECISIONS I-296): the CLI
  # reverse-tunnels its Chrome to this port, and while the bridge is on,
  # 9224 is served by repose-browser-bridge.socket, which proxies here
  # instead of to the machine's own Chromium. The MCP servers keep their
  # one endpoint; the switch is which proxy answers it.
  bridgePort = 9226;
  cdpUrl = "http://127.0.0.1:${toString cdpPort}";
  profileDir = "/home/dev/.local/share/repose/browser";

  # repose-browser-bridge on|off|status|tunnel: which proxy answers 9224.
  # `on` and `off` need root (repose-guest-profile runs them with sudo);
  # `status` and `tunnel` do not. Stopping a proxy ends the connections
  # the MCP servers hold through it, so their next call reconnects
  # through 9224 to whichever browser is there now (both servers
  # reconnect when their browser is gone, I-246).
  bridge = pkgs.writeShellApplication {
    name = "repose-browser-bridge";
    runtimeInputs = [ pkgs.systemd pkgs.iproute2 pkgs.coreutils pkgs.gnugrep ];
    text = ''
      case "''${1:-}" in
        on)
          systemctl stop repose-browser-proxy.service repose-browser.socket
          systemctl start repose-browser-bridge.socket
          ;;
        off)
          systemctl stop repose-browser-bridge-proxy.service repose-browser-bridge.socket
          systemctl start repose-browser.socket
          ;;
        status)
          if systemctl is-active --quiet repose-browser-bridge.socket; then echo on; else echo off; fi
          ;;
        tunnel)
          # Is a laptop's end listening: sshd's listener for the
          # reverse forward, gone when the ssh that carried it ended.
          ss -Hltn "sport = :${toString bridgePort}" | grep -q LISTEN
          ;;
        release)
          # Make room for a new bridge: end the ssh session that holds
          # the port from before (a laptop that slept keeps its listener
          # until sshd's ClientAlive gives up on it, two minutes). The
          # session process runs as dev, so dev sees and may kill it.
          for pid in $(ss -Hltnp "sport = :${toString bridgePort}" | grep -o 'pid=[0-9]*' | cut -d= -f2 | sort -u); do
            kill "$pid" 2>/dev/null || true
          done
          for _ in $(seq 1 20); do
            ss -Hltn "sport = :${toString bridgePort}" | grep -q LISTEN || exit 0
            sleep 0.1
          done
          echo "port ${toString bridgePort} is still held" >&2
          exit 1
          ;;
        *)
          echo "usage: repose-browser-bridge on|off|status|tunnel|release" >&2
          exit 64
          ;;
      esac
    '';
  };

  # Run a program inside the user's browser slice when a user manager is
  # reachable, else run it directly. `--scope` keeps stdio, which the MCP
  # servers need.
  scope = pkgs.writeShellApplication {
    name = "repose-browser-scope";
    runtimeInputs = [ pkgs.systemd pkgs.coreutils ];
    text = ''
      if [ "$#" -lt 1 ]; then
        echo "usage: repose-browser-scope <program> [args...]" >&2
        exit 64
      fi
      export XDG_RUNTIME_DIR="''${XDG_RUNTIME_DIR:-/run/user/$(id -u)}"
      export DBUS_SESSION_BUS_ADDRESS="''${DBUS_SESSION_BUS_ADDRESS:-unix:path=$XDG_RUNTIME_DIR/bus}"
      if [ -z "''${REPOSE_BROWSER_SCOPED:-}" ] \
         && systemd-run --user --quiet --scope --collect --slice=repose-browser.slice -- true 2>/dev/null; then
        export REPOSE_BROWSER_SCOPED=1
        exec systemd-run --user --quiet --scope --collect --slice=repose-browser.slice -- "$@"
      fi
      exec "$@"
    '';
  };

  scoped = name: pkg: bin: pkgs.symlinkJoin {
    name = "repose-${name}";
    paths = [ pkg ];
    postBuild = ''
      rm -f $out/bin/${bin}
      cat > $out/bin/${bin} <<WRAP
      #!${pkgs.runtimeShell}
      exec ${scope}/bin/repose-browser-scope ${pkg}/bin/${bin} "\$@"
      WRAP
      chmod +x $out/bin/${bin}
    '';
    passthru = { unwrapped = pkg; };
    meta = (pkg.meta or { }) // { mainProgram = bin; };
  };

  chromium = scoped "chromium" pkgs.chromium "chromium";
  playwrightMcp = scoped "playwright-mcp" pkgs.reposeMcp.playwright-mcp "playwright-mcp";
  chromeDevtoolsMcp = scoped "chrome-devtools-mcp" pkgs.reposeMcp.chrome-devtools-mcp "chrome-devtools-mcp";

  # /etc/repose/mcp.json: Claude Code's shape, read by repose-agent-setup
  # for Claude Code and Codex, and by the VM tests. The other agents get
  # the same servers from config.repose.mcpServers in their own system
  # layer (agent-guide.nix; DECISIONS I-553).
  mcpConfig = {
    mcpServers = lib.mapAttrs (_: s: { type = "stdio"; inherit (s) command args; }) config.repose.mcpServers;
    # Entries earlier bases registered. repose-agent-setup replaces a
    # user's entry that is exactly one of these, since it wrote them
    # itself, and leaves any other entry alone (user entries win, I-246).
    repose_retired = {
      playwright = [{ type = "stdio"; command = "playwright-mcp"; args = [ "--headless" ]; }];
      chrome-devtools = [{ type = "stdio"; command = "chrome-devtools-mcp"; args = [ "--headless" ]; }];
    };
  };

  # The agents' browser. No --headless: it draws on :99 whether or not
  # anyone watches. No --window-size: the window manager maximises every
  # window (desktop.nix), so the browser is the screen's size and follows
  # it when the viewer resizes the screen to the user's tab (I-292). No
  # GPU in a guest: --disable-gpu composites
  # in software instead of relaunching a GPU process that fails to find
  # EGL, and WebGL still works through SwiftShader, as in Playwright's own
  # launches (which pass --enable-unsafe-swiftshader too).
  browserStart = pkgs.writeShellScript "repose-browser-start" ''
    mkdir -p ${profileDir}
    # A kill leaves the profile marked as crashed, and Chromium then offers
    # to restore the last session on top of the agent's page.
    ${pkgs.gnused}/bin/sed -i -e 's/"exit_type":"Crashed"/"exit_type":"Normal"/' \
      -e 's/"exited_cleanly":false/"exited_cleanly":true/' \
      ${profileDir}/Default/Preferences 2>/dev/null || true
    exec ${pkgs.chromium}/bin/chromium \
      --user-data-dir=${profileDir} \
      --remote-debugging-port=${toString backendPort} \
      --no-first-run --no-default-browser-check \
      --password-store=basic \
      --hide-crash-restore-bubble \
      --disable-gpu --enable-unsafe-swiftshader \
      --start-maximized --window-position=0,0 \
      about:blank
  '';

  # The unit counts as started only once the proxy can connect.
  waitBackend = pkgs.writeShellScript "repose-wait-${toString backendPort}" ''
    for _ in $(seq 1 200); do
      if ${pkgs.iproute2}/bin/ss -Hltn "sport = :${toString backendPort}" | grep -q LISTEN; then exit 0; fi
      sleep 0.1
    done
    echo "port ${toString backendPort} not listening after 20 s" >&2
    exit 1
  '';
in
{
  environment.systemPackages = [
    chromium
    playwrightMcp
    chromeDevtoolsMcp
    scope
    bridge
  ];

  environment.variables = {
    # PLAYWRIGHT_BROWSERS_PATH is the writable ~/.cache/ms-playwright,
    # seeded with the packaged browsers (compat.nix, DECISIONS I-228); the
    # MCP server's wrapper names the store path itself.
    PLAYWRIGHT_SKIP_VALIDATE_HOST_REQUIREMENTS = "1";
    # chrome-devtools-mcp and puppeteer users: never download a browser.
    PUPPETEER_SKIP_DOWNLOAD = "1";
    PUPPETEER_EXECUTABLE_PATH = "${chromium}/bin/chromium";
    CHROME_BIN = "${chromium}/bin/chromium";
  };

  repose.mcpServers = {
    playwright = { command = "playwright-mcp"; args = [ "--cdp-endpoint" cdpUrl ]; };
    chrome-devtools = { command = "chrome-devtools-mcp"; args = [ "--browserUrl" cdpUrl ]; };
  };

  environment.etc."repose/mcp.json".text = builtins.toJSON mcpConfig;

  # Gemini CLI's system defaults layer, beneath the user's
  # ~/.gemini/settings.json (DECISIONS I-553). Copied with mode 0644: Gemini
  # skips a system file whose resolved directory is not root-owned or is
  # group-writable, which a /nix/store symlink is. Folder trust is off: in
  # a folder it was not told about, Gemini disables every MCP server, and
  # agents on the machine already run without prompts (I-250). Self-update
  # is off: Gemini updated itself into ~/.npm-global, which shadowed the
  # repose wrapper (I-543). This file is the only definition of it.
  environment.etc."gemini-cli/system-defaults.json" = {
    text = builtins.toJSON {
      mcpServers = lib.mapAttrs (_: s: { inherit (s) command args; }) config.repose.mcpServers;
      security.folderTrust.enabled = false;
      general = { enableAutoUpdate = false; enableAutoUpdateNotification = false; };
    };
    mode = "0644";
  };

  # Fonts the pages render with (Noto Sans, Noto Sans Mono, Noto Serif;
  # Liberation for the metric-compatible Arial, Times and Courier names
  # pages ask for; colour emoji), so no page renders as boxes. Grayscale
  # antialiasing with slight hinting: the screen travels as an image to
  # the viewer, and subpixel colour fringes survive that trip as noise
  # while grayscale stays crisp (I-292).
  fonts = {
    fontconfig = {
      enable = true;
      antialias = true;
      hinting = { enable = true; style = "slight"; };
      subpixel = { rgba = "none"; lcdfilter = "none"; };
      defaultFonts = {
        sansSerif = [ "Noto Sans" ];
        serif = [ "Noto Serif" ];
        monospace = [ "Noto Sans Mono" ];
        emoji = [ "Noto Color Emoji" ];
      };
    };
    enableDefaultPackages = false;
    packages = with pkgs; [ noto-fonts noto-fonts-color-emoji liberation_ttf ];
  };

  systemd.user.slices.repose-browser = {
    description = "repose: browsers and browser MCP servers";
    sliceConfig = slice;
  };

  systemd.slices.repose-browser = {
    description = "repose: the agents' browser";
    sliceConfig = slice;
  };

  # A crash or an OOM kill of the browser stops this unit and the proxy
  # (BindsTo); the socket keeps listening, the next DevTools connection
  # starts both again with the same profile, and both MCP servers reconnect
  # on their next call.
  systemd.services.repose-browser = {
    description = "repose: the agents' Chromium on ${display}, DevTools on 127.0.0.1:${toString backendPort}";
    requires = [ "repose-xvnc.service" ];
    bindsTo = [ "repose-xvnc.service" ];
    wants = [ "repose-openbox.service" ];
    after = [ "repose-xvnc.service" "repose-openbox.service" ];
    environment = {
      DISPLAY = display;
      HOME = "/home/dev";
      LANG = "C.UTF-8";
    };
    serviceConfig = {
      User = "dev";
      Group = "dev";
      Slice = "repose-browser.slice";
      # TZ, so pages see the project's time zone.
      EnvironmentFile = "-/etc/repose/env";
      ExecStart = browserStart;
      # last-cdp starts the idle clock (desktop.nix).
      ExecStartPost = [ waitBackend "${pkgs.coreutils}/bin/touch /run/repose/desktop/last-cdp" ];
      # A renderer the slice limit kills is one crashed tab, not the end of
      # the browser.
      OOMPolicy = "continue";
      Restart = "no";
      TimeoutStopSec = 10;
      SuccessExitStatus = "0 15 SIGTERM";
    };
  };

  systemd.sockets.repose-browser = {
    description = "repose: the agents' browser DevTools endpoint on 127.0.0.1:${toString cdpPort}";
    wantedBy = [ "sockets.target" ];
    conflicts = [ "repose-browser-bridge.socket" ];
    socketConfig = {
      ListenStream = "127.0.0.1:${toString cdpPort}";
      Service = "repose-browser-proxy.service";
    };
  };

  # The same endpoint while `repose browser bridge` is on (I-296): started
  # by repose-browser-bridge on, never at boot. Its proxy reaches the
  # laptop's Chrome through sshd's reverse-forward listener on
  # 127.0.0.1:9226; when the tunnel is gone, a connection is refused at
  # once and the MCP server reports it, until the desktop idle check (or
  # the bridge's own exit) switches the endpoint back.
  systemd.sockets.repose-browser-bridge = {
    description = "repose: the DevTools endpoint on 127.0.0.1:${toString cdpPort}, bridged to the laptop's Chrome";
    conflicts = [ "repose-browser.socket" ];
    socketConfig = {
      ListenStream = "127.0.0.1:${toString cdpPort}";
      Service = "repose-browser-bridge-proxy.service";
    };
  };

  systemd.services.repose-browser-bridge-proxy = {
    description = "repose: proxy ${toString cdpPort} to the laptop's Chrome on 127.0.0.1:${toString bridgePort}";
    serviceConfig = {
      ExecStart = "${pkgs.systemd}/lib/systemd/systemd-socket-proxyd 127.0.0.1:${toString bridgePort}";
      PrivateTmp = true;
      DynamicUser = true;
    };
  };

  systemd.services.repose-browser-proxy = {
    description = "repose: proxy ${toString cdpPort} to the agents' browser, starting it";
    requires = [ "repose-browser.service" ];
    bindsTo = [ "repose-browser.service" ];
    after = [ "repose-browser.service" ];
    serviceConfig = {
      ExecStart = "${pkgs.systemd}/lib/systemd/systemd-socket-proxyd 127.0.0.1:${toString backendPort}";
      PrivateTmp = true;
      DynamicUser = true;
    };
  };
}
