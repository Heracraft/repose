-- Reverts 0047. Every account goes back to copying the gh login unless
-- its own laptops say otherwise, and no token expiry is held.
alter table users drop column gh_token_expires_at;
alter table users drop column gh_login_copy;
