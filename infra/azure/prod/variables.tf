variable "subscription_id" {
  type        = string
  description = "Azure subscription holding every repose resource."
}

variable "resource_group_name" {
  type        = string
  description = "Existing resource group (docs/ops/AZURE-SETUP.md step 4)."
  default     = "repose-prod"
}

variable "zone" {
  type        = string
  description = "Availability zone for hosts and their data disks."
  default     = "1"
}

variable "operator_cidrs" {
  type        = list(string)
  description = "Addresses allowed to reach operator SSH."
}

variable "control_web_cidrs" {
  type        = list(string)
  description = "Addresses allowed to reach 80 and 443 on the control plane."
}

variable "operator_authorized_keys" {
  type        = list(string)
  description = "Operator SSH public keys."
}

variable "ssh_private_key_path" {
  type        = string
  description = "Path to the matching private key on the machine running tofu."
}

variable "api_identity_object_id" {
  type        = string
  description = "Entra object id of the api's service principal, or null until it exists."
  default     = null
}

variable "flake_path" {
  type        = string
  description = "Absolute path to the repository's nix/ directory."
}

variable "build_on" {
  type        = string
  description = "nixos-anywhere --build-on: auto, local or remote."
  default     = "auto"
}

variable "hosts" {
  type        = list(string)
  description = "Hosts that exist in production."
  default     = []
}

variable "host_flake_attrs" {
  type        = map(string)
  description = "Per-host nixosConfigurations attribute in nix/flake.nix (for example host-01 = \"host-01\"); a host absent here installs the generic `host`."
  default     = {}
}

variable "join_tokens" {
  type        = map(string)
  description = "host name to single-use registration token. Passed with -var on the apply that adds a host; never committed."
  sensitive   = true
  default     = {}
}

variable "host_size" {
  type        = string
  description = "Azure VM size for hosts."
  default     = "Standard_D16s_v7"
}

variable "host_class" {
  type        = string
  description = "Capacity class recorded on each host."
  default     = "azure-d16s-v7"
}

variable "host_security_type" {
  type        = string
  description = <<-EOT
    Azure security type for hosts. Deliberately has no default, so a plan that
    does not pass the environment's tfvars fails instead of quietly picking
    one; only "Standard" is accepted, because Trusted Launch and Confidential
    VMs disable nested virtualization (docs/DESIGN.md §4).
  EOT
}

variable "host_data_disk_gb" {
  type        = number
  description = "Premium SSD v2 data disk per host."
  default     = 512
}

variable "host_data_disk_iops" {
  type        = number
  description = "Provisioned IOPS on each host's data disk. The first 3,000 are free."
  default     = 16000
}

variable "host_data_disk_mbps" {
  type        = number
  description = "Provisioned throughput MB/s on each host's data disk. The first 125 are free; Azure caps this at a quarter of the IOPS."
  default     = 600
}

variable "host_shapes" {
  type = map(object({
    size           = optional(string)
    class          = optional(string)
    zone           = optional(string)
    data_disk_gb   = optional(number)
    data_disk_iops = optional(number)
    data_disk_mbps = optional(number)
    os_disk_gb     = optional(number)
  }))
  description = "Per-host override of the host values above, keyed by host name (for example host-02 = { size = \"Standard_D64s_v7\" }). A host with no entry takes the globals, and its plan does not change."
  default     = {}
}

variable "edge_operator_ssh_port" {
  type        = number
  description = <<-EOT
    Port the edge's operator sshd listens on; every host provisioner jumps
    through it. 2222 is the target state, because 22 belongs to the
    user-facing SSH gateway. Until workstream 06 gives the edge that gateway,
    nix/edge serves sshd on 22 and this must be 22
    (infra/README.md, "How the installer reaches a host").
  EOT
  default     = 2222
}

variable "edge_size" {
  type        = string
  description = "Edge VM size."
  default     = "Standard_D2s_v7"
}

variable "coolify_count" {
  type        = number
  description = <<-EOT
    Control-plane VMs: 0 or 1. Production runs 1 from wave 3 (DECISIONS I-24,
    I-71); the default stays 0 so a new environment costs nothing until
    somebody asks for a control plane. Setting it back to 0 destroys the VM
    and its OS disk, the platform Postgres included.
  EOT
  default     = 0
}

variable "coolify_size" {
  type        = string
  description = "Control-plane VM size."
  default     = "Standard_D4s_v7"
}

variable "coolify_public_key" {
  type        = string
  description = "SSH public key of the owner's existing Coolify instance; it manages the control VM as a server (DECISIONS I-83)."
}

variable "coolify_manager_cidrs" {
  type        = list(string)
  description = "Addresses the owner's Coolify instance connects from (prod.local.tfvars). Opened on 22 to the control VM."
  default     = []
}

variable "edge_wireguard_public_key" {
  type        = string
  description = "The edge's WireGuard public key, once the edge exists."
  default     = null
}

variable "snapshots_account_name" {
  type        = string
  description = "Storage account guest snapshots are written to."
}

variable "keyvault_name" {
  type        = string
  description = "Key Vault holding the DEK-wrapping key."
}

variable "freeze_keyvault_name" {
  type        = string
  description = "Key Vault for the guest freeze's image-wrapping key (DECISIONS I-653); null creates none."
  default     = null
}

variable "manage_dns" {
  type        = bool
  description = <<-EOT
    Create the Cloudflare records. False until CLOUDFLARE_API_TOKEN and
    cloudflare_zone_id both exist; until then the records are created by hand
    from the table in infra/README.md, "DNS while manage_dns is false".
  EOT
  default     = false
}

variable "cloudflare_zone_id" {
  type        = string
  description = "Cloudflare zone id for herakraft.co."
  default     = null
}

variable "dns_zone" {
  type        = string
  description = "DNS zone the repose names live in."
  default     = "herakraft.co"
}

variable "operator_object_id" {
  type        = string
  description = "Entra object id that keeps the Key Vault operator policy. Null means whoever runs tofu, which moves the policy to a service principal the first time one applies (DECISIONS I-361). Set it in the local tfvars."
  default     = null
}

variable "alerts_enabled" {
  type        = bool
  description = "Create the Azure Monitor alerts (DECISIONS I-648)."
  default     = false
}

variable "alert_email" {
  type        = string
  description = "Where the Azure alerts are emailed. Set it in prod.local.tfvars: the repository is public."
  default     = ""
}
