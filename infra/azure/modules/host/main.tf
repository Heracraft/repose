# One repose host: an Intel Dsv5 VM with no public IP, a Premium SSD v2 data
# disk that becomes vg-guests, and NixOS installed over the Ubuntu image.
#
# docs/DESIGN.md §4, docs/interfaces/host-conventions.md,
# docs/workstreams/11-infra-opentofu.md §2 and §5.
#
# The variable surface is deliberately provider-neutral (name, join_token,
# class, data_disk_gb; outputs private_ip and ssh_jump) so a hetzner_host
# module slots into the root module's for_each without touching anything else
# (docs/workstreams/11-infra-opentofu.md §5, "Provider portability").

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
  tags = merge(var.tags, {
    "repose:role"      = "host"
    "repose:host-name" = var.name
    "repose:class"     = var.class
  })

  # Premium SSD v2 throughput is capped at a quarter of provisioned IOPS; a
  # plan that asks for more is rejected by Azure at apply time, so catch it here.
  max_mbps = floor(var.data_disk_iops / 4)
}

resource "azurerm_network_interface" "main" {
  name                = "nic-${var.name}"
  resource_group_name = var.resource_group_name
  location            = var.location
  tags                = local.tags

  ip_configuration {
    name                          = "internal"
    subnet_id                     = var.subnet_id
    private_ip_address_allocation = "Dynamic"
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

  # security_type Standard. Trusted Launch and Confidential VMs disable
  # nested virtualization, and without nested virtualization there is no
  # /dev/kvm and no guests at all (docs/DESIGN.md §4). azurerm leaves both of
  # these false unless asked, which is the opposite of the portal's default,
  # so they are written out.
  secure_boot_enabled = false
  vtpm_enabled        = false

  custom_data = base64encode(templatefile("${path.module}/templates/cloud-init.yaml.tftpl", {
    authorized_keys = join("\n", var.authorized_keys)
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

  identity {
    type         = "UserAssigned"
    identity_ids = [var.snapshot_identity_id]
  }

  # Serial console output is the only way to see a kexec that never came back.
  boot_diagnostics {}

  lifecycle {
    precondition {
      condition     = var.security_type == "Standard"
      error_message = "Host ${var.name}: security_type must be Standard; Trusted Launch and Confidential VMs disable nested virtualization (docs/DESIGN.md §4)."
    }
    precondition {
      condition     = can(regex("^Standard_D[0-9]+(l|d|ld)?s_v[567]$", var.size))
      error_message = "Host ${var.name}: size ${var.size} is not an Intel Dsv5/v6/v7 size. AMD (Da*, *as_v*) and ARM sizes have no usable nested virtualization (docs/DESIGN.md §4, DECISIONS I-14)."
    }
    precondition {
      condition     = var.zone != null
      error_message = "Host ${var.name}: a zone is required because a Premium SSD v2 data disk only attaches to a VM in the same zone."
    }
    ignore_changes = [
      # The Ubuntu image is a delivery vehicle; by the time anyone reads this
      # the VM is running NixOS from the flake. A newer marketplace version
      # must not re-image a host holding tenant volumes.
      source_image_reference,
      custom_data,
    ]
  }
}

# The guest thin pool lives here. Losing it loses every project on this host
# that has not been snapshotted, so it is a separate resource that a VM
# replacement does not touch.
resource "azurerm_managed_disk" "data" {
  name                 = "datadisk-${var.name}"
  resource_group_name  = var.resource_group_name
  location             = var.location
  zone                 = var.zone
  storage_account_type = "PremiumV2_LRS"
  create_option        = "Empty"
  disk_size_gb         = var.data_disk_gb
  disk_iops_read_write = var.data_disk_iops
  disk_mbps_read_write = var.data_disk_mbps
  tags                 = local.tags

  lifecycle {
    prevent_destroy = true

    precondition {
      condition     = var.data_disk_mbps <= local.max_mbps
      error_message = "Host ${var.name}: data_disk_mbps (${var.data_disk_mbps}) exceeds a quarter of data_disk_iops (${var.data_disk_iops}); Azure rejects Premium SSD v2 disks above that ratio."
    }
  }
}

resource "azurerm_virtual_machine_data_disk_attachment" "data" {
  managed_disk_id    = azurerm_managed_disk.data.id
  virtual_machine_id = azurerm_linux_virtual_machine.main.id
  lun                = var.data_disk_lun
  caching            = "None" # Premium SSD v2 does not support host caching.

  lifecycle {
    # Detaching is how a host loses every tenant volume at once. A plan that
    # wants to is a mistake; make it need a reviewed edit of this block.
    prevent_destroy = true
  }
}

module "install" {
  source = "../nixos_anywhere"

  name                 = var.name
  flake_path           = var.flake_path
  flake_attr           = var.flake_attr
  target_host          = azurerm_network_interface.main.private_ip_address
  ssh_private_key_path = var.ssh_private_key_path
  authorized_keys      = var.authorized_keys
  build_on             = var.build_on
  # Only so the deadline message can print a boot-diagnostics command the
  # operator can paste.
  resource_group_name = var.resource_group_name

  # The image this host is installed through, and the bound on the install.
  # Deliberately absent from `triggers` below: the image is an input to how a
  # host is built, not a property of the host that exists. In triggers, the
  # next change to it -- a nixpkgs bump, a kernel pin, a fix to
  # nix/installer -- would reinstall every host in the environment, host-01
  # included, with tenant guests on it. Nothing about a new installer image
  # asks for a running host to be rebuilt.
  kexec_image      = var.kexec_image
  kexec_flake_attr = var.kexec_flake_attr
  install_deadline = var.install_deadline

  jump_host = var.jump_host
  jump_user = var.jump_user
  jump_port = var.jump_port

  # The flake's host configuration is what makes /dev/kvm usable; if the size
  # or security type was wrong the apply must fail here rather than leave a
  # host that registers and then refuses every placement.
  post_install_commands = [
    "test -e /dev/kvm || { echo 'no /dev/kvm on ${var.name}: wrong VM size or security_type (docs/DESIGN.md §4)' >&2; exit 1; }",
    "test \"$(cat /sys/module/kvm_intel/parameters/nested)\" = Y || { echo 'nested virtualization disabled on ${var.name}' >&2; exit 1; }",
    "test -e ${var.data_disk_device} || { echo 'data disk pool ${var.data_disk_device} missing on ${var.name}' >&2; exit 1; }",
  ]

  triggers = {
    vm_id      = azurerm_linux_virtual_machine.main.id
    flake_attr = var.flake_attr
    disk_id    = azurerm_virtual_machine_data_disk_attachment.data.id
  }

  depends_on = [
    azurerm_virtual_machine_data_disk_attachment.data,
    var.network_ready,
  ]
}

# The join token is single-use, expires in 24 hours, and is consumed by
# Register (docs/interfaces/grpc-hostd.md). It travels as file content, never
# as a command line, so it does not reach the apply log; it lands on a tmpfs,
# so it does not reach the disk; and rotating it is a re-run of this resource
# alone (DECISIONS I-20).
#
# The host is reached through the edge because it has no public IP, which is
# also the path the runbook's "Host never registered" entry uses by hand.
resource "terraform_data" "join_token" {
  count = var.join_token == "" ? 0 : 1

  triggers_replace = {
    install    = module.install.post_install_id
    token_hash = sha256(var.join_token)
  }

  connection {
    type                = "ssh"
    host                = azurerm_network_interface.main.private_ip_address
    user                = "root"
    port                = 22
    private_key         = file(var.ssh_private_key_path)
    timeout             = "10m"
    bastion_host        = var.jump_host
    bastion_user        = var.jump_user
    bastion_port        = var.jump_port
    bastion_private_key = file(var.ssh_private_key_path)
  }

  # 0755, not 0700: /run/repose also holds the store export every guest's
  # unprivileged virtiofsd traverses, and repose-host-net's own files. A
  # 0700 directory here left every guest create failing at step 8 with
  # "/run/repose/store-export does not exist" (DECISIONS I-97). The token
  # itself is 0600.
  provisioner "remote-exec" {
    inline = ["install -d -m 0755 -o root -g root /run/repose"]
  }

  provisioner "file" {
    content     = var.join_token
    destination = "/run/repose/join-token"
  }

  provisioner "remote-exec" {
    inline = [
      "chmod 0600 /run/repose/join-token",
      "systemctl try-restart ${var.hostd_unit}",
    ]
  }
}
