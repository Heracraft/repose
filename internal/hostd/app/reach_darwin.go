package app

import (
	"os"
	"syscall"
)

// canPass reports whether uid (group gid) may search the directory st
// describes: the owner's, the group's or everyone's x bit, whichever
// applies. Root passes everywhere.
func canPass(st os.FileInfo, uid, gid int) bool {
	if uid == 0 {
		return true
	}
	sys, ok := st.Sys().(*syscall.Stat_t)
	if !ok {
		return true
	}
	mode := st.Mode().Perm()
	switch {
	case int(sys.Uid) == uid:
		return mode&0o100 != 0
	case int(sys.Gid) == gid:
		return mode&0o010 != 0
	default:
		return mode&0o001 != 0
	}
}
