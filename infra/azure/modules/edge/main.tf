# The edge: the public face of repose. It runs the SSH gateway on 22, the
# preview-proxy stub on 443, the WireGuard hub on 51820/udp that every host
# dials, and the operator sshd that is the only way into a host.
#
# docs/DESIGN.md §7, docs/workstreams/06-gateway-edge.md §5.1,
# docs/workstreams/11-infra-opentofu.md §2. NixOS rather than Coolify because
# a Coolify port mapping costs an app its rolling deploys and WireGuard wants
# the kernel module (DECISIONS R4-13).

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
  tags = merge(var.tags, { "repose:role" = "edge" })
}

# ssh.repose.herakraft.co points here and every host's WireGuard endpoint is
# this address. Replacing it silently breaks every host's tunnel and every
# user's known SSH target.
resource "azurerm_public_ip" "main" {
  name                = "pip-${var.name}"
  resource_group_name = var.resource_group_name
  location            = var.location
  allocation_method   = "Static"
  sku                 = "Standard"
  zones               = var.zone == null ? null : [var.zone]
  tags                = local.tags

  lifecycle {
    prevent_destroy = true
  }
}

resource "azurerm_network_interface" "main" {
  name                  = "nic-${var.name}"
  resource_group_name   = var.resource_group_name
  location              = var.location
  ip_forwarding_enabled = true
  tags                  = local.tags

  ip_configuration {
    name                          = "public"
    subnet_id                     = var.subnet_id
    private_ip_address_allocation = "Dynamic"
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
    authorized_keys = join("\n", var.authorized_keys)
  }))

  os_disk {
    name                 = "osdisk-${var.name}"
    caching              = "ReadWrite"
    storage_account_type = "Premium_LRS"
    disk_size_gb         = var.os_disk_gb
  }

  source_image_reference {
    publisher = var.image.publisher
    offer     = var.image.offer
    sku       = var.image.sku
    version   = var.image.version
  }

  boot_diagnostics {}

  lifecycle {
    ignore_changes = [source_image_reference, custom_data]
  }
}

module "install" {
  source = "../nixos_anywhere"

  name                 = var.name
  flake_path           = var.flake_path
  flake_attr           = var.flake_attr
  target_host          = azurerm_public_ip.main.ip_address
  ssh_private_key_path = var.ssh_private_key_path
  authorized_keys      = var.authorized_keys
  build_on             = var.build_on
  # Only so the deadline message can print a boot-diagnostics command the
  # operator can paste.
  resource_group_name = var.resource_group_name

  # The edge has the same latent defect as the hosts: it is the same Azure VM
  # shape, with accelerated networking, so its synthetic NIC and its SR-IOV VF
  # share a MAC and the upstream installer's MAC-matched DHCP units claim
  # both. It has simply not been reinstalled since that started mattering. So
  # it is wired here and it inherits the fix the day it is next reinstalled,
  # not before -- and like the hosts, the image is not in `triggers`, because
  # a new installer image must never be a reason to reinstall the edge, which
  # is the jump host and the WireGuard hub every other machine reaches the
  # control plane through.
  kexec_image      = var.kexec_image
  kexec_flake_attr = var.kexec_flake_attr
  install_deadline = var.install_deadline

  # The edge is reached directly; it is itself the jump host for everything else.
  jump_host = null

  # 22 belongs to the user-facing gateway once NixOS is up, so every operator
  # connection after the install goes to the operator sshd.
  post_install_port = var.operator_ssh_port
  post_install_commands = [
    "ss -ltn | grep -q ':${var.operator_ssh_port} ' || { echo 'operator sshd is not listening on ${var.operator_ssh_port}' >&2; exit 1; }",
  ]

  triggers = {
    vm_id      = azurerm_linux_virtual_machine.main.id
    flake_attr = var.flake_attr
  }

  depends_on = [var.network_ready]
}
