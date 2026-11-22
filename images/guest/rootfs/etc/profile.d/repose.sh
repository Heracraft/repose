# repose guest profile: sourced by every shell (/etc/profile, /etc/bash.bashrc
# and the agent wrappers; nix/guest/base/env.nix).
if [ -z "${REPOSE:-}" ] && [ -r /etc/repose/session.env ]; then
  set -a
  . /etc/repose/session.env
  set +a
fi
if [ -r /etc/repose/env ]; then
  set -a
  . /etc/repose/env
  set +a
fi
# Secrets through the same guarded refresh as BASH_ENV (I-475).
if [ -r /run/repose/secrets.refresh ]; then
  . /etc/repose/bash-env.sh
elif [ -r /run/repose/secrets.env ]; then
  . /run/repose/secrets.env
fi
# DISPLAY only while the on-demand desktop's X server is up.
if [ -S /tmp/.X11-unix/X99 ]; then
  export DISPLAY=:99
else
  unset DISPLAY
fi
# mise (DECISIONS I-1466): `mise use -g` tools on PATH in every bash, the
# shims for the commands no shell activates.
if [ -n "${BASH_VERSION:-}" ] && command -v mise >/dev/null 2>&1; then
  eval "$(mise activate bash)"
fi
