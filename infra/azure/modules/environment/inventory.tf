# What the resource group holds that this configuration did not create.
#
# The failure this exists for: a host's OS disk is declared inline on the VM
# (`os_disk` in ../host/main.tf), the module sets no `delete_option`, and no
# plan ever names it. So whether `osdisk-host-01` survived host-01's destroy
# on 2026-11-02 as an unowned Premium SSD P15 at about $38 a month was
# invisible to the diff, invisible to `tofu plan`, and knowable only by
# asking Azure afterwards by hand. It went with the VM that time. That is a
# fact about one apply and not a promise about the next, which is why this is
# mechanical now (DECISIONS I-1004).
#
# Scope, said plainly rather than left to be inferred: four resource types.
# Snapshots, blobs, Key Vault keys, managed identities and the NAT gateway
# itself are not checked, so a clean result here is not an empty group. It
# answers one question -- is there a machine-shaped billable thing nobody
# owns -- and nothing wider.

data "azurerm_resources" "group" {
  # Module scope, deliberately, not inside the check block. A data source
  # that errors *inside* a check has its error demoted to a warning under a
  # different heading and every assertion in that block is SKIPPED, so an
  # expired credential or a lost Reader role would read as a clean group and
  # go green under any `grep "Check block assertion failed"` gate. Out here a
  # failed or unauthorised ARM read is a hard plan error, which is the only
  # honest answer to "we could not look".
  #
  # Counted off where the check is off, so an environment that does not own
  # its group exclusively pays no ARM list call for an assertion that would
  # pass trivially.
  count = var.resource_group_is_exclusive ? 1 : 0

  resource_group_name = data.azurerm_resource_group.main.name
}

locals {
  # The types a stray costs money in and that this configuration creates.
  inventory_types = toset([
    "Microsoft.Compute/virtualMachines",
    "Microsoft.Compute/disks",
    "Microsoft.Network/networkInterfaces",
    "Microsoft.Network/publicIPAddresses",
  ])

  # Expected names per ROLE, never a cross-product of names and types: hosts
  # have no public IP, edge and control have no data disk, and control is
  # absent entirely when coolify_count is 0. A templated all-pairs set would
  # expect `pip-host-02`, which nothing creates, and would have to tolerate
  # absences -- and a check that tolerates absences cannot see a stray.
  inventory_vm_names = concat(
    sort(var.hosts),
    [var.edge_name],
    var.coolify_count == 1 ? [var.coolify_name] : [],
  )

  inventory_expected = setunion(
    # Every role: a VM, its NIC, its inline OS disk.
    toset(local.inventory_vm_names),
    toset([for n in local.inventory_vm_names : "nic-${n}"]),
    toset([for n in local.inventory_vm_names : "osdisk-${n}"]),
    # Hosts only: the data disk that holds every tenant volume.
    toset([for n in var.hosts : "datadisk-${n}"]),
    # Edge and control only, plus the NAT gateway's, which belongs to no VM
    # and is read from the network module rather than retyped here.
    toset(concat(
      ["pip-${var.edge_name}"],
      var.coolify_count == 1 ? ["pip-${var.coolify_name}"] : [],
      [module.network.nat_public_ip_name],
    )),
  )

  inventory_found = var.resource_group_is_exclusive ? toset([
    for r in data.azurerm_resources.group[0].resources :
    r.name if contains(local.inventory_types, r.type)
  ]) : toset([])

  inventory_extra = setsubtract(local.inventory_found, local.inventory_expected)

  # Fail closed as well as open. A read that comes back empty, or filtered to
  # nothing by a type string Azure renamed, is not a clean group -- it is a
  # check that has stopped looking, and it would pass the `extra` clause for
  # ever. These are the names no environment can be without: the edge and its
  # NIC, and every host in `hosts`.
  inventory_must_exist = setunion(
    toset(var.hosts),
    toset([var.edge_name, "nic-${var.edge_name}"]),
  )

  inventory_missing = setsubtract(local.inventory_must_exist, local.inventory_found)
}

check "resource_group_is_exclusive" {
  assert {
    condition = !var.resource_group_is_exclusive || (
      length(local.inventory_extra) == 0 && length(local.inventory_missing) == 0
    )
    error_message = join("", [
      "The resource group ${data.azurerm_resource_group.main.name} does not match what this configuration creates.\n",
      length(local.inventory_extra) > 0 ? join("", [
        "\nBillable and owned by nothing here: ",
        join(", ", sort(tolist(local.inventory_extra))),
        "\nEach is a virtual machine, disk, network interface or public IP that no resource in this configuration names, so no plan will ever remove it. A host's OS disk is the one that gets left: it is inline on the VM and no plan names it either way. Confirm with `az resource list -g ",
        data.azurerm_resource_group.main.name,
        " --query \"[?contains(name,'<name>')].{name:name,type:type}\" -o table`, and delete a disk nothing needs with `az disk delete -g ",
        data.azurerm_resource_group.main.name,
        " -n <name>`.\n",
      ]) : "",
      length(local.inventory_missing) > 0 ? join("", [
        "\nMissing, which means the inventory read itself is wrong rather than the group: ",
        join(", ", sort(tolist(local.inventory_missing))),
        "\nThese must exist in every environment. Not seeing them means the ARM read returned less than the group holds, or a type in inventory_types no longer matches what Azure reports. Do not read the absence of strays above as a clean group until this clause is quiet.\n",
      ]) : "",
    ])
  }
}
