//go:build linux

package sandbox

func NewDriver() (Driver, error) {
	return linuxDriver{}, nil
}
