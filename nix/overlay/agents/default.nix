# Platform-owned agent overlay. docs/DECISIONS.md R3-19: agents come from
# here, not nixpkgs, so the platform decides when a tenant's agent changes.
#
# Split of ownership (docs/workstreams/02-guest-base.md §3): workstream 12
# owns the *package definitions* (`reposeAgentsUnwrapped`: each agent from
# its upstream release binary, pinned by version and hash in versions.json,
# bumped by scripts/bump-agents.sh); workstream 02 owns the wrappers, the
# hook plumbing and the MCP servers.
final: prev:
let
  wrap = import ./wrap.nix { pkgs = final; };
in
{
  reposeAgentsUnwrapped = {
    claude-code = final.callPackage ./claude-code.nix { };
    opencode = final.callPackage ./opencode.nix { };
    codex = final.callPackage ./codex.nix { };
    gemini-cli = final.callPackage ./gemini-cli.nix { };
    pi-coding-agent = final.callPackage ./pi-coding-agent.nix { };
  };

  # herdr, the second multiplexer (DECISIONS I-501): in every guest's
  # system packages, unwrapped, pinned in versions.json beside the agents.
  reposeHerdr = final.callPackage ./herdr.nix { };

  # The five agents, each wrapped per guest-conventions.md "Agent wrappers".
  reposeAgents = builtins.mapAttrs (name: pkg: wrap { inherit name pkg; })
    final.reposeAgentsUnwrapped;

  # The checkout's dev environment loader every agent wrapper sources
  # (DECISIONS I-259), also at /etc/repose/devshell.sh for `repose exec`
  # (I-275), so a command run that way sees what an agent sees.
  reposeDevshell = final.replaceVars ./devshell.sh {
    inherit (final) direnv jq tmux coreutils;
  };

  # Runs once per agent start: idempotent hook and MCP registration.
  repose-agent-setup = final.callPackage ./agent-setup.nix { herdr = final.reposeHerdr; };

  # Shell implementation of the repose-hook contract (guest-conventions.md);
  # the base uses it until workstream 04's Go binary exists in cmd/repose-hook.
  repose-hook-shim = final.callPackage ./repose-hook.nix { };

  # Playwright's browsers, chromium preset (with the headless shell that
  # headless launches use). The WebKit and Firefox builds are neither wanted
  # nor, at this nixpkgs revision, buildable (WebKit misses libmanette).
  reposePlaywrightBrowsers = prev.playwright-driver.browsers.override {
    withFirefox = false;
    withWebkit = false;
  };

  # nixpkgs's playwright-test bakes the all-browsers set into its wrapper
  # (and playwright-mcp links against it), so its install phase is rewritten
  # to point at the chromium set. The string's other references (the
  # playwright npm build, node) are kept through their contexts; only the
  # all-browsers derivation drops out, so it is never built.
  reposePlaywrightTest =
    let
      old = prev.playwright-test;
      all = prev.playwright-driver.browsers;
      keep = prev.lib.filterAttrs (drv: _: drv != all.drvPath) (builtins.getContext old.installPhase);
      phase = builtins.replaceStrings [ "${all}" ] [ "${final.reposePlaywrightBrowsers}" ]
        (builtins.unsafeDiscardStringContext old.installPhase);
    in old.overrideAttrs (_: { installPhase = builtins.appendContext phase keep; });

  reposeMcp = {
    # nixpkgs keeps playwright-mcp and playwright-driver in step; the two
    # are tightly coupled, so both come from the same locked rev, with the
    # browsers swapped for the chromium preset above.
    #
    # nixpkgs's wrapper forces an isolated (in-memory) context whenever no
    # user data dir is set; attached to the guest's shared browser over
    # CDP that would put the agent in a context of its own, apart from the
    # window and logins the user sees on the desktop (I-246). With
    # --cdp-endpoint the server uses the browser's default context.
    playwright-mcp =
      let
        pkg = prev.playwright-mcp.override {
          playwright-test = final.reposePlaywrightTest;
          playwright-driver = prev.playwright-driver // { browsers = final.reposePlaywrightBrowsers; };
        };
        old = ''if [ -z "$PLAYWRIGHT_MCP_USER_DATA_DIR" ]; then export PLAYWRIGHT_MCP_ISOLATED=1; fi'';
        new = ''case " $* " in *" --cdp-endpoint"*) ;; *) if [ -z "$PLAYWRIGHT_MCP_USER_DATA_DIR" ] && [ -z "''${PLAYWRIGHT_MCP_CDP_ENDPOINT:-}" ]; then export PLAYWRIGHT_MCP_ISOLATED=1; fi ;; esac'';
      in
      assert prev.lib.assertMsg (prev.lib.hasInfix old pkg.postInstall)
        "playwright-mcp: nixpkgs's wrapper changed; revisit the isolated override (I-246)";
      pkg.overrideAttrs (o: { postInstall = builtins.replaceStrings [ old ] [ new ] o.postInstall; });
    chrome-devtools-mcp = final.callPackage ./chrome-devtools-mcp.nix { };
  };

  reposeOpencodePlugin = ./opencode-plugin.js;
}
