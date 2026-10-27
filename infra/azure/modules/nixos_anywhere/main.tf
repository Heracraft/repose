# Turn a freshly booted Ubuntu VM into the NixOS system named by a flake
# attribute, using nixos-anywhere over SSH.
#
# Ubuntu is a delivery vehicle only (docs/workstreams/11-infra-opentofu.md §5,
# "Why Ubuntu as the install target"): nixos-anywhere kexecs into the NixOS
# installer, runs disko against nix/hosts/disko.nix, installs the closure and
# reboots. Nothing from the Ubuntu system survives, which is why the operator
# public keys are handed to the new system through --extra-files rather than
# being left in cloud-init's authorized_keys.
#
# The installer it kexecs into is always ours (nix/installer), built from the
# same flake_path this machine's system is built from and passed as `--kexec`
# below. There is no value that asks for the upstream release asset, because
# that asset is the defect: the --kexec block says what it does on this
# provider.
#
# The whole run is wrapped in coreutils `timeout`, because nixos-anywhere's
# wait for the installer is unbounded and a provisioner that never returns
# holds the environment's state lock. timeout signals the command's process
# group, so the ssh and nix copy children go with it.
#
# Anything the installed system needs that is *not* in the flake goes through
# post_install_commands, which run over SSH after the reboot. The join token
# takes that path (DECISIONS I-20): a token in cloud-init would sit in the
# Azure VM model and in IMDS for the life of the VM, and would be wiped by the
# install anyway.

terraform {
  required_version = ">= 1.6.0"
}

locals {
  ssh_common = concat(
    [
      "StrictHostKeyChecking=no",
      "UserKnownHostsFile=/dev/null",
      "ConnectTimeout=10",
    ],
    # ProxyJump, with the jump host's key pre-recorded by the install step
    # below. A ProxyCommand carrying the same -o options was tried first and
    # fails in nix copy: nix splits NIX_SSHOPTS on whitespace, so a value
    # with spaces becomes stray ssh arguments ("Bad stdio forwarding
    # specification '%h:%p'").
    var.jump_host == null ? [] : [
      "ProxyJump=${var.jump_user}@${var.jump_host}:${var.jump_port}",
    ],
  )

  # Single-quoted: the ProxyCommand value contains spaces, and unquoted it
  # split into stray arguments that made nixos-anywhere print its usage.
  ssh_option_flags = join(" ", [for o in local.ssh_common : "--ssh-option '${o}'"])
  ssh_cli_flags    = join(" ", [for o in local.ssh_common : "-o '${o}'"])

  authorized_keys = join("\n", var.authorized_keys)
}

# Wait until cloud-init has finished on the Ubuntu image, then put the
# operator keys on root ourselves. Connecting as root at this point is a race
# the first apply lost: until cloud-init's ssh module has run, root's
# authorized_keys is the image's forced-command line that refuses everything
# with "Please login as the user azureuser", and remote-exec treats that as a
# hard failure rather than a retry. The provisioning user is always let in,
# and installing the keys here also repairs a VM whose cloud-init ran with an
# older template.
resource "terraform_data" "wait_for_cloud_init" {
  triggers_replace = var.triggers

  connection {
    type                = "ssh"
    host                = var.target_host
    user                = var.bootstrap_user
    port                = 22
    private_key         = file(var.ssh_private_key_path)
    timeout             = var.connect_timeout
    bastion_host        = var.jump_host
    bastion_user        = var.jump_host == null ? null : var.jump_user
    bastion_port        = var.jump_host == null ? null : var.jump_port
    bastion_private_key = var.jump_host == null ? null : file(var.ssh_private_key_path)
  }

  provisioner "file" {
    content     = "${local.authorized_keys}\n"
    destination = "/tmp/repose-root-authorized_keys"
  }

  provisioner "remote-exec" {
    inline = [
      "sudo cloud-init status --wait >/dev/null 2>&1 || true",
      "sudo install -d -m 0700 -o root -g root /root/.ssh",
      "sudo install -m 0600 -o root -g root /tmp/repose-root-authorized_keys /root/.ssh/authorized_keys",
      "rm -f /tmp/repose-root-authorized_keys",
      "sudo test -s /root/.ssh/authorized_keys",
      "! sudo grep -q 'Please login as the user' /root/.ssh/authorized_keys",
      "sudo systemctl restart ssh || sudo systemctl restart sshd",
    ]
  }
}

