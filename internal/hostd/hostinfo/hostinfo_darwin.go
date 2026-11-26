package hostinfo

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

// The host figures of a Mac (design 2, DECISIONS I-1664), from sysctl and
// vm_stat where Linux reads /proc.

// BootID returns kern.bootsessionuuid, new at every boot.
func BootID() (string, error) {
	return unix.Sysctl("kern.bootsessionuuid")
}

// BootTime returns kern.boottime.
func BootTime() (time.Time, error) {
	tv, err := unix.SysctlTimeval("kern.boottime")
	if err != nil {
		return time.Time{}, err
	}
	return time.Unix(tv.Sec, int64(tv.Usec)*1000).UTC(), nil
}

// MemInfo returns hw.memsize and what macOS can give without paging out:
// free, inactive, speculative and purgeable pages, as vm_stat counts them.
func MemInfo() (total, avail uint64, err error) {
	total, err = unix.SysctlUint64("hw.memsize")
	if err != nil {
		return 0, 0, err
	}
	out, err := exec.Command("/usr/bin/vm_stat").Output()
	if err != nil {
		return total, 0, err
	}
	avail, err = vmStatAvail(out)
	return total, avail, err
}

func vmStatAvail(out []byte) (uint64, error) {
	sc := bufio.NewScanner(bytes.NewReader(out))
	var page uint64
	var pages uint64
	for sc.Scan() {
		l := sc.Text()
		if strings.HasPrefix(l, "Mach Virtual Memory Statistics:") {
			if i := strings.Index(l, "page size of "); i >= 0 {
				f := strings.Fields(l[i+len("page size of "):])
				page, _ = strconv.ParseUint(f[0], 10, 64)
			}
			continue
		}
		k, v, ok := strings.Cut(l, ":")
		if !ok {
			continue
		}
		switch strings.TrimSpace(k) {
		case "Pages free", "Pages inactive", "Pages speculative", "Pages purgeable":
			n, err := strconv.ParseUint(strings.TrimSuffix(strings.TrimSpace(v), "."), 10, 64)
			if err == nil {
				pages += n
			}
		}
	}
	if page == 0 {
		return 0, errors.New("hostinfo: vm_stat printed no page size")
	}
	return pages * page, nil
}

// Load1 reads vm.loadavg.
func Load1() float64 {
	b, err := unix.SysctlRaw("vm.loadavg")
	if err != nil || len(b) < 16 {
		return 0
	}
	// struct loadavg { fixpt_t ldavg[3]; long fscale; }: 32-bit fixed
	// point, then (padded) a 64-bit scale.
	l0 := binary.LittleEndian.Uint32(b[0:4])
	scale := binary.LittleEndian.Uint64(b[len(b)-8:])
	if scale == 0 {
		return 0
	}
	return float64(l0) / float64(scale)
}
