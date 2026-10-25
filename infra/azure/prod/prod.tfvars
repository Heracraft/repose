# Production values that belong in review. Secrets and per-operator values go
# in prod.local.tfvars, which is not committed; see prod.local.tfvars.example.
#
# Join tokens are never written to a file at all: they are single-use, expire
# in 24 hours, and are passed on the apply that adds a host
# (infra/README.md, "Adding a host").

subscription_id     = "5f27aace-dd8c-4dc0-95bf-b59ee8de7d70"
resource_group_name = "repose-prod"
zone                = "1"

# host-01's shape (DECISIONS I-14, I-39), and the fallback for any host that
# names nothing in host_shapes. These values stay as they are: host-01 carries
# live guests, and changing a global here would plan a change on every host.
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
hosts = ["host-01", "host-02"]
host_flake_attrs = {
  host-01 = "host-01"
  host-02 = "host-02"
}

# host-02 is the bigger host (DECISIONS I-14): it runs alongside host-01, and
# host-01 is retired by moving its projects across. host-01 names no entry, so
# every value it has today resolves from the globals above and its plan is
# empty.
#
# os_disk_gb is 512, not the fleet's 256, because the OS disk holds
# /nix/store and the store is shared by every guest on the host: hostd keeps
# a GC root for each live closure and three revisions per project, and a root
# filesystem past 80 percent makes hostd refuse Build with
# `insufficient_capacity: host store full` for every tenant on that machine
# (RUNBOOK, "StoreHigh"). host-01 sells 4 seats and host-02 sells 24. The
# data disk grows online and the RUNBOOK says how; the OS disk is inline on
# the VM resource, has no grow procedure, and changing it later is a plan
# against a live host. $38 a month at the P15 to P20 step.
#
# data_disk_iops and data_disk_mbps are left at the fleet's 16000 and 600. A
# 2048 GB disk under four times the guests may want more; provisioned IOPS and
# throughput are billed, so raising them is the owner's call. Raising mbps is
# also the only lever on the guest freeze's budget (DECISIONS I-786).
host_shapes = {
  host-02 = {
    size         = "Standard_D64s_v7"
    class        = "azure-d64s-v7"
    data_disk_gb = 2048
    os_disk_gb   = 512
  }
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
