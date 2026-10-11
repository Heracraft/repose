variable "name" {
  description = "Host name, e.g. host-01. Becomes the VM name, the computer name and the repose:host-name tag."
  type        = string

  validation {
    condition     = can(regex("^host-[0-9a-z-]+$", var.name))
    error_message = "Host names look like host-01; the runbook and the host-registration commands use the same form."
  }
}

variable "class" {
  description = <<-EOT
    Provider-neutral capacity class recorded on the host, e.g. azure-d16s-v5.
    A hetzner_host module uses the same variable with hetzner-ax162r.
  EOT
  type        = string
}

variable "join_token" {
  description = <<-EOT
    Single-use registration token, delivered to /run/repose/join-token over
    SSH after the install. It comes from `hostdev init` until the api exists
    and from `repose-admin hosts add` after (DECISIONS I-17, I-9). Empty skips
    delivery,
    which is what an already-registered host wants; keeping the spent token in
    the (git-ignored) local tfvars file keeps later plans quiet, and replacing
    its value is how the token is rotated.
  EOT
  type        = string
  sensitive   = true
  default     = ""
}

variable "size" {
  description = "Azure VM size. Must be an Intel Dsv5/Dsv6 size (docs/DESIGN.md §4)."
  type        = string
}

variable "security_type" {
  description = <<-EOT
    Azure security type. Only "Standard" is accepted: Trusted Launch and
    Confidential VMs disable nested virtualization. There is no default, so a
    plan that forgets it fails rather than silently building a host with no
    /dev/kvm (docs/workstreams/11-infra-opentofu.md §6).
  EOT
  type        = string

  validation {
    condition     = var.security_type == "Standard"
    error_message = "security_type must be Standard; Trusted Launch and Confidential VMs disable nested virtualization."
  }
}

variable "zone" {
  description = "Availability zone. Required: a Premium SSD v2 disk attaches only within its own zone."
  type        = string
}

variable "resource_group_name" {
  description = "Resource group the host is created in."
  type        = string
}

variable "location" {
  description = "Azure region."
  type        = string
}

variable "subnet_id" {
  description = "Hosts subnet. Has no inbound NSG rules and egresses through a NAT gateway."
  type        = string
}

variable "network_ready" {
  description = "Value to depend on so the host is not created before the subnet's NSG and NAT gateway associations exist."
  type        = any
  default     = null
}

variable "data_disk_gb" {
  description = "Premium SSD v2 data disk size. Becomes vg-guests; 512 pre-launch, 2048 at launch (DECISIONS I-14)."
  type        = number
}

variable "data_disk_iops" {
  description = "Provisioned IOPS on the data disk."
  type        = number
  default     = 16000
}

variable "data_disk_mbps" {
  description = "Provisioned throughput MB/s on the data disk. Azure caps this at a quarter of data_disk_iops."
  type        = number
  default     = 600
}

variable "data_disk_lun" {
  description = "LUN the data disk is attached at."
  type        = number
  default     = 10
}

variable "data_disk_device" {
  description = <<-EOT
    Path checked after the install so a detached disk, a wrong LUN or a
    disko layout that never ran fails the apply instead of surfacing later
    as hostd's pool_missing. The default is the thin pool disko creates on
    the data disk, which is the same on SCSI and NVMe sizes; the device the
    disk appears at is resolved by the host's disko hook (DECISIONS I-41).

    It is the device-mapper name, not /dev/vg-guests/thin: LVM creates the
    volume-group symlink only for volumes something can open, and a thin
    pool is not one of them, so the old default failed every install it was
    meant to guard (host-stg-01, 2026-10-11).
  EOT
  type        = string
  default     = "/dev/mapper/vg--guests-thin"
}

variable "os_disk_gb" {
  description = "OS disk size. Holds /nix/store for the host and every guest closure, so it is not small."
  type        = number
  default     = 256
}

variable "os_disk_type" {
  description = "OS disk SKU."
  type        = string
  default     = "Premium_LRS"
}

variable "image" {
  description = "Ubuntu marketplace image used only as the nixos-anywhere target."
  type = object({
    publisher = string
    offer     = string
    sku       = string
    version   = string
  })
  default = {
    publisher = "Canonical"
    offer     = "ubuntu-24_04-lts"
    sku       = "server"
    version   = "latest"
  }
}

variable "admin_username" {
  description = "Cloud-init user on the Ubuntu image. Gone after the install."
  type        = string
  default     = "azureuser"
}

variable "snapshot_identity_id" {
  description = "User-assigned managed identity hostd uses to write snapshots to Blob."
  type        = string
}

variable "flake_path" {
  description = "Absolute path to the directory holding nix/flake.nix."
  type        = string
}

variable "flake_attr" {
  description = "nixosConfigurations attribute installed on this host."
  type        = string
  default     = "host"
}

variable "ssh_private_key_path" {
  description = "Operator private key used by the provisioners."
  type        = string
}

variable "authorized_keys" {
  description = "Operator public keys. The first is also the VM's admin key."
  type        = list(string)
}

variable "jump_host" {
  description = "Edge public address; hosts have no public IP so every provisioner goes through it."
  type        = string
}

variable "jump_user" {
  description = "User on the edge."
  type        = string
  default     = "root"
}

variable "jump_port" {
  description = "Operator sshd port on the edge (22 is the user gateway)."
  type        = number
  default     = 2222
}

variable "build_on" {
  description = "nixos-anywhere --build-on: auto, local or remote."
  type        = string
  default     = "auto"
}

variable "hostd_unit" {
  description = "systemd unit restarted after the join token is written (docs/interfaces/host-conventions.md)."
  type        = string
  default     = "hostd.service"
}

variable "tags" {
  description = "Tags applied to every resource in this module."
  type        = map(string)
  default     = {}
}
