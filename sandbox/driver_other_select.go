//go:build !linux && !darwin

package sandbox

import "fmt"

func NewDriver() (Driver, error) {
	return nil, fmt.Errorf("sandbox: unsupported platform")
}
