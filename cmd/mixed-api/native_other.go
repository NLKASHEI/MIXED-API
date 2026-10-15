//go:build !windows

package main

import (
	"errors"
	"github.com/NLKASHEI/MIXED-API/internal/gateway"
)

func startNative(_ gateway.Config, _ *gateway.Server, _ bool) error {
	return errors.New("native desktop GUI is Windows only")
}
