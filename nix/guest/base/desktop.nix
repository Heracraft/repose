# The on-demand desktop: TigerVNC's Xvnc as the X display :99 and the VNC
# server in one process on 127.0.0.1:5900, openbox, websockify serving the
# viewer page repose ships (desktop/viewer/) on 127.0.0.1:6081 behind the
# socket-activated entry point on 127.0.0.1:6080 (DECISIONS I-33, I-246,
# I-292). The display has two users: the agents' browser (browser.nix),
# which draws on it whether or not anyone watches, and the viewer
# (websockify and the page), which a connection to 6080 starts through
# systemd-socket-proxyd. Starting the viewer starts the browser too, so the
# desktop is never empty. Xvnc and openbox stop by themselves once neither
# needs them (StopWhenUnneeded); nothing runs and nothing is paid for until
# one of them is asked for.
#
# Xvnc implements the client's SetDesktopSize, so the screen takes the
# size of the user's browser tab (the viewer page asks for it) instead of
# being a 1440x900 picture scaled to fit; 1440x900 is only the size before
# the first viewer connects. Xvfb has one fixed mode and x11vnc cannot
# resize it, which is why they went (I-292).
#
# A per-minute check stops the viewer after 30 minutes without a client
# (`systemctl start repose-desktop-idle` stops it now), and the browser
# after 30 minutes with neither a DevTools client (an MCP server holds its
# connection for the agent's whole session) nor a viewer.
#
# The password is generated once per boot into
# /run/repose/desktop/vnc-password (0600 dev), the first time the display
# starts, so a viewer that idled out reconnects with the link it has; the
# CLI puts it in the viewer URL's fragment (docs/features/browser.md). The
# desktop is only reachable through the SSH forward, so the password is
# defence in depth, not the boundary.
{ config, lib, pkgs, ... }:
let
  display = ":99";
  dir = "/run/repose/desktop";
  webDir = "${dir}/web";
  # python312 is in the closure already (tools.nix); no second interpreter.
  # numpy is optional in websockify (it only speeds up unmasking what the
  # browser sends: keys and pointer moves) and costs about 480 MB of
  # closure with openblas, so it is dropped (DECISIONS I-218).
  websockify = pkgs.python312Packages.websockify.overridePythonAttrs (o: {
    dependencies = lib.filter (d: (d.pname or "") != "numpy") o.dependencies;
    dontCheckRuntimeDeps = true;
    doCheck = false;
  });
  # The viewer page: repose's own index.html, viewer.js and viewer.css on
  # noVNC's ES module core (core/ and vendor/ copied out of the package,
  # nothing else of it: a link into `${pkgs.novnc}` would drag its
  # novnc_proxy wrapper, a second Python and websockify with numpy, blas
  # and lapack into the closure, 260 MB, I-218). No build step: the files
  # are copied as they are in the repository.
  viewer = pkgs.runCommand "repose-desktop-viewer" { } ''
    mkdir -p $out
    cp ${./desktop/viewer}/* $out/
    cp -r ${pkgs.novnc}/share/webapps/novnc/core $out/core
    cp -r ${pkgs.novnc}/share/webapps/novnc/vendor $out/vendor
  '';
  idleSeconds = 1800;

  # Every window maximised: the browser fills the screen the user watches,
  # and follows it when the viewer resizes the screen (openbox reapplies
  # the maximised geometry on a RandR change).
  openboxConfig = pkgs.writeText "repose-openbox-rc.xml" ''
    <?xml version="1.0" encoding="UTF-8"?>
    <openbox_config xmlns="http://openbox.org/3.4/rc">
      <desktops><number>1</number></desktops>
      <applications>
        <application class="*">
          <maximized>yes</maximized>
          <decor>no</decor>
        </application>
      </applications>
    </openbox_config>
  '';

  # One password per boot: /run is a tmpfs, so the file is gone at the
  # next boot and made again then. vnc-password is the plain text the CLI
  # reads; vnc-passwd is TigerVNC's obfuscated form Xvnc reads.
  genPassword = pkgs.writeShellApplication {
    name = "repose-vnc-password";
    runtimeInputs = [ pkgs.coreutils pkgs.tigervnc ];
    text = ''
      if [ -s ${dir}/vnc-password ] && [ -s ${dir}/vnc-passwd ]; then
        exit 0
      fi
      pw=$(head -c 32 /dev/urandom | base64 | tr -dc 'A-Za-z0-9' | head -c 8)
      umask 077
      printf '%s\n' "$pw" > ${dir}/vnc-password
      printf '%s\n' "$pw" | vncpasswd -f > ${dir}/vnc-passwd
    '';
  };

  # The web root websockify serves: the viewer's files, plus project.json
  # with the project's name for the page's top bar, written at every
  # viewer start (the name can change with the laptop's, I-198).
  webRoot = pkgs.writeShellApplication {
    name = "repose-desktop-web";
    runtimeInputs = [ pkgs.coreutils pkgs.jq ];
    text = ''
      rm -rf ${webDir}
      mkdir -p ${webDir}
      for f in ${viewer}/*; do
        ln -s "$f" ${webDir}/
      done
      project=/home/dev/.repose/project.json
      if [ -s "$project" ]; then
        name=$(jq -r '.name // .slug // empty' "$project" 2>/dev/null || true)
      fi
      [ -n "''${name:-}" ] || name=$(cat /proc/sys/kernel/hostname)
      jq -n --arg name "$name" --argjson idle ${toString (idleSeconds / 60)} \
        '{name: $name, idle_minutes: $idle}' > ${webDir}/project.json
    '';
  };

  idleCheck = pkgs.writeShellApplication {
    name = "repose-desktop-idle-check";
    runtimeInputs = [ pkgs.coreutils pkgs.iproute2 pkgs.systemd ];
    text = ''
      # A bridge whose tunnel is gone (the laptop's ssh ended without its
      # hold script running, or sshd gave up on a sleeping laptop) goes
      # back to the machine's browser here, so agents are never left
      # with an endpoint that refuses every connection (I-296).
      if systemctl is-active --quiet repose-browser-bridge.socket \
         && ! ss -Hltn "sport = :9226" | grep -q LISTEN; then
        repose-browser-bridge off
      fi
      viewer=false; browser=false
      systemctl is-active --quiet repose-novnc.service && viewer=true
      systemctl is-active --quiet repose-browser.service && browser=true
      [ "$viewer" = true ] || [ "$browser" = true ] || exit 0
      now=$(date +%s)
      # age <stamp>: seconds since the stamp, created now when missing.
      age() {
        [ -e "$1" ] || touch "$1"
        echo $((now - $(stat -c %Y "$1")))
      }
      viewers=$(ss -Htn state established '( sport = :6081 or sport = :5900 )' | wc -l)
      devtools=$(ss -Htn state established '( sport = :9225 )' | wc -l)
      [ "$viewers" -eq 0 ] || touch ${dir}/last-client
      [ "$devtools" -eq 0 ] || touch ${dir}/last-cdp
      viewer_idle=$(age ${dir}/last-client)
      cdp_idle=$(age ${dir}/last-cdp)
      if [ "$viewer" = true ] && [ "$viewer_idle" -ge ${toString idleSeconds} ]; then
        systemctl start repose-desktop-idle.service
      fi
      if [ "$browser" = true ] && [ "$viewer_idle" -ge ${toString idleSeconds} ] \
         && [ "$cdp_idle" -ge ${toString idleSeconds} ]; then
        systemctl stop repose-browser-proxy.service repose-browser.service
      fi
    '';
  };

  # Wait until a listener is bound, so a unit only counts as started when a
  # dependent can connect (websockify and Xvnc have no sd_notify).
  waitPort = port: pkgs.writeShellScript "repose-wait-${toString port}" ''
    for _ in $(seq 1 100); do
      if ${pkgs.iproute2}/bin/ss -Hltn "sport = :${toString port}" | grep -q LISTEN; then exit 0; fi
      sleep 0.1
    done
    echo "port ${toString port} not listening after 10 s" >&2
    exit 1
  '';

  # Xvnc needs a moment to open its X socket.
  waitDisplay = pkgs.writeShellScript "repose-wait-display" ''
    for _ in $(seq 1 100); do
      [ -S /tmp/.X11-unix/X99 ] && exit 0
      sleep 0.1
    done
    echo "display ${display} not up after 10 s" >&2
    exit 1
  '';

  # restartIfChanged: a base switch never restarts the display under the
  # agents' browser or a user's viewer; the units stop on their own when
  # unneeded or idle, and start from the new base next time (DECISIONS
  # I-536).
  common = {
    restartIfChanged = false;
    serviceConfig = {
      User = "dev";
      Group = "dev";
      Restart = "no";
    };
    environment.DISPLAY = display;
  };
in
{
  environment.systemPackages = [ pkgs.tigervnc pkgs.openbox websockify ];

  # The X display and the VNC server. -ac: the browser, the MCP servers and
  # a root `import` in the VM test all draw on or read the display.
  # -noreset keeps the server up between X clients, as Xvfb did.
  systemd.services.repose-xvnc = lib.recursiveUpdate common {
    description = "repose desktop: Xvnc ${display}, VNC on 127.0.0.1:5900";
    unitConfig.StopWhenUnneeded = true;
    serviceConfig = {
      ExecStartPre = "${genPassword}/bin/repose-vnc-password";
      ExecStart = lib.concatStringsSep " " [
        "${pkgs.tigervnc}/bin/Xvnc ${display}"
        "-geometry 1440x900 -depth 24 -ac -nolisten tcp -noreset"
        "-localhost -rfbport 5900 -SecurityTypes VncAuth -PasswordFile ${dir}/vnc-passwd"
        "-AlwaysShared -AcceptSetDesktopSize -FrameRate 60 -desktop repose"
      ];
      # A display started for the browser alone starts the viewer's idle
      # clock, so the stamp is never older than the display.
      ExecStartPost = [ waitDisplay (waitPort 5900) "${pkgs.coreutils}/bin/touch ${dir}/last-client" ];
    };
  };

  systemd.services.repose-openbox = lib.recursiveUpdate common {
    description = "repose desktop: window manager";
    requires = [ "repose-xvnc.service" ];
    after = [ "repose-xvnc.service" ];
    bindsTo = [ "repose-xvnc.service" ];
    unitConfig.StopWhenUnneeded = true;
    serviceConfig.ExecStart = "${pkgs.openbox}/bin/openbox --config-file ${openboxConfig}";
  };

  # Xvnc's clipboard helper: an X client that carries the X selections to
  # the VNC clipboard and back, so the viewer page's clipboard bridge has
  # something to bridge. Runs only while the viewer does.
  systemd.services.repose-vncconfig = lib.recursiveUpdate common {
    description = "repose desktop: clipboard between the display and the viewer";
    requires = [ "repose-xvnc.service" ];
    after = [ "repose-xvnc.service" ];
    bindsTo = [ "repose-xvnc.service" ];
    unitConfig.StopWhenUnneeded = true;
    serviceConfig.ExecStart = "${pkgs.tigervnc}/bin/vncconfig -nowin";
  };

  # The viewer: websockify bridges the page's WebSocket to Xvnc and serves
  # the page. It wants the browser, so the desktop always shows it.
  systemd.services.repose-novnc = lib.recursiveUpdate common {
    description = "repose desktop: the viewer (websockify) on 127.0.0.1:6081";
    requires = [ "repose-xvnc.service" ];
    wants = [ "repose-openbox.service" "repose-vncconfig.service" "repose-browser.service" ];
    after = [ "repose-xvnc.service" "repose-openbox.service" ];
    bindsTo = [ "repose-xvnc.service" ];
    serviceConfig = {
      ExecStartPre = "${webRoot}/bin/repose-desktop-web";
      ExecStart = "${websockify}/bin/websockify --file-only --web ${webDir} 127.0.0.1:6081 127.0.0.1:5900";
      # A viewer started while the browser had the display up for hours
      # starts its own idle clock.
      ExecStartPost = [ (waitPort 6081) "${pkgs.coreutils}/bin/touch ${dir}/last-client" ];
    };
  };

  systemd.sockets.repose-novnc = {
    description = "repose desktop: viewer entry point on 127.0.0.1:6080";
    wantedBy = [ "sockets.target" ];
    socketConfig = {
      ListenStream = "127.0.0.1:6080";
      Service = "repose-novnc-proxy.service";
    };
  };

  systemd.services.repose-novnc-proxy = {
    description = "repose desktop: proxy 6080 to the viewer, starting the chain";
    restartIfChanged = false;
    requires = [ "repose-novnc.service" ];
    after = [ "repose-novnc.service" ];
    serviceConfig = {
      ExecStart = "${pkgs.systemd}/lib/systemd/systemd-socket-proxyd --exit-idle-time=60s 127.0.0.1:6081";
      PrivateTmp = true;
      DynamicUser = true;
    };
  };

  # Stops the viewer. Xvnc and openbox follow unless the agents' browser
  # still draws on them; the browser has its own idle stop above.
  systemd.services.repose-desktop-idle = {
    description = "repose desktop: stop the viewer";
    serviceConfig = {
      Type = "oneshot";
      ExecStart = "${pkgs.systemd}/bin/systemctl stop repose-novnc-proxy.service repose-novnc.service";
    };
  };

  systemd.services.repose-desktop-idle-check = {
    description = "repose desktop: stop the viewer and the browser after ${toString (idleSeconds / 60)} minutes unused";
    # repose-browser-bridge (browser.nix) for the bridge guard above.
    path = [ "/run/current-system/sw" ];
    serviceConfig = {
      Type = "oneshot";
      ExecStart = "${idleCheck}/bin/repose-desktop-idle-check";
    };
  };

  systemd.timers.repose-desktop-idle-check = {
    wantedBy = [ "timers.target" ];
    timerConfig = {
      OnBootSec = "1min";
      OnUnitActiveSec = "1min";
      AccuracySec = "10s";
    };
  };

  # dev may start and stop the desktop without a password (repose-guest-profile).
  security.sudo.extraRules = [
    {
      users = [ "dev" ];
      commands = [
        { command = "${pkgs.systemd}/bin/systemctl start repose-novnc.service"; options = [ "NOPASSWD" ]; }
        { command = "${pkgs.systemd}/bin/systemctl start repose-desktop-idle.service"; options = [ "NOPASSWD" ]; }
      ];
    }
  ];
}
