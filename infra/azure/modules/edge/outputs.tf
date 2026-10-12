output "public_ip" {
  description = "Static address ssh.repose.herakraft.co points at and every host dials for WireGuard."
  value       = azurerm_public_ip.main.ip_address
}

output "private_ip" {
  description = "Edge address inside the VNet."
  value       = azurerm_network_interface.main.private_ip_address
}

output "ssh_jump" {
  description = "Operator jump target for every host."
  value       = "root@${azurerm_public_ip.main.ip_address}:${var.operator_ssh_port}"
}

output "operator_ssh_port" {
  description = "Port the operator sshd listens on."
  value       = var.operator_ssh_port
}

output "installed" {
  description = "Known once the edge is running NixOS; hosts depend on it because it is their jump host."
  value       = module.install.post_install_id
}

output "vm_id" {
  description = "Azure resource id of the edge VM, for the availability alert (DECISIONS I-648)."
  value       = azurerm_linux_virtual_machine.main.id
}
