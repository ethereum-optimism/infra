//go:build !windows

package proxyd

func setSockOptLinger(fd int) error {
	// setSockOptLinger is not supported on Windows
	return nil
}
