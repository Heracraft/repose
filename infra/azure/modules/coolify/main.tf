# The control-plane VM: Logto, Postgres, and the api and dashboard as
# single-container Coolify applications (DECISIONS R4-2, R5-11), deployed onto
# it by the owner's existing Coolify instance, which manages this machine as a
# server over SSH (DECISIONS I-83). Coolify itself does not run here.
#
# Unlike the host and the edge this VM stays Ubuntu and is never touched by
# nixos-anywhere, because Coolify rejects NixOS as a managed server.

terraform {
  required_version = ">= 1.6.0"
  required_providers {
    azurerm = {
      source  = "hashicorp/azurerm"
      version = ">= 4.0, < 5.0"
    }
  }
}

locals {
  tags = merge(var.tags, { "repose:role" = "control" })
}

resource "azurerm_public_ip" "main" {
  name                = "pip-${var.name}"
  resource_group_name = var.resource_group_name
  location            = var.location
  allocation_method   = "Static"
  sku                 = "Standard"
  zones               = var.zone == null ? null : [var.zone]
  tags                = local.tags

  lifecycle {
    # repose.run and api.repose.run resolve here; a replacement is a
    # DNS outage.
    prevent_destroy = true
  }
}

resource "azurerm_network_interface" "main" {
  name                = "nic-${var.name}"
  resource_group_name = var.resource_group_name
  location            = var.location
  tags                = local.tags

  ip_configuration {
    name      = "public"
    subnet_id = var.subnet_id
    # Static: hosts dial the api's gRPC listener at this address before they
    # have a WireGuard tunnel (DECISIONS I-92), and it is written into their
    # NixOS configuration (nix/hosts/host-01.nix repose.host.apiAddr).
    private_ip_address_allocation = var.private_ip == null ? "Dynamic" : "Static"
    private_ip_address            = var.private_ip
    public_ip_address_id          = azurerm_public_ip.main.id
  }
}

resource "azurerm_linux_virtual_machine" "main" {
  name                = var.name
  computer_name       = var.name
  resource_group_name = var.resource_group_name
  location            = var.location
  size                = var.size
  zone                = var.zone
  tags                = local.tags

  network_interface_ids = [azurerm_network_interface.main.id]

  admin_username                  = var.admin_username
  disable_password_authentication = true

  admin_ssh_key {
    username   = var.admin_username
    public_key = var.authorized_keys[0]
  }

  secure_boot_enabled = false
  vtpm_enabled        = false

  custom_data = base64encode(templatefile("${path.module}/templates/cloud-init.yaml.tftpl", {
    authorized_keys       = join("\n", var.authorized_keys)
    coolify_public_key    = var.coolify_public_key
    edge_public_key       = var.edge_wireguard_public_key == null ? "" : var.edge_wireguard_public_key
    edge_endpoint         = var.edge_wireguard_endpoint
    wireguard_address     = var.wireguard_address
    wireguard_allowed_ips = join(", ", var.wireguard_allowed_ips)
  }))

  os_disk {
    name                 = "osdisk-${var.name}"
    caching              = "ReadWrite"
    storage_account_type = var.os_disk_type
    disk_size_gb         = var.os_disk_gb
  }

  source_image_reference {
    publisher = var.image.publisher
    offer     = var.image.offer
    sku       = var.image.sku
    version   = var.image.version
  }

  boot_diagnostics {}

  depends_on = [var.network_ready]

  lifecycle {
    # The platform Postgres lives on this VM's OS disk. Re-imaging it because
    # the marketplace published a new Ubuntu version, or because a WireGuard
    # key changed in cloud-init, would destroy the control plane. Rebuilding
    # it is a deliberate, documented restore from the R2 dump
    # (infra/README.md).
    ignore_changes = [source_image_reference, custom_data]

    precondition {
      condition     = length(var.authorized_keys) > 0
      error_message = "The Coolify VM needs at least one operator public key; there is no other way in."
    }
  }
}

# An apply that returns before the machine is ready leaves the operator with
# a public IP and a Coolify "Validate & configure" that fails for a reason
# only visible on the VM. This checks what Coolify's validation checks (root
# login by the key it holds, Docker with the compose plugin) plus the two
# restore commands the runbook opens with.
#
# It is deliberately not tied to custom_data: the VM ignores custom_data
# changes (see the lifecycle block above), so a template edit must not look
# like a reason to re-check a machine that is already serving.
resource "terraform_data" "ready" {
  triggers_replace = {
    vm_id = azurerm_linux_virtual_machine.main.id
  }

  connection {
    type        = "ssh"
    host        = azurerm_public_ip.main.ip_address
    user        = "root"
    port        = 22
    private_key = file(var.ssh_private_key_path)
    timeout     = var.connect_timeout
  }

  provisioner "remote-exec" {
    inline = [
      "cloud-init status --wait >/dev/null 2>&1 || true",
      "cloud-init status | grep -q 'status: done' || { echo 'cloud-init did not finish cleanly on ${var.name}; see /var/log/cloud-init-output.log' >&2; cloud-init status --long >&2; exit 1; }",
      "command -v docker >/dev/null || { echo 'docker is not installed on ${var.name}; see /var/log/cloud-init-output.log' >&2; exit 1; }",
      "systemctl is-active --quiet docker || { echo 'docker is installed but not running on ${var.name}' >&2; exit 1; }",
      "docker compose version >/dev/null 2>&1 || { echo 'the docker compose plugin is missing; Coolify validation needs it' >&2; exit 1; }",
      "command -v tailscale >/dev/null || { echo 'tailscale is not installed on ${var.name}; the owner cannot join it to the tailnet' >&2; exit 1; }",
      # The key Coolify will connect with. If it is not here, Coolify's
      # "Validate & configure" fails with a permission error that looks like
      # a firewall problem.
      "grep -qF '${trimspace(var.coolify_public_key)}' /root/.ssh/authorized_keys || { echo 'coolify_public_key is not in root authorized_keys on ${var.name}' >&2; exit 1; }",
    ]
  }
}
