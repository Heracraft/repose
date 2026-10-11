output "edge_public_ip" {
  description = "ssh.repose.herakraft.co and every host's WireGuard endpoint."
  value       = module.environment.edge_public_ip
}

output "edge_ssh_jump" {
  description = "Operator jump target for reaching hosts."
  value       = module.environment.edge_ssh_jump
}

output "control_public_ip" {
  description = "Where repose.herakraft.co and api.repose.herakraft.co resolve."
  value       = module.environment.control_public_ip
}

output "control_private_ip" {
  description = "Where hosts reach the api's gRPC listener on the VNet (nix/hosts/<host>.nix apiAddr)."
  value       = module.environment.control_private_ip
}

output "guest_egress_ip" {
  description = "Address every guest egresses from."
  value       = module.environment.guest_egress_ip
}

output "hosts" {
  description = "Per-host private address, jump target and class."
  value       = module.environment.hosts
}

output "snapshots" {
  description = "Snapshot account, container, endpoint and the host identity's client id."
  value       = module.environment.snapshots
}

output "keyvault" {
  description = "Vault URI and wrapping-key id for the api's environment."
  value       = module.environment.keyvault
}

output "fqdns" {
  description = "DNS names created."
  value       = module.environment.fqdns
}

output "freeze_keyvault" {
  description = "The guest freeze's vault URI and key name (DECISIONS I-653)."
  value       = module.environment.freeze_keyvault
}
