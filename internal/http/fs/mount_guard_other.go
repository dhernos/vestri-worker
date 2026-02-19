//go:build !linux

package fs

func pathCrossesMountPoint(base, full string) (bool, error) {
	return false, nil
}
