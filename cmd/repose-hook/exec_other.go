//go:build !unix

package main

import "errors"

func execve(string, []string, []string) error {
	return errors.New("repose-mcp runs in a Linux guest only")
}
