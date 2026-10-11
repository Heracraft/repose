# One complete repose environment: network, snapshot storage, Key Vault, the
# edge, the control plane, every host, and the DNS records that point at them.
#
# The prod/ and staging/ roots are thin: they pin a state key, configure the
# providers, and call this module. Keeping the composition in one place is
# what stops staging from drifting into a different shape than the thing it
# is supposed to rehearse.
#
# docs/workstreams/11-infra-opentofu.md §2.

terraform {
  required_version = ">= 1.6.0"
  required_providers {
    azurerm = {
      source  = "hashicorp/azurerm"
      version = ">= 4.0, < 5.0"
    }
    cloudflare = {
      source  = "cloudflare/cloudflare"
      version = ">= 5.0, < 6.0"
    }
  }
}

data "azurerm_client_config" "current" {}

# The resource group and the OpenTofu state account inside it are created once
# by infra/bootstrap and are never managed from here: an apply that could
# destroy the resource group could destroy the state that describes it
# (DECISIONS I-19).
data "azurerm_resource_group" "main" {
  name = var.resource_group_name
}

locals {
  tags = merge(var.tags, {
    "repose:env"        = var.env
    "repose:managed-by" = "opentofu"
  })

  location = data.azurerm_resource_group.main.location

  dashboard_fqdn = "${var.dns_prefix}.${var.dns_zone}"
  api_fqdn       = "api.${var.dns_prefix}.${var.dns_zone}"
  ssh_fqdn       = "ssh.${var.dns_prefix}.${var.dns_zone}"
}

# A host with no token boots, finds nothing at /run/repose/join-token, and
# logs register_fail every 30 seconds forever (docs/workstreams/11-infra-
# opentofu.md §6). A check block warns at plan time instead of leaving that to
# be discovered in the host's journal. It warns rather than fails because a
# host that registered last month legitimately has no token today.
check "every_host_has_a_join_token" {
  assert {
    # The token values are sensitive; the host names they are keyed by are
    # not, and without nonsensitive() OpenTofu refuses to print the message
    # that names the hosts, which is the entire value of the check.
    condition = length(setsubtract(toset(var.hosts), toset(nonsensitive(keys(var.join_tokens))))) == 0
    error_message = format(
      "No join token for %s. If they have already registered this is fine; otherwise mint one with `hostdev init` (or `repose-admin hosts add` once the api exists) and put it in the environment's local tfvars.",
      join(", ", setsubtract(toset(var.hosts), toset(nonsensitive(keys(var.join_tokens))))),
    )
  }
}

# herakraft.co answers every name under it from a proxied wildcard record, so
# "no record" is not the same as "no answer": with manage_dns off,
# ssh.repose.herakraft.co resolves to Cloudflare's proxy, which carries
# neither SSH nor WireGuard, and the edge's real address is reachable only as
# a literal IP. Observed on 2026-09-20; infra/README.md, "DNS while manage_dns
# is false", has the records to create by hand until a token exists.
check "dns_is_managed_or_manual" {
  assert {
    condition     = var.manage_dns
    error_message = "manage_dns is false: the repose names resolve to the herakraft.co wildcard, not to the edge or the control plane. Create them by hand (infra/README.md) or set manage_dns = true once CLOUDFLARE_API_TOKEN and cloudflare_zone_id exist."
  }
}

module "network" {
  source = "../network"

  env                 = var.env
  resource_group_name = data.azurerm_resource_group.main.name
  location            = local.location
  zone                = var.zone

  vnet_cidr           = var.vnet_cidr
  hosts_subnet_cidr   = var.hosts_subnet_cidr
  edge_subnet_cidr    = var.edge_subnet_cidr
  control_subnet_cidr = var.control_subnet_cidr

  operator_cidrs         = var.operator_cidrs
  coolify_manager_cidrs  = var.coolify_manager_cidrs
  control_web_cidrs      = var.control_web_cidrs
  edge_operator_ssh_port = var.edge_operator_ssh_port

  tags = local.tags
}

module "storage" {
  source = "../storage"

  account_name        = var.snapshots_account_name
  resource_group_name = data.azurerm_resource_group.main.name
  location            = local.location

  tier_to_cool_days = var.snapshot_tier_to_cool_days
  delete_after_days = var.snapshot_delete_after_days

  api_identity_object_id = var.api_identity_object_id

  tags = local.tags
}

module "keyvault" {
  source = "../keyvault"

  name                = var.keyvault_name
  resource_group_name = data.azurerm_resource_group.main.name
  location            = local.location
  tenant_id           = data.azurerm_client_config.current.tenant_id

  operator_object_id     = coalesce(var.operator_object_id, data.azurerm_client_config.current.object_id)
  api_identity_object_id = var.api_identity_object_id

  tags = local.tags
}

