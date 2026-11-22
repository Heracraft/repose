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
    # What staging's Ubuntu guest printed: the open of a readonly=on
    # virtio-blk device succeeds and the write is refused with EPERM.
    echo "dd: error writing '/dev/vda': Operation not permitted"; exit 1 ;;
  *"test -e /dev/vg-guests"*)
    exit 1 ;;
  *"findmnt -no FSTYPE,OPTIONS /run/repose/ro"*)
    echo "erofs ro,relatime,user_xattr,acl,cache_strategy=readaround" ;;
  *"test -d /run/repose/ro"*)
    exit 0 ;;
  *"touch /usr/"*)
    exit 1 ;;  # the write lands in the overlay's upper; the base has no probe
  *"touch /run/repose/ro/"*)
    echo "touch: cannot touch '/run/repose/ro/repose-isolation-probe': Read-only file system"; exit 1 ;;
  *"/nix/store"*|*"/nix/.ro-store"*)
    [ "$FAKE_RUNTIME" = nix ] || { echo "No such file or directory"; exit 1; }
    case "$script" in
      *"ls -A"*) echo; echo 0 ;;
      *) echo "Read-only file system"; exit 1 ;;
    esac ;;
  *)
    echo "fake guest: no answer for: $script" >&2; exit 2 ;;
esac
