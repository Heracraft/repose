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
# In a `bash -c` string it also defines bash's command_not_found_handle,
# which names the nixpkgs package of an unknown command (I-219, I-516).
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
# A `bash -c` string (how an agent runs each command) gets the same
# command-not-found hint as an interactive shell (DECISIONS I-219, I-516),
# unless something already defined a handler. A script file and sh keep
# bash's plain message: BASH_EXECUTION_STRING is set only for -c.
if [ -n "${BASH_VERSION-}" ] && [ -n "${BASH_EXECUTION_STRING-}" ]; then
  # By absolute path: bash runs the handler in a child, and a command the
  # handler cannot find calls the handler again in a grandchild. With a
  # PATH that lacked repose-command-not-found, one unknown command forked
  # bash until the machine ran out of memory (DECISIONS I-577).
  # shellcheck disable=SC3044 # bash only: BASH_VERSION is set
  declare -F command_not_found_handle >/dev/null 2>&1 || command_not_found_handle() {
    unset -f command_not_found_handle
    if [ -x /run/current-system/sw/bin/repose-command-not-found ]; then
      /run/current-system/sw/bin/repose-command-not-found "$1"
    else
      printf '%s: command not found\n' "$1" >&2
    fi
    return 127
  }
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
