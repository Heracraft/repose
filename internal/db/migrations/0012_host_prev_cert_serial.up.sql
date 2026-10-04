-- A host's certificate serial is enforced (DECISIONS I-432): the api admits
-- the serial in cert_serial, and the one it replaced until the host first
-- connects with the new certificate, so a rotate whose files never reached
-- disk does not lock the host out.
alter table hosts add column prev_cert_serial text;
