#!/bin/sh
# A stand-in for EXEC_A in the disk row's fixture test: it answers the
# row's probes the way a guest of runtime $FAKE_RUNTIME (nix or erofs)
# does. The script to run is the last argument.
script=$1
case "$script" in
  *"mountpoint -q /run/repose/rw"*)
    [ "$FAKE_RUNTIME" = erofs ] && echo erofs || echo nix ;;
  *"NAME,TYPE"*)
    if [ "$FAKE_RUNTIME" = erofs ]; then printf 'vda\nvdb\n'; else echo vda; fi ;;
  *"NAME,RO"*)
    printf 'vda    1\nvdb    0\n' ;;
  *"of=/dev/vda"*)
    echo "dd: failed to open '/dev/vda': Read-only file system"; exit 1 ;;
  *"test -e /dev/vg-guests"*)
    exit 1 ;;
  *)
    echo "fake guest: no answer for: $script" >&2; exit 2 ;;
esac
