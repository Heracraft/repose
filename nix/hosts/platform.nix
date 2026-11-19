# What every host that answers to the production api needs to find it,
# wherever the host is: the internal name the api's certificate carries,
# the platform x509 host CA that signs it, and the repository hostd clones
# base checkouts from. prod.nix (the Azure fleet, which dials the api at
# its VNet address) and mac.nix (the appliance, which dials it at the
# control plane's hub address, I-1382) both read it, so the two cannot
# drift apart. None of it is secret: the CA is public material
# (`repose-admin ca show`) and the rest are names.
{
  # The internal host-to-api name keeps the herakraft.co domain after the
  # move to repose.run (DECISIONS I-1156); moving it reissues the api's
  # certificate and switches every host and the edge in one window.
  apiServerName = "api.repose.herakraft.co";

  apiCA = ''
    -----BEGIN CERTIFICATE-----
    MIIBYzCCAQmgAwIBAgIBATAKBggqhkjOPQQDAjAZMRcwFQYDVQQDEw5yZXBvc2Ug
    aG9zdCBjYTAeFw0yNjA5MjAxNDM1NDRaFw0zNjA5MTcxNTM1NDRaMBkxFzAVBgNV
    BAMTDnJlcG9zZSBob3N0IGNhMFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcDQgAEEf0Z
    a+ShTl8C17TyIihGfxGA8/+LPSztZO7U+1jkulN/0PyP+u0mFtoz1No7eNIsLQdI
    4XqvAoE1SQ5NJMb9+6NCMEAwDgYDVR0PAQH/BAQDAgKEMA8GA1UdEwEB/wQFMAMB
    Af8wHQYDVR0OBBYEFJ3mh/uToG0kXZreInW+yRCV7Vv0MAoGCCqGSM49BAMCA0gA
    MEUCIQD4CrivKkoYjKlkHwnK7Ors2lohLv4cFQ+yUU5IhKmIUgIgRiWHt/xsWPzy
    pgFYxi/RgOzA9GSronavaaOscYi+Vng=
    -----END CERTIFICATE-----
  '';

  # hostd clones the base checkout it builds fragments from (I-28); the
  # repository is public.
  baseRepoUrl = "https://github.com/Heracraft/repose.git";
}
