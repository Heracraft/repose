variable "name" {
  description = "Edge VM name."
  type        = string
  default     = "edge-01"
}

variable "size" {
  description = <<-EOT
    Azure VM size. Default Standard_D2s_v7 per the size note at the top of
    docs/workstreams/11-infra-opentofu.md. A burstable B-series size is
    cheaper but throttles once its credits are gone, and the thing that would
    throttle is every user's SSH session.
  EOT
  type        = string
  default     = "Standard_D2s_v7"
}

variable "zone" {
  description = "Availability zone, or null for regional."
  type        = string
  default     = null
}

variable "resource_group_name" {
  type        = string
  description = "Resource group the edge is created in."
}

variable "location" {
  type        = string
  description = "Azure region."
}

variable "subnet_id" {
  type        = string
  description = "Edge subnet; its NSG allows 22, 443, 51820/udp and the operator SSH port."
}

variable "network_ready" {
  description = "Value to depend on so the edge is not created before its subnet's NSG association exists."
  type        = any
  default     = null
}

variable "os_disk_gb" {
  type        = number
  description = "OS disk size."
  default     = 64
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
  type        = string
  description = "Cloud-init user on the Ubuntu image. Gone after the install."
  default     = "azureuser"
}

variable "operator_ssh_port" {
  type        = number
  description = "Port the installed system's operator sshd listens on."
  default     = 2222
}

variable "flake_path" {
  type        = string
  description = "Absolute path to the directory holding nix/flake.nix."
}

variable "flake_attr" {
  type        = string
  description = "nixosConfigurations attribute installed on the edge."
  default     = "edge"
}

variable "ssh_private_key_path" {
  type        = string
  description = "Operator private key used by the provisioners."
}

variable "authorized_keys" {
  type        = list(string)
  description = "Operator public keys. The first is also the VM's admin key."
}

variable "build_on" {
  type        = string
  description = "nixos-anywhere --build-on: auto, local or remote."
  default     = "auto"
}

variable "kexec_image" {
  type        = string
  description = "Kexec installer tarball nixos-anywhere boots the edge into, as a local path or a URL; null passes no --kexec. Not a trigger: see the install module call."
  default     = null
}

variable "kexec_flake_attr" {
  description = "packages attribute of flake_path that builds the kexec installer image when kexec_image is null. Passed down so the environment's preflight and this machine's install build the same attribute."
  type        = string
  default     = "kexec-installer"
}

variable "install_deadline" {
  type        = string
  description = "Wall-clock bound on the nixos-anywhere run (coreutils timeout duration). infra/azure/modules/nixos_anywhere/variables.tf has the why."
  default     = "45m"
}

variable "tags" {
  type        = map(string)
  description = "Tags applied to every resource in this module."
  default     = {}
}
