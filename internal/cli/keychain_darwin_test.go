//go:build darwin

package cli

import (
	"errors"
	"sync"
)

// The tests' keychain is a map: a test never writes to, or waits on, the
// Keychain of the Mac it runs on.
func init() {
	var mu sync.Mutex
	items := map[string]string{}
	keychainGet = func(account string) (string, bool) {
		mu.Lock()
		defer mu.Unlock()
		v, ok := items[account]
		return v, ok && account != ""
	}
	keychainSet = func(account, value string) error {
		mu.Lock()
		defer mu.Unlock()
		items[account] = value
		return nil
	}
	keychainDelete = func(account string) error {
		mu.Lock()
		defer mu.Unlock()
		if _, ok := items[account]; !ok {
			return errors.New("security: the specified item could not be found in the keychain")
		}
		delete(items, account)
		return nil
	}
}
