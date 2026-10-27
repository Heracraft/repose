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

  # What each host is made of: its entry in var.host_shapes where it names an
  # attribute, the environment's global everywhere else. A host with no entry
  # resolves to the globals alone, so its plan is unchanged.
  #
  # Attribute by attribute rather than merge(): an attribute an entry leaves
  # out is null, and a null on the right of a merge beats the global rather
  # than falling back to it. An object whose attributes are not all one type
  # does not convert to a map for merge() anyway. try() because indexing a
  # key that is not in the map is an error, not null. Every global keeps a
  # default, because coalesce fails when all of its arguments are null.
  host_shape = {
    for name in var.hosts : name => {
      size           = coalesce(try(var.host_shapes[name].size, null), var.host_size)
      class          = coalesce(try(var.host_shapes[name].class, null), var.host_class)
      zone           = coalesce(try(var.host_shapes[name].zone, null), var.zone)
      data_disk_gb   = coalesce(try(var.host_shapes[name].data_disk_gb, null), var.host_data_disk_gb)
      data_disk_iops = coalesce(try(var.host_shapes[name].data_disk_iops, null), var.host_data_disk_iops)
      data_disk_mbps = coalesce(try(var.host_shapes[name].data_disk_mbps, null), var.host_data_disk_mbps)
      os_disk_gb     = coalesce(try(var.host_shapes[name].os_disk_gb, null), var.host_os_disk_gb)
    }
  }

  # Everything the installer image is built from, so the preflight re-runs
  # when the image changes and not on every apply. Guarded on flake.nix so a
  # stale flake_path reaches the precondition's message rather than failing
  # here on a missing file; fileset() on a directory that is not there is
  # empty, not an error.
  kexec_image_sources = fileexists("${var.flake_path}/flake.nix") ? merge(
    {
      "flake.nix"  = filesha256("${var.flake_path}/flake.nix")
      "flake.lock" = filesha256("${var.flake_path}/flake.lock")
    },
    {
      for f in fileset("${var.flake_path}/installer", "**") :
      "installer/${f}" => filesha256("${var.flake_path}/installer/${f}")
    },
  ) : {}
}

# Every host_flake_attrs entry must name a module that is there. `host-03`
# pointing at an attribute whose nix/hosts/host-03.nix was never written
# fails during that host's install, after the VM, the disk and the NIC exist.
# It is a file check and not an evaluation, so it catches the typo and the
# forgotten file and nothing subtler.
check "host_flake_attrs_name_a_module" {
  assert {
    condition = length([
      for name, attr in var.host_flake_attrs :
      attr if !fileexists("${var.flake_path}/hosts/${attr}.nix")
    ]) == 0
    error_message = format(
      "host_flake_attrs names %s, and %s has no matching nix/hosts/<attr>.nix. The attribute would fail to evaluate during that machine's install, with the VM and its data disk already created.",
      join(", ", [for name, attr in var.host_flake_attrs : "${name} -> ${attr}" if !fileexists("${var.flake_path}/hosts/${attr}.nix")]),
      var.flake_path,
    )
  }
}

# Build the installer image before the first VM exists.
#
# This is the cheapest check there is, and it stands in for the most
# expensive recovery. On 2026-10-26 a host was created, kexeced into an
# image that could not get an address, and waited on for 16h56m holding this
# environment's only state lock. A flake that does not evaluate, an
# installer attribute that is not there, and an image that does not build
# are all things to learn now, with nothing created and the lock held no
# longer than a cached `nix build`.
#
# Every machine is ordered behind this, not merely behind its own install,
# so a broken image costs no VM at all.
#
# --out-link, and the link is kept under .terraform, which is per-root and
# not committed. The triggers below are the image's sources, so this does
# not re-run on an apply that changed nothing; with --no-link, a `nix store
# gc` in between would then collect the image while this resource went on
# reporting success. The link stops that for as long as .terraform survives,
# and a wiped .terraform costs only a rebuild, because each install builds
# the attribute itself rather than trusting this resource's record of it.
# This resource's job is the early failure, not the delivery.
resource "terraform_data" "kexec_image" {
  triggers_replace = merge(local.kexec_image_sources, { attr = var.kexec_flake_attr })

  lifecycle {
    precondition {
      condition     = fileexists("${var.flake_path}/flake.nix") && fileexists("${var.flake_path}/installer/default.nix")
      error_message = "flake_path is ${var.flake_path}, which has no flake.nix or no installer/default.nix. Usually this means the work is not on that checkout's branch yet rather than that the path is wrong: flake_path names the checkout the apply BUILDS from, which is not the worktree you are running make in, and nix reads a bare path inside a git repo as a git flakeref, so only files committed there exist as far as the build is concerned. Adding a host or the installer from a worktree: let the release queue land it on main, then apply."
    }
  }

  provisioner "local-exec" {
    interpreter = ["/usr/bin/env", "bash", "-euo", "pipefail", "-c"]
    # Under `timeout` for the same reason every other step here is: a
    # substituter that accepts a connection and then stops answering would
    # otherwise hold the state lock with no deadline, which is the failure
    # this whole preflight exists to shorten.
    command = "timeout --kill-after=1m '${var.install_deadline}' nix build --out-link '${path.root}/.terraform/repose-kexec-installer' '${var.flake_path}#${var.kexec_flake_attr}'"
  }
}

