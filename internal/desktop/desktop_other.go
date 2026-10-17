//go:build !windows

package desktop

import "errors"

func Run(base, admin string) error {
	return errors.New("desktop window requires Windows; use --no-browser")
}
