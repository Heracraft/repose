# The key that wraps every per-user data encryption key.
#
# docs/DESIGN.md §12 and DECISIONS R3-10: named secrets are encrypted in
# Postgres with a per-user DEK, and the DEK is wrapped by this key. Rotating
# it re-wraps DEKs without touching a single ciphertext, which is the whole
# point of the arrangement.
#
# The api holds wrap and unwrap and nothing else. It never needs the key
# material, so it never gets Get, and an api that is fully compromised can
# decrypt only what it is handed, never export the key.

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
  tags = merge(var.tags, { "repose:role" = "keyvault" })
}

resource "azurerm_key_vault" "main" {
  name                = var.name
  resource_group_name = var.resource_group_name
  location            = var.location
  tenant_id           = var.tenant_id
  sku_name            = var.sku_name

  # Without purge protection, deleting the vault and purging it destroys every
  # user's secrets irrecoverably: the ciphertext in Postgres is unreadable the
  # moment the wrapping key is gone. This cannot be turned off again once set,
  # which is the intent.
  purge_protection_enabled   = true
  soft_delete_retention_days = var.soft_delete_retention_days

  # Access policies rather than RBAC because the grant this vault needs is
  # "wrap and unwrap this one key", which is exactly what a key access policy
  # expresses (docs/workstreams/11-infra-opentofu.md §2).
  rbac_authorization_enabled = false

  public_network_access_enabled = true

  tags = local.tags

  lifecycle {
    prevent_destroy = true
  }
}

# Whoever runs tofu has to be able to create and rotate the key.
resource "azurerm_key_vault_access_policy" "operator" {
  key_vault_id = azurerm_key_vault.main.id
  tenant_id    = var.tenant_id
  object_id    = var.operator_object_id

  key_permissions = [
    "Create",
    "Delete",
    "Get",
    "GetRotationPolicy",
    "List",
    "Purge",
    "Recover",
    "SetRotationPolicy",
    "Rotate",
    "Update",
  ]
}

# The api. Wrap and unwrap, plus Get: the api reads the key's current
# version before wrapping (internal/api/secrets/azurekv.go CurrentVersion),
# and Get on a Key Vault *key* returns the public half and attributes only;
# the private key never leaves the HSM whatever the permission (DECISIONS
# I-91). Still no List, Export, Create or Delete.
resource "azurerm_key_vault_access_policy" "api" {
  count = var.api_identity_object_id == null ? 0 : 1

  key_vault_id = azurerm_key_vault.main.id
  tenant_id    = var.tenant_id
  object_id    = var.api_identity_object_id

  key_permissions = var.api_key_permissions
}

resource "azurerm_key_vault_key" "dek_wrap" {
  name         = var.key_name
  key_vault_id = azurerm_key_vault.main.id
  key_type     = "RSA"
  key_size     = var.key_size
  key_opts     = ["wrapKey", "unwrapKey"]

  rotation_policy {
    expire_after         = var.key_expire_after
    notify_before_expiry = var.key_notify_before_expiry

    automatic {
      time_after_creation = var.key_rotate_after_creation
    }
  }

  tags = local.tags

  depends_on = [azurerm_key_vault_access_policy.operator]

  lifecycle {
    prevent_destroy = true
  }
}
