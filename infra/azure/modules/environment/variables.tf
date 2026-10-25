# --- identity of the environment -------------------------------------------

variable "env" {
  type        = string
  description = "Environment name: prod or staging. Appears in resource names and the repose:env tag."
}

variable "resource_group_name" {
  type        = string
  description = "Existing resource group, created by infra/bootstrap. Read, never managed (DECISIONS I-19)."
}

variable "zone" {
  type        = string
  description = "Availability zone for hosts and their Premium SSD v2 data disks. Both must be in the same zone."
  default     = "1"
}

# --- network ---------------------------------------------------------------

variable "vnet_cidr" {
  type        = string
  description = "VNet address space."
  default     = "10.200.0.0/16"
}

variable "hosts_subnet_cidr" {
  type        = string
  description = "Hosts subnet. Not to be confused with 10.64.0.0/12, which is the guests' space inside each host."
  default     = "10.200.1.0/24"
}

variable "edge_subnet_cidr" {
  type        = string
  description = "Edge subnet."
  default     = "10.200.2.0/24"
}

variable "control_subnet_cidr" {
  type        = string
  description = "Control-plane subnet."
  default     = "10.200.3.0/24"
}

variable "operator_cidrs" {
  type        = list(string)
  description = "Addresses allowed to reach operator SSH on the edge and the control plane."
}

variable "control_web_cidrs" {
  type        = list(string)
  description = "Addresses allowed to reach 80 and 443 on the control plane. The operator list pre-launch, 0.0.0.0/0 at launch."
}

variable "edge_operator_ssh_port" {
  type        = number
  description = "Operator sshd port on the edge; 22 is the user-facing gateway."
  default     = 2222
}

# --- operator access -------------------------------------------------------

variable "operator_authorized_keys" {
  type        = list(string)
  description = "Operator SSH public keys. Written to /root/.ssh/authorized_keys on every machine; the first is also each VM's Azure admin key."

  validation {
    condition     = length(var.operator_authorized_keys) > 0
    error_message = "At least one operator public key is required."
  }
}

variable "ssh_private_key_path" {
  type        = string
  description = "Path to the matching private key on the machine running tofu. Read by the provisioners; never stored in state."
}

variable "operator_object_id" {
  type        = string
  description = "Entra object id granted key management on the vault. Defaults to whoever runs tofu."
  default     = null
}

variable "api_identity_object_id" {
  type        = string
  description = "Entra object id of the api's service principal; gets wrap and unwrap on the DEK-wrapping key and Storage Blob Data Contributor on the snapshots container. Null until it exists."
  default     = null
}

# --- nix -------------------------------------------------------------------

variable "flake_path" {
  type        = string
  description = "Absolute path to the repository's nix/ directory, which holds flake.nix."
}

variable "edge_flake_attr" {
  type        = string
  description = "nixosConfigurations attribute installed on the edge."
  default     = "edge"
}

variable "host_flake_attr_default" {
  type        = string
  description = <<-EOT
    nixosConfigurations attribute installed on a host with no entry in
    host_flake_attrs. The flake ships one `host` configuration today because a
    host takes its guest CIDR and WireGuard keys from Register rather than
    from its Nix config (docs/workstreams/11-infra-opentofu.md §5).
  EOT
  default     = "host"
}

variable "host_flake_attrs" {
  type        = map(string)
  description = "Per-host override of the flake attribute, for when workstream 01 gives a host its own configuration."
  default     = {}
}

variable "build_on" {
  type        = string
  description = "nixos-anywhere --build-on: auto, local or remote."
  default     = "auto"
}

# --- hosts -----------------------------------------------------------------

variable "hosts" {
  type        = list(string)
  description = "Host names to exist in this environment, e.g. [\"host-01\"]. Shrinking this list destroys a host; drain and retire it first (infra/README.md)."
  default     = []
}

