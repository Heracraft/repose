//go:build !unix

package testdb

// The dev shell and CI are both Linux, and CI uses DATABASE_URL rather than
// a throwaway cluster, so there is nothing to own or reap elsewhere.
func claim(string) {}
func reap()        {}
