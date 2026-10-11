# Azure's own view of the environment's VMs, emailed to the owner
# (DECISIONS I-648). It is the second channel next to the personal server's
# Alertmanager, and the only one that still works when the personal server,
# the edge's WireGuard or the api is down: on 2026-10-10 Azure live-migrated
# host-01, rebooted it to heal it, and nobody was told.
#
# Email only. Azure's webhook body is its common alert schema, which Google
# Chat (the owner's paging channel) does not accept, so the Chat page for the
# same outage comes from Prometheus (HostdDown, GatewayDown, ApiDown).
#
# Off unless alerts_enabled: staging's VMs come and go, and every one would
# email the owner. RUNBOOK "Azure alert" says what each of these means.

locals {
  alerts = var.alerts_enabled

  # Every VM of the environment by name: the hosts, the edge, and the control
  # VM when it exists.
  alert_vms = merge(
    { for name, h in module.host : name => h.vm_id },
    { (var.edge_name) = module.edge.vm_id },
    var.coolify_count == 0 ? {} : { (var.coolify_name) = one(module.coolify[*].vm_id) },
  )
}

# Service Health matches regions by display name ("East US"), not by the
# resource group's "eastus".
data "azurerm_location" "main" {
  count    = local.alerts ? 1 : 0
  location = local.location
}

resource "azurerm_monitor_action_group" "repose" {
  count = local.alerts ? 1 : 0

  name                = "repose-${var.env}"
  resource_group_name = data.azurerm_resource_group.main.name
  short_name          = "repose"
  tags                = local.tags

  email_receiver {
    name                    = "owner"
    email_address           = var.alert_email
    use_common_alert_schema = true
  }

  lifecycle {
    precondition {
      condition     = can(regex("^[^@\\s]+@[^@\\s]+\\.[^@\\s]+$", var.alert_email))
      error_message = "alerts_enabled is true but alert_email is not an email address. Set alert_email in the environment's local tfvars."
    }
  }
}

# Azure stopped, rebooted or degraded a VM itself: a live migration, a heal,
# a host fault. PlatformInitiated is the reason Azure gives for those;
# UserInitiated is an operator's own restart, which needs no email, and
# Unknown is included because Azure uses it when it has not decided yet.
resource "azurerm_monitor_activity_log_alert" "vm_health" {
  count = local.alerts ? 1 : 0

  name                = "repose-${var.env}-vm-health"
  resource_group_name = data.azurerm_resource_group.main.name
  location            = "global"
  scopes              = [data.azurerm_resource_group.main.id]
  description         = "Azure stopped, rebooted or degraded a repose VM itself. RUNBOOK: Azure alert."
  tags                = local.tags

  criteria {
    category      = "ResourceHealth"
    resource_type = "Microsoft.Compute/virtualMachines"

    resource_health {
      current = ["Unavailable", "Degraded", "Unknown"]
      reason  = ["PlatformInitiated", "Unknown"]
    }
  }

  action {
    action_group_id = azurerm_monitor_action_group.repose[0].id
  }
}

# Planned maintenance, service incidents and advisories for the region. None
# of these existed before 2026-10-11; a maintenance notice is the only
# warning Azure gives before it moves a VM.
resource "azurerm_monitor_activity_log_alert" "service_health" {
  count = local.alerts ? 1 : 0

  name                = "repose-${var.env}-service-health"
  resource_group_name = data.azurerm_resource_group.main.name
  location            = "global"
  scopes              = ["/subscriptions/${data.azurerm_client_config.current.subscription_id}"]
  description         = "Azure service issue, planned maintenance or health advisory in the region. RUNBOOK: Azure alert."
  tags                = local.tags

  criteria {
    category = "ServiceHealth"

    service_health {
      events    = ["Incident", "Maintenance", "ActionRequired", "Informational"]
      locations = ["Global", data.azurerm_location.main[0].display_name]
    }
  }

  action {
    action_group_id = azurerm_monitor_action_group.repose[0].id
  }
}

# The VM is not running as far as Azure can tell (VmAvailabilityMetric is 1
# while it runs, 0 when it does not, absent while it is deallocated). One per
# VM, so the email names the VM. The edge's network function has been
# revoked three times (Sep 26, Sep 27, Oct 3) and the control VM has never
# been rebooted; both are watched like the host.
resource "azurerm_monitor_metric_alert" "vm_availability" {
  for_each = local.alerts ? local.alert_vms : {}

  name                = "repose-${var.env}-${each.key}-availability"
  resource_group_name = data.azurerm_resource_group.main.name
  scopes              = [each.value]
  description         = "${each.key} has been unavailable for 5 minutes. RUNBOOK: Azure alert."
  severity            = 1
  frequency           = "PT1M"
  window_size         = "PT5M"
  tags                = local.tags

  # Maximum below 1: every one-minute sample in the 5-minute window was 0,
  # which is what the description says. Average would fire on a single
  # minute, including the minute or two of a planned reboot.
  criteria {
    metric_namespace = "Microsoft.Compute/virtualMachines"
    metric_name      = "VmAvailabilityMetric"
    aggregation      = "Maximum"
    operator         = "LessThan"
    threshold        = 1
  }

  action {
    action_group_id = azurerm_monitor_action_group.repose[0].id
  }
}
