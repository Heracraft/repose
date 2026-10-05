package sample

import (
	"bufio"
	"bytes"
	"os"
	"strconv"
)

// memUsed is MemTotal less MemAvailable from a meminfo file, in bytes: the
// memory the guest's own processes and kernel hold, as `free` reports it
// (DECISIONS I-493). 0 when the file cannot be read or lacks either line.
func memUsed(path string) uint64 {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	var total, avail uint64
	var haveTotal, haveAvail bool
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		f := bytes.Fields(sc.Bytes())
		if len(f) < 2 {
			continue
		}
		v, err := strconv.ParseUint(string(f[1]), 10, 64)
		if err != nil {
			continue
		}
		switch string(f[0]) {
		case "MemTotal:":
			total, haveTotal = v<<10, true
		case "MemAvailable:":
			avail, haveAvail = v<<10, true
		}
	}
	if !haveTotal || !haveAvail || avail > total {
		return 0
	}
	return total - avail
}
