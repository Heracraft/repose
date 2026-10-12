# Production values that belong in review. Secrets and per-operator values go
# in prod.local.tfvars, which is not committed; see prod.local.tfvars.example.
#
# Join tokens are never written to a file at all: they are single-use, expire
# in 24 hours, and are passed on the apply that adds a host
# (infra/README.md, "Adding a host").

subscription_id     = "5f27aace-dd8c-4dc0-95bf-b59ee8de7d70"
resource_group_name = "repose-prod"
zone                = "1"

# DECISIONS I-14 and I-39: the pre-launch host. Launch values are Standard_D64s_v7,
# azure-d64s-v5 and 2048, applied with guests stopped.
host_size           = "Standard_D16s_v7"
host_class          = "azure-d16s-v7"
host_data_disk_gb   = 512
host_data_disk_iops = 16000
host_data_disk_mbps = 600

# The portal defaults to Trusted Launch, which silently disables nested
# virtualization. Only "Standard" is accepted and the variable has no default.
host_security_type = "Standard"

edge_size = "Standard_D2s_v7"

# Wave 3 is here: the api and repose-admin are merged, so the control plane is
# worth its ~$237 a month (DECISIONS I-24, I-71; infra/README.md "Cost"). The
# VM's OS disk holds the platform Postgres, so setting this back to 0 destroys
# the control plane; the retention that matters is the R2 dump.
#
# The VM is a server of the owner's existing Coolify instance, not a Coolify
# install of its own (DECISIONS I-83): this is the public half of the key that
# instance connects with. The address it connects from is coolify_manager_cidrs
# in prod.local.tfvars.
coolify_count      = 1
coolify_size       = "Standard_D4s_v7"
coolify_public_key = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAINF+RWPEGM/+MfUFU4nZ1HbqBt3YkNa74frsKHepY0Ca"

# No Cloudflare token on this subscription yet, so the records are created by
# hand from the table in infra/README.md. herakraft.co answers every name from
# a proxied wildcard, so "no record" means "resolves to Cloudflare's proxy",
# which carries neither SSH nor WireGuard (infra/README.md, "DNS while
# manage_dns is false").
manage_dns = false

# Hosts are added one at a time; see infra/README.md. Each production host
# has its own nixosConfigurations attribute (nix/hosts/<name>.nix, DECISIONS
# I-40); the join token goes in prod.local.tfvars.
hosts = ["host-01"]
host_flake_attrs = {
  host-01 = "host-01"
}

snapshots_account_name = "reposesnapshots3912"
keyvault_name          = "repose-kv-3912"
# The guest freeze's image-wrapping key, in a vault of its own because access
# policies are vault-wide: the hosts get Get and UnwrapKey here and nothing on
# the vault that wraps users' secret keys (DECISIONS I-653, I-686). The name is
# not secret, so it lives here; nix/hosts/host-01.nix names the same vault.
freeze_keyvault_name = "repose-frz-3912"

dns_zone = "herakraft.co"

# Azure Monitor alerts on all three VMs, emailed to alert_email
# (prod.local.tfvars). DECISIONS I-648; RUNBOOK "Azure alert".
alerts_enabled = true
