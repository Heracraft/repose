# repose guest: the BASH_ENV of every bash run by dev (nix/guest/base/env.nix,
# DECISIONS I-475). A non-interactive bash, which is how an agent runs each
# command, reads no profile, so without this it keeps the secrets its agent
# started with. This brings them up to date from /run/repose/secrets.env when
# guestd has written that file since this process's environment was formed
# (REPOSE_SECRETS_GEN differs from the file's first line).
#
# Sourced into the caller's shell, so it runs no other program, prints
# nothing, changes no shell option or positional parameter, leaves $? at 0
# and never fails under set -euo pipefail. xtrace and verbose are switched
# off while it runs, so `bash -x` never echoes a secret's value (`bash -v`
# echoes this file's own lines, which hold none).
{ __repose_bash_env_opts=$-; set +xv; } 2>/dev/null
if [ -z "${__repose_bash_env_line+set}" ] && [ -r /run/repose/secrets.env ]; then
  __repose_bash_env_line=
  if { IFS= read -r __repose_bash_env_line; } 2>/dev/null </run/repose/secrets.env &&
    [ "$__repose_bash_env_line" != "export REPOSE_SECRETS_GEN=${REPOSE_SECRETS_GEN-}" ]; then
    # shellcheck source=/dev/null
    . /run/repose/secrets.env 2>/dev/null || :
  fi
  unset __repose_bash_env_line
fi
# Restoring the options is the last command, so nothing after it is traced.
case $__repose_bash_env_opts in
*x*v* | *v*x*) unset __repose_bash_env_opts; set -xv ;;
*x*) unset __repose_bash_env_opts; set -x ;;
*v*) unset __repose_bash_env_opts; set -v ;;
*) unset __repose_bash_env_opts ;;
esac