# Deliberately not checked in a check block, because OpenTofu cannot do it
# well:
#
#   Whether the image builds, and whether the flake attribute evaluates.
#   check blocks and preconditions evaluate expressions; they cannot run
#   nix. That is why the resource above exists instead: it runs the real
#   build, ordered ahead of every VM. The cost, accepted knowingly, is that
#   it runs at apply time and not at plan time, so `tofu plan` cannot tell
#   you the image builds -- only `tofu apply` can, and it does so before
#   creating anything. An `external` data source would move it to plan
#   time, at the price of shelling out on every plan including read-only
#   ones, which gives a plan side effects and makes it as slow as a build.
#
#   Whether a join token is still valid. A token is opaque and the api
#   keeps only its hash and its expiry (docs/interfaces/db-schema.md), so
#   nothing in the string says when it was minted. A token that went stale
#   in the 24 hours since it was minted is indistinguishable here from a
#   fresh one; it surfaces as hostd's exit 3 on a rejected token. Presence
#   is all that can be checked, and that is the check below.

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

# A host_shapes key that is not a host name applies nothing and reports
# nothing: the host it was meant for comes up on the globals instead, which
# for prod is a D16s_v7 with a 512 GB data disk.
check "host_shapes_name_real_hosts" {
  assert {
    condition = length(setsubtract(toset(keys(var.host_shapes)), toset(var.hosts))) == 0
    error_message = format(
      "host_shapes has an entry for %s, which is not in hosts. The entry applies to nothing and the host it was meant for takes the global size and disk.",
      join(", ", setsubtract(toset(keys(var.host_shapes)), toset(var.hosts))),
    )
  }
}

# A host with no host_flake_attrs entry installs the generic `host`
# configuration, and then never registers. Its apiAddr is the default
# `api.repose.herakraft.co:443` (nix/hosts/default.nix), where nothing gRPC
# answers: api-grpc's mTLS listeners are 8443 and 8444, and with
# manage_dns = false that name is Cloudflare's proxied wildcard. It also
# ships no freeze key vault and no pauseIdle. The apply succeeds, the VM is
# healthy, and the host never appears in `repose-admin hosts list` --
# RUNBOOK's "Host never registered", cause 4. It warns rather than fails
# because host_flake_attr_default is a supported value: an environment can
# mean to run the generic configuration.
check "every_host_has_a_flake_attr" {
  assert {
    condition = length(setsubtract(toset(var.hosts), toset(keys(var.host_flake_attrs)))) == 0
    error_message = format(
      "No host_flake_attrs entry for %s, so it installs `%s`, the generic configuration, whose api address is port 443 where nothing gRPC answers: the apply succeeds and the host never registers (RUNBOOK, \"Host never registered\", cause 4). It would also run without the freeze key vault and without pauseIdle. Add nix/hosts/<name>.nix and name it in host_flake_attrs.",
      join(", ", setsubtract(toset(var.hosts), toset(keys(var.host_flake_attrs)))),
      var.host_flake_attr_default,
    )
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
  kexec_image          = var.kexec_image
  kexec_flake_attr     = var.kexec_flake_attr
  install_deadline     = var.install_deadline

  tags = local.tags

  # Ordered behind the image build, not merely behind its own install: a
  # broken installer image must cost no VM.
  depends_on = [terraform_data.kexec_image]
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

  name  = each.key
  class = local.host_shape[each.key].class
  size  = local.host_shape[each.key].size
  zone  = local.host_shape[each.key].zone
  # Global on purpose: both validations pin it to "Standard", because Trusted
  # Launch and Confidential VMs disable nested virtualization, so a per-host
  # value has no use.
  security_type = var.host_security_type

  resource_group_name = data.azurerm_resource_group.main.name
  location            = local.location
  subnet_id           = module.network.hosts_subnet_id
  network_ready       = module.network.hosts_subnet_ready

  data_disk_gb   = local.host_shape[each.key].data_disk_gb
  data_disk_iops = local.host_shape[each.key].data_disk_iops
  data_disk_mbps = local.host_shape[each.key].data_disk_mbps
  os_disk_gb     = local.host_shape[each.key].os_disk_gb

  snapshot_identity_id = module.storage.host_identity_id

  flake_path           = var.flake_path
  flake_attr           = lookup(var.host_flake_attrs, each.key, var.host_flake_attr_default)
  ssh_private_key_path = var.ssh_private_key_path
  authorized_keys      = var.operator_authorized_keys
  build_on             = var.build_on
  kexec_image          = var.kexec_image
  kexec_flake_attr     = var.kexec_flake_attr
  install_deadline     = var.install_deadline

  # Hosts have no public IP; every provisioner goes through the edge, which
  # must therefore already be running NixOS.
  jump_host = module.edge.public_ip
  jump_port = var.edge_operator_ssh_port

  join_token = lookup(var.join_tokens, each.key, "")

  tags = local.tags

  depends_on = [module.edge, terraform_data.kexec_image]
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