variable "join_tokens" {
  type        = map(string)
  description = <<-EOT
    host name to single-use registration token. For M1 the token comes from
    `hostdev init`, the one-host dev driver that plays the api side of the
    gRPC contract until the api exists (DECISIONS I-17); once the api is up it
    comes from `repose-admin hosts add` (I-9). Never committed.
  EOT
  sensitive   = true
  default     = {}
}

variable "host_size" {
  type        = string
  description = "Azure VM size for hosts. Standard_D16s_v7 pre-launch, Standard_D64s_v7 at launch (DECISIONS I-14)."
  default     = "Standard_D16s_v7"

  validation {
    condition     = can(regex("^Standard_D[0-9]+(l|d|ld)?s_v[567]$", var.host_size))
    error_message = "host_size must be an Intel Dsv5/Dsv6 size. AMD sizes carry an `a` (Da*, *as_v*) and have 50 to 90 percent nested-virtualization penalties; ARM sizes have none at all (docs/DESIGN.md §4)."
  }
}

variable "host_security_type" {
  type        = string
  description = "Azure security type for hosts. Only Standard works; Trusted Launch disables nested virtualization."
  default     = "Standard"

  validation {
    condition     = var.host_security_type == "Standard"
    error_message = "host_security_type must be Standard."
  }
}

variable "host_class" {
  type        = string
  description = "Provider-neutral capacity class recorded on each host."
  default     = "azure-d16s-v7"
}

variable "host_data_disk_gb" {
  type        = number
  description = "Premium SSD v2 data disk per host; becomes vg-guests. 512 pre-launch, 2048 at launch (DECISIONS I-14)."
  default     = 512
}

variable "host_data_disk_iops" {
  type        = number
  description = "Provisioned IOPS on each host's data disk."
  default     = 16000
}

variable "host_data_disk_mbps" {
  type        = number
  description = "Provisioned throughput MB/s on each host's data disk."
  default     = 600
}

variable "host_os_disk_gb" {
  type        = number
  description = "Host OS disk. Holds /nix/store, which every guest's closure is built into and shared from over virtio-fs."
  default     = 256
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
  description = <<-EOT
    Per-host override of the host values above, keyed by host name. An
    attribute a host does not name takes the global; a host with no entry at
    all is exactly what it was before this variable existed. That is what
    lets one apply add a host of a different size without touching the hosts
    already carrying guests.
  EOT
  default     = {}

  validation {
    condition = alltrue([
      for shape in var.host_shapes :
      shape.size == null || can(regex("^Standard_D[0-9]+(l|d|ld)?s_v[567]$", coalesce(shape.size, "")))
    ])
    error_message = "Every size in host_shapes must be an Intel Dsv5/Dsv6/Dsv7 size. AMD sizes carry an `a` (Da*, *as_v*) and have 50 to 90 percent nested-virtualization penalties; ARM sizes have no nested virtualization at all (docs/DESIGN.md §4)."
  }
}

# --- edge and control plane ------------------------------------------------

variable "edge_name" {
  type        = string
  description = "Edge VM name."
  default     = "edge-01"
}

variable "edge_size" {
  type        = string
  description = "Edge VM size."
  default     = "Standard_D2s_v7"
}

variable "edge_wireguard_public_key" {
  type        = string
  description = "The edge's WireGuard public key, read from the edge after its first install. Null leaves the control plane's tunnel down."
  default     = null
}

variable "coolify_count" {
  type        = number
  description = <<-EOT
    How many control-plane VMs to create: 0 or 1. Default 0. The api, the
    dashboard and Logto arrive in wave 3 (workstreams 05 and 08); before they
    exist the VM bills about $180 a month for nothing, while the edge and the
    first host are worth paying for early (DECISIONS I-24). Setting this to 0
    after the VM exists destroys it and everything on its OS disk, Postgres
    included. The VM is a server of the owner's existing Coolify, not a
    Coolify install of its own (DECISIONS I-83).
  EOT
  default     = 0

  validation {
    condition     = contains([0, 1], var.coolify_count)
    error_message = "coolify_count is 0 or 1; there is one control plane."
  }
}