resource "terraform_data" "install" {
  triggers_replace = var.triggers

  depends_on = [terraform_data.wait_for_cloud_init]

  provisioner "local-exec" {
    interpreter = ["/usr/bin/env", "bash", "-euo", "pipefail", "-c"]
    environment = {
      REPOSE_AUTHORIZED_KEYS = local.authorized_keys
    }
    command = <<-EOT
      extra=$(mktemp -d)
      # Holds the GC root for the installer image; see the --kexec block.
      imgroot=$(mktemp -d)
      trap 'rm -rf "$extra" "$imgroot"' EXIT
      # The jump connection ProxyJump opens ignores the -o options above, so
      # it checks the jump host against ~/.ssh/known_hosts; record the edge's
      # current key there (it changes on every reinstall).
      %{if var.jump_host != null~}
      mkdir -p ~/.ssh && touch ~/.ssh/known_hosts
      ssh-keygen -R '[${var.jump_host}]:${var.jump_port}' >/dev/null 2>&1 || true
      ssh-keygen -R '${var.jump_host}' >/dev/null 2>&1 || true
      ssh-keyscan -T 15 -p ${var.jump_port} '${var.jump_host}' >> ~/.ssh/known_hosts 2>/dev/null
      %{endif~}
      mkdir -p "$extra/root/.ssh"
      printf '%s\n' "$REPOSE_AUTHORIZED_KEYS" > "$extra/root/.ssh/authorized_keys"
      chmod 700 "$extra/root/.ssh"
      chmod 600 "$extra/root/.ssh/authorized_keys"

      # Positional parameters rather than a bash array, because "$@" has no
      # braces for OpenTofu's template interpolation to take an interest in.
      set -- \
        --flake '${var.flake_path}#${var.flake_attr}' \
        --extra-files "$extra" \
        --chown /root/.ssh 0:0 \
        --build-on ${var.build_on} \
        -i '${var.ssh_private_key_path}' \
        ${local.ssh_option_flags} \
        --post-kexec-ssh-port 22

      # The image the target kexecs into. With no --kexec at all,
      # nixos-anywhere has the *target* download a fixed upstream release
      # asset (nixos-images nixos-25.05, Linux 6.14.10) whose restore-network
      # writes one MAC-matched DHCP unit per link. On Azure accelerated
      # networking the synthetic NIC and the SR-IOV VF share one MAC, so each
      # of those units matches both links and the installer DHCPs the VF as
      # though it were a second NIC. Those same bytes installed host-01,
      # host-stg-01 and host-02, and on 2026-10-26 they left host-02 in a
      # discovery loop that never converged.
      #
      # So --kexec is always passed, and no value of kexec_image asks for
      # that asset. A default that selects a known-broken artifact is not a
      # default; someone who truly wants the upstream image can pass its URL,
      # which is explicit and shows up in a diff. Null here would mean the
      # fix applied only when an operator remembered a variable, which is the
      # same shape of protection that already failed once this week.
      #
      # Unset means ours, built here. Set means that path or URL verbatim:
      # how an image is pinned while bisecting an install, and how a
      # published tarball will be used once there is somewhere to publish it
      # (a URL is fetched by the target directly, where a local path is
      # uploaded through the jump host).
      #
      # --out-link rather than --no-link: the image is half a gigabyte with
      # no GC root of its own, and a concurrent nix-collect-garbage between
      # the build and the upload would take it. The link lives in a temp
      # directory the trap above removes, so the root lasts exactly as long
      # as this install needs it.
      #
      # This build is not the preflight. modules/environment builds the same
      # attribute before any VM exists and orders every machine behind it, so
      # a flake that does not build fails with nothing created; by the time
      # this runs it is a cache hit.
      # The build is under its own `timeout` for the same reason the install
      # below is: it is the other way this step can take the state lock and
      # never give it back. A warm store makes it seconds, but a substituter
      # that accepts a connection and then stops answering makes it forever,
      # and `nix build` has no deadline of its own.
      %{if var.kexec_image != null~}
      kexec='${var.kexec_image}'
      %{else~}
      brc=0
      timeout --kill-after=1m '${var.install_deadline}' \
        nix build --out-link "$imgroot/image" '${var.flake_path}#${var.kexec_flake_attr}' || brc=$?
      if [ "$brc" = 124 ] || [ "$brc" = 137 ]; then
        echo "building ${var.flake_path}#${var.kexec_flake_attr} for ${var.name} passed the ${var.install_deadline} install_deadline and was stopped. Nothing was installed and no machine was touched. A cached build is seconds, so this is a substituter that stopped answering or a cold store with no cache; run the build by hand to see which." >&2
        exit "$brc"
      fi
      [ "$brc" = 0 ] || exit "$brc"
      # --kexec wants the tarball, not the directory holding it, and reads
      # the extension to know how to decompress it.
      kexec=$(echo "$imgroot/image"/*.tar.gz)
      if [ ! -f "$kexec" ]; then
        echo "${var.flake_path}#${var.kexec_flake_attr} built no .tar.gz: that attribute must build the kexec installer tarball." >&2
        exit 1
      fi
      %{endif~}
      set -- "$@" --kexec "$kexec"

      # coreutils `timeout`, because nothing else here can bound this.
      # nixos-anywhere waits for the installer to come back with
      # "until runSsh -o ConnectTimeout=10 -- exit 0; do sleep 5; done": an
      # unbounded loop with no flag to bound it. OpenTofu cannot bound it
      # either -- local-exec takes no timeout argument (tofu: An argument
      # named "timeout" is not expected here) -- and connect_timeout reaches
      # only the SSH handshakes of the steps either side of this one. So
      # nothing bounded it, and on 2026-10-26 that loop held this resource,
      # and with it prod's single state lock, for 16h56m while host-02's
      # installer DHCPed a VF that was never going to answer. A held prod
      # lock blocks every emergency apply there is: the edge, the control
      # plane, DNS, the vaults.
      rc=0
      timeout --kill-after=1m '${var.install_deadline}' \
        nixos-anywhere "$@" '${var.target_user}@${var.target_host}' || rc=$?

      # 124 is the deadline; 137 is the deadline plus the --kill-after KILL.
      # Both have to read differently from the installer failing on its own,
      # because the two want opposite things from the operator: a deadline
      # reached with the installer alive is a stuck install to go and look
      # at, and only an install that was genuinely still working wants a
      # longer install_deadline.
      if [ "$rc" = 124 ] || [ "$rc" = 137 ]; then
        echo "nixos-anywhere on ${var.name} was stopped at the ${var.install_deadline} install_deadline. It did not fail; it ran out of time, and ${var.name} is still on the installer or on Ubuntu." >&2
        echo "Two read-only checks say which, and neither needs the machine:" >&2
        echo "  az vm boot-diagnostics get-boot-log --name ${var.name} --resource-group ${var.resource_group_name == "" ? "<resource group>" : var.resource_group_name}" >&2
        echo "  the VM's per-minute Network In and Network Out Total: a byte-exact flat count means the installer is up and has no usable address" >&2
        echo "docs/ops/RUNBOOK.md, \"Install hit the deadline\"." >&2
      fi
      exit $rc
    EOT
  }
}

# Everything after the reboot into NixOS. The connection port is the installed
# system's sshd port, which is not 22 on the edge (22 is the user gateway).
resource "terraform_data" "post_install" {
  count = length(var.post_install_commands) > 0 ? 1 : 0

  triggers_replace = [var.triggers, var.post_install_triggers]

  depends_on = [terraform_data.install]

  connection {
    type                = "ssh"
    host                = var.target_host
    user                = "root"
    port                = var.post_install_port
    private_key         = file(var.ssh_private_key_path)
    timeout             = var.connect_timeout
    bastion_host        = var.jump_host
    bastion_user        = var.jump_host == null ? null : var.jump_user
    bastion_port        = var.jump_host == null ? null : var.jump_port
    bastion_private_key = var.jump_host == null ? null : file(var.ssh_private_key_path)
  }

  provisioner "remote-exec" {
    inline = concat(
      [
        # Fail loudly if the reboot came up on something other than NixOS:
        # a half-finished kexec leaves Ubuntu running and every later step
        # would silently configure the wrong system.
        "grep -q '^ID=nixos' /etc/os-release || { echo 'nixos-anywhere did not leave NixOS on ${var.name}' >&2; exit 1; }",
      ],
      var.post_install_commands,
    )
  }
}
