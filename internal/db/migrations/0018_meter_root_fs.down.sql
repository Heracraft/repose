-- Reverts 0018: samples carry no root filesystem figure.
alter table meter_samples drop column root_size;
alter table meter_samples drop column root_used;
