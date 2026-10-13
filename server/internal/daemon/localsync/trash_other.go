//go:build !darwin

package localsync

import "fmt"

func defaultMoveToTrash(path string) error {
	return fmt.Errorf("cannot move %q to the system trash on this platform", path)
}
