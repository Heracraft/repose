# Staging values. Secrets and per-operator values go in staging.local.tfvars,
# which is not committed.

subscription_id     = "5f27aace-dd8c-4dc0-95bf-b59ee8de7d70"
resource_group_name = "repose-staging"
zone                = "1"

# Smallest Intel size that still nests KVM, because staging exists to
# rehearse the shape of an apply, not its capacity.
host_size           = "Standard_D2s_v7"
host_class          = "azure-d2s-v7"
host_data_disk_gb   = 64
host_data_disk_iops = 3000
host_data_disk_mbps = 125

host_security_type = "Standard"

edge_size = "Standard_D2s_v7"

coolify_count = 0
coolify_size  = "Standard_D2s_v7"

# Empty except during the monthly create-register-destroy exercise
# (docs/workstreams/11-infra-opentofu.md §7).
hosts = []

snapshots_account_name = "reposesnapstaging3912"
keyvault_name          = "repose-kv-staging-3912"

dns_zone   = "herakraft.co"
dns_prefix = "repose-staging"