# The guest freeze's wrapping key (DECISIONS I-653): RSA, the hosts' identity
# may read its public half and unwrap with it. Hosts wrap image keys with the
# public half they fetched ahead of time, so a freeze at shutdown needs no
# network; a restore needs the vault.
module "freeze_keyvault" {
  source = "../keyvault"
  count  = var.freeze_keyvault_name == null ? 0 : 1

  name                = var.freeze_keyvault_name
  resource_group_name = data.azurerm_resource_group.main.name
  location            = local.location
  tenant_id           = data.azurerm_client_config.current.tenant_id

  operator_object_id     = coalesce(var.operator_object_id, data.azurerm_client_config.current.object_id)
  api_identity_object_id = module.storage.host_identity_principal_id
  api_key_permissions    = ["Get", "UnwrapKey"]
  key_name               = "freeze-wrap"

  tags = local.tags
}

module "edge" {
  source = "../edge"

  name                = var.edge_name
  size                = var.edge_size
  zone                = var.zone
  resource_group_name = data.azurerm_resource_group.main.name
  location            = local.location
  subnet_id           = module.network.edge_subnet_id
  network_ready       = module.network.hosts_subnet_ready

  operator_ssh_port = var.edge_operator_ssh_port

  flake_path           = var.flake_path
  flake_attr           = var.edge_flake_attr
  ssh_private_key_path = var.ssh_private_key_path
  authorized_keys      = var.operator_authorized_keys
  build_on             = var.build_on

  tags = local.tags
}

# Not created until wave 3. The api, the dashboard and Logto are workstreams
# 05 and 08; until they exist this VM is about $180 a month of nothing, and
# the edge and the first host are the parts worth paying for early
# (DECISIONS I-24).
module "coolify" {
  source = "../coolify"
  count  = var.coolify_count

  name                = var.coolify_name
  size                = var.coolify_size
  zone                = var.zone
  resource_group_name = data.azurerm_resource_group.main.name
  location            = local.location
  subnet_id           = module.network.control_subnet_id
  network_ready       = module.network.control_subnet_ready
  # The first usable address of the control subnet (Azure reserves .0-.3),
  # which is what dynamic allocation handed the first VM; pinning it keeps
  # the hosts' api address stable (DECISIONS I-92).
  private_ip = cidrhost(var.control_subnet_cidr, 4)

  authorized_keys      = var.operator_authorized_keys
  ssh_private_key_path = var.ssh_private_key_path
  os_disk_gb           = var.coolify_os_disk_gb
  coolify_public_key   = var.coolify_public_key


  edge_wireguard_public_key = var.edge_wireguard_public_key
  edge_wireguard_endpoint   = "${module.edge.public_ip}:51820"

  tags = local.tags
}

module "host" {
  source   = "../host"
  for_each = toset(var.hosts)

  name          = each.key
  class         = var.host_class
  size          = var.host_size
  security_type = var.host_security_type
  zone          = var.zone

  resource_group_name = data.azurerm_resource_group.main.name
  location            = local.location
  subnet_id           = module.network.hosts_subnet_id
  network_ready       = module.network.hosts_subnet_ready

  data_disk_gb   = var.host_data_disk_gb
  data_disk_iops = var.host_data_disk_iops
  data_disk_mbps = var.host_data_disk_mbps
  os_disk_gb     = var.host_os_disk_gb

  snapshot_identity_id = module.storage.host_identity_id

  flake_path           = var.flake_path
  flake_attr           = lookup(var.host_flake_attrs, each.key, var.host_flake_attr_default)
  ssh_private_key_path = var.ssh_private_key_path
  authorized_keys      = var.operator_authorized_keys
  build_on             = var.build_on

  # Hosts have no public IP; every provisioner goes through the edge, which
  # must therefore already be running NixOS.
  jump_host = module.edge.public_ip
  jump_port = var.edge_operator_ssh_port

  join_token = lookup(var.join_tokens, each.key, "")

  tags = local.tags

  depends_on = [module.edge]
}

module "dns" {
  source = "../../../dns"
  count  = var.manage_dns ? 1 : 0

  zone_id = var.cloudflare_zone_id

  # A record that resolves to nothing is worse than no record, so the control
  # plane's names appear only once the VM behind them does.
  records = merge(
    {
      (local.ssh_fqdn) = {
        address = module.edge.public_ip
        comment = "repose SSH gateway and WireGuard hub (${var.env})"
      }
    },
    var.coolify_count == 0 ? {} : {
      (local.dashboard_fqdn) = {
        address = one(module.coolify[*].public_ip)
        proxied = var.proxy_web_records
        comment = "repose dashboard (${var.env})"
      }
      (local.api_fqdn) = {
        address = one(module.coolify[*].public_ip)
        proxied = var.proxy_web_records
        comment = "repose api (${var.env})"
      }
    },
  )
}
