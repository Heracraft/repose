variable "zone_id" {
  type        = string
  description = "Cloudflare zone id for the environment's DNS zone."

  # This module is only instantiated when manage_dns is true, so a null or
  # placeholder zone id here means somebody turned DNS on without the two
  # things it needs. Failing in the variable names both; failing inside the
  # provider produces an authentication error that names neither.
  validation {
    condition     = var.zone_id != null && can(regex("^[0-9a-f]{32}$", var.zone_id))
    error_message = "manage_dns is true, so cloudflare_zone_id must be the 32-character zone id for the zone, and CLOUDFLARE_API_TOKEN must be set in the environment (infra/README.md, \"DNS\")."
  }
}

variable "records" {
  description = <<-EOT
    Fully qualified name to address. `proxied` must stay false for the
    `ssh.` name: Cloudflare's proxy does not carry SSH, and a proxied
    record would hide the edge's real address from every host's WireGuard
    endpoint as well.
  EOT
  type = map(object({
    address = string
    proxied = optional(bool, false)
    comment = optional(string, "managed by repose infra/dns")
  }))

  validation {
    condition = alltrue([
      for name, r in var.records : !r.proxied if startswith(name, "ssh.")
    ])
    error_message = "The ssh. record must not be proxied; Cloudflare's proxy does not carry SSH or WireGuard."
  }
}

variable "ttl" {
  type        = number
  description = "TTL in seconds for unproxied records. Short, because a rebuilt edge or control plane changes an address."
  default     = 300
}
