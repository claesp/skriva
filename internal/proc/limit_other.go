//go:build !linux

package proc

import "errors"

// setDataLimit is not available on this system.
func setDataLimit(int, uint64) error {
	return errors.New("memory limits are available only on Linux")
}
