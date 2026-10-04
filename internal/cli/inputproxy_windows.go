//go:build windows

package cli

// runInputProxy is not used on Windows (I-280): the CLI runs ssh with
// the console as it always has, and `repose cp` is the way to send a file.
func runInputProxy(args []string, h *dropHandler, re *reattacher) (handled bool, err error) {
	return false, nil
}
