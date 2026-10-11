variable "name" {
  type        = string
  description = "Key Vault name; globally unique, 3 to 24 characters."

  validation {
    condition     = can(regex("^[a-zA-Z][a-zA-Z0-9-]{1,22}[a-zA-Z0-9]$", var.name))
    error_message = "Key Vault names are 3 to 24 characters of letters, digits and hyphens, starting with a letter."
  }
}

variable "resource_group_name" {
  type        = string
  description = "Resource group the vault is created in."
}

variable "location" {
  type        = string
  description = "Azure region."
}

variable "tenant_id" {
  type        = string
  description = "Entra tenant the vault authenticates against."
}

variable "sku_name" {
  type        = string
  description = "standard (software-protected keys) or premium (HSM)."
  default     = "standard"

  validation {
    condition     = contains(["standard", "premium"], var.sku_name)
    error_message = "sku_name must be standard or premium."
  }
}

variable "soft_delete_retention_days" {
  type        = number
  description = "How long a deleted vault is recoverable."
  default     = 90
}

variable "operator_object_id" {
  type        = string
  description = "Object id of the principal running tofu; needs to create and rotate the key."
}

variable "api_identity_object_id" {
  type        = string
  description = <<-EOT
    Object id of the service principal the api authenticates as, which gets
    wrap and unwrap on the key and nothing else. Null until the app
    registration exists: it is an Entra directory object with a client
    certificate, created by hand alongside the other identity setup in
    docs/ops/AZURE-SETUP.md rather than by an agent's apply.
  EOT
  default     = null
}

variable "api_key_permissions" {
  description = "Key permissions for api_identity_object_id. The api wraps and unwraps users' DEKs; the guest freeze's hosts only read the public half and unwrap (I-653)."
  type        = list(string)
  default     = ["Get", "WrapKey", "UnwrapKey"]
}

variable "key_name" {
  type        = string
  description = "Name of the wrapping key."
  default     = "repose-dek-wrap"
}

variable "key_size" {
  type        = number
  description = "RSA key size."
  default     = 3072
}

variable "key_rotate_after_creation" {
  type        = string
  description = "ISO 8601 duration after creation at which the key rotates automatically."
  default     = "P12M"
}

variable "key_expire_after" {
  type        = string
  description = "ISO 8601 duration after which a key version expires. Must leave room for notify_before_expiry after the rotation point."
  default     = "P13M"
}

variable "key_notify_before_expiry" {
  type        = string
  description = "ISO 8601 duration before expiry at which Azure emits an event."
  default     = "P30D"
}

variable "tags" {
  type        = map(string)
  description = "Tags applied to every resource in this module."
  default     = {}
}
