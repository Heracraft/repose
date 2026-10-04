# repose guest: the BASH_ENV of every bash run by dev (nix/guest/base/env.nix,
# DECISIONS I-475), also sourced by /etc/profile.d/repose.sh. A
# non-interactive bash, which is how an agent runs each command, reads no
# profile, so without this it keeps the secrets its agent started with.
#
# When guestd has written the secrets since this process's environment was
# formed (REPOSE_ENV_GEN differs from the first line of
# /run/repose/secrets.refresh), this sources that file. Its lines replace or
# unset a secret only where the process still holds the value its own
# generation delivered, so a value the process, its parent or the project's
# .envrc set on purpose stays.
#
# Sourced into the caller's shell, so it runs no other program, prints
# nothing, changes no shell option or positional parameter, leaves $? at 0
# and never fails under set -euo pipefail. xtrace and verbose are switched
# off while it runs, so `bash -x` never echoes a secret's value (`bash -v`
# echoes this file's own lines, which hold none). POSIX sh, for the profile.
{ __repose_bash_env_opts=$-; set +xv; } 2>/dev/null
if [ -z "${__repose_bash_env_line+set}" ] && [ -r /run/repose/secrets.refresh ]; then
  __repose_bash_env_line=
  if { IFS= read -r __repose_bash_env_line; } 2>/dev/null </run/repose/secrets.refresh &&
    [ "$__repose_bash_env_line" != "# repose-env-gen ${REPOSE_ENV_GEN-}" ]; then
    # shellcheck source=/dev/null
    . /run/repose/secrets.refresh 2>/dev/null || :
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
