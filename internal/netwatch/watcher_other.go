//go:build !darwin && !linux

package netwatch

func current() (NetworkInfo, error) {
	return NetworkInfo{}, nil
}