variable "coolify_name" {
  type        = string
  description = "Control-plane VM name."
  default     = "coolify-01"
}

variable "coolify_size" {
  type        = string
  description = "Control-plane VM size."
  default     = "Standard_D4s_v7"
}

variable "coolify_os_disk_gb" {
  type        = number
  description = "Control-plane OS disk; holds the platform Postgres and every image layer Coolify deploys."
  default     = 256
}

variable "coolify_public_key" {
  type        = string
  description = "SSH public key of the owner's existing Coolify instance, which manages the control VM as a server (DECISIONS I-83). Required when coolify_count is 1."
  default     = null
}

variable "coolify_manager_cidrs" {
  type        = list(string)
  description = "Addresses the owner's Coolify instance connects from; opened on 22 to the control VM next to operator_cidrs. Empty until the owner supplies them, and then Coolify cannot reach the server."
  default     = []
}

# --- storage and secrets ---------------------------------------------------

variable "snapshots_account_name" {
  type        = string
  description = "Globally unique storage account name for guest snapshots."
}

variable "snapshot_tier_to_cool_days" {
  type        = number
  description = "Days after which a snapshot blob moves to the cool tier."
  default     = 7
}

variable "snapshot_delete_after_days" {
  type        = number
  description = "Days after which a snapshot blob is deleted. The backstop behind the api's own retention."
  default     = 45
}

variable "keyvault_name" {
  type        = string
  description = "Globally unique Key Vault name."
}

variable "freeze_keyvault_name" {
  type        = string
  description = <<-EOT
    Key Vault for the guest freeze's image-wrapping key (DECISIONS I-653),
    a vault of its own because access policies are vault-wide: the hosts
    get Get and UnwrapKey here, and nothing on the vault that wraps users'
    secret keys. Null creates nothing; hosts then cannot freeze guests.
  EOT
  default     = null
}

# --- DNS -------------------------------------------------------------------

variable "manage_dns" {
  type        = bool
  description = <<-EOT
    Create the Cloudflare records. Default false, because it takes a
    CLOUDFLARE_API_TOKEN in the environment and a zone id, and a plan without
    them fails inside the provider rather than in a variable. Turn it on in
    the environment's local tfvars on the first apply that has a token; until
    then the records are created by hand from the table in infra/README.md,
    "DNS while manage_dns is false".
  EOT
  default     = false
}

variable "cloudflare_zone_id" {
  type        = string
  description = "Cloudflare zone id for the DNS zone below. Required when manage_dns is true."
  default     = null
}

variable "dns_zone" {
  type        = string
  description = "DNS zone the names live in."
  default     = "herakraft.co"
}

variable "dns_prefix" {
  type        = string
  description = "Label under the zone: repose.herakraft.co, api.repose.herakraft.co, and so on (DECISIONS R4-12, I-15)."
  default     = "repose"
}

variable "proxy_web_records" {
  type        = bool
  description = <<-EOT
    Put the dashboard, api and Logto records behind Cloudflare's proxy. False
    by default: Coolify terminates TLS itself with Let's Encrypt, and a
    proxied record changes which certificate a user sees and hides client
    addresses from the api's rate limits.
  EOT
  default     = false
}

variable "tags" {
  type        = map(string)
  description = "Extra tags merged into every resource."
  default     = {}
}

# --- Alerts (DECISIONS I-648) ------------------------------------------------

variable "alerts_enabled" {
  type        = bool
  description = <<-EOT
    Create the Azure Monitor action group and alerts in alerts.tf. True in
    prod. False by default, so staging, whose VMs come and go, never emails
    the owner.
  EOT
  default     = false
}

variable "alert_email" {
  type        = string
  description = "Where the Azure alerts are emailed. Required when alerts_enabled is true. It is a personal address in a public repository, so it goes in the environment's local tfvars."
  default     = ""
}
