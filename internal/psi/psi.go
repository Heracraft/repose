// Package psi reads Linux pressure stall information: the "some" line of a
// cpu.pressure file (/proc/pressure/cpu in a guest, a unit cgroup's
// cpu.pressure on a host). DECISIONS I-493.
package psi

import (
	"bytes"
	"fmt"
	"os"
	"strconv"
)

// SomeTotal parses the cumulative "some" total, in microseconds, from the
// contents of a pressure file:
//
//	some avg10=0.00 avg60=0.00 avg300=0.00 total=12345
//	full avg10=0.00 avg60=0.00 avg300=0.00 total=0
func SomeTotal(data []byte) (uint64, error) {
	for _, line := range bytes.Split(data, []byte("\n")) {
		f := bytes.Fields(line)
		if len(f) == 0 || string(f[0]) != "some" {
			continue
		}
		for _, kv := range f[1:] {
			if v, ok := bytes.CutPrefix(kv, []byte("total=")); ok {
				return strconv.ParseUint(string(v), 10, 64)
			}
		}
	}
	return 0, fmt.Errorf("psi: no some total")
}

// ReadSomeTotal reads path and parses it with SomeTotal.
func ReadSomeTotal(path string) (uint64, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return SomeTotal(b)
}
