//go:build darwin

package sandbox

func NewDriver() (Driver, error) {
	return darwinDriver{}, nil
}
