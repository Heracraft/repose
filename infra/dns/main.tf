# Cloudflare records for the names repose answers on, in whichever zone
# the environment names (prod: repose.run, DECISIONS I-1156).
#
# This is a module, not a root, so DNS is created in the same apply as the
# addresses it points at (docs/workstreams/11-infra-opentofu.md §2). A record
# that outlives the IP it names is how `ssh.repose.run` starts
# resolving to somebody else's VM.
#
# Names: docs/DECISIONS.md R4-12, I-15 and I-1156. repose.run (dashboard),
# api.repose.run, ssh.repose.run (gateway). Logto is the owner's existing
# accounts.herakraft.co (I-84), not a name here, and neither are the old
# repose.herakraft.co names that keep answering through the overlap. The
# wildcard for preview URLs is not created; previews are out of scope for
# the first release (DESIGN §18).

terraform {
  required_version = ">= 1.6.0"
  required_providers {
    cloudflare = {
      source  = "cloudflare/cloudflare"
      version = ">= 5.0, < 6.0"
    }
  }
}

resource "cloudflare_dns_record" "a" {
  for_each = var.records

  zone_id = var.zone_id
  name    = each.key
  type    = "A"
  content = each.value.address
  ttl     = each.value.proxied ? 1 : var.ttl
  proxied = each.value.proxied
  comment = each.value.comment
}
