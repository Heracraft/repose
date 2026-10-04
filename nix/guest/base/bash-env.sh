# repose guest: the BASH_ENV of every bash run by dev (nix/guest/base/env.nix,
# DECISIONS I-475), also sourced by /etc/profile.d/repose.sh. A
# non-interactive bash, which is how an agent runs each command, reads no
# profile, so without this it keeps the secrets its agent started with.
#
# When guestd has written the secrets since this process's environment was
# formed (REPOSE_ENV_GEN differs from the first line of
# /run/repose/secrets.refresh), this sources that file. Its lines set a
# secret the process lacks, replace a value guestd exported earlier, and
# unset a removed secret the process still holds as guestd exported it. A
# value the process, its parent or the project's .envrc set itself stays.
#
# Sourced into the caller's shell, so it runs no other program, prints
# nothing, changes no shell option or positional parameter, keeps $_ (a
# script's first command still sees the script's path), leaves $? at 0 and
# never fails under set -euo pipefail. It leaves one unexported variable,
# __repose_bash_env_u, behind: unsetting it would change $_. xtrace and
# verbose are switched off while it runs, so `bash -x` never echoes a
# secret's value (`bash -v` echoes this file's own lines, which hold none).
# POSIX sh, for the profile.
{ __repose_bash_env_u=$_; __repose_bash_env_opts=$-; set +xv; } 2>/dev/null
if [ -z "${__repose_bash_env_line+set}" ] && [ -r /run/repose/secrets.refresh ]; then
  __repose_bash_env_line=
  if { IFS= read -r __repose_bash_env_line; } 2>/dev/null </run/repose/secrets.refresh &&
    [ "$__repose_bash_env_line" != "# repose-env-gen ${REPOSE_ENV_GEN-}" ]; then
    # shellcheck source=/dev/null
    . /run/repose/secrets.refresh 2>/dev/null || :
  fi
  unset __repose_bash_env_line
fi
case $__repose_bash_env_opts in
*x*v* | *v*x*) __repose_bash_env_opts=-xv ;;
*x*) __repose_bash_env_opts=-x ;;
*v*) __repose_bash_env_opts=-v ;;
*) __repose_bash_env_opts=+xv ;;
esac
# Restoring the options and $_ is the last command, and its trace goes to
# /dev/null, so nothing of this file is traced. `:` with the saved value as
# its last argument is what puts $_ back.
{ set "$__repose_bash_env_opts"; unset __repose_bash_env_opts; : "$__repose_bash_env_u"; } 2>/dev/null
