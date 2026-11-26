-- A machine parked on a Mac without the appliance, waiting for the Mac's
-- user to log in before it can be restored (DECISIONS I-1672): when it was
-- parked, null on every other minute. Only an owned host's sample can set
-- it (meter/ingest.go); every row before this, and every fleet row, is
-- null.
alter table meter_samples add column if not exists parked_waiting_since timestamptz;
