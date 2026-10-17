//go:build !windows

package desktop

import (
	"context"
	"errors"
	"github.com/NLKASHEI/MIXED-API/internal/googleweb"
)

func Run(base, admin string) error {
	return errors.New("desktop window requires Windows; use --no-browser")
}

func ShowError(message string) {}
func NewGoogleSessions(root string) *googleweb.Browser {
	return googleweb.New(func(context.Context, string) (googleweb.Page, error) {
		return nil, errors.New("来源窗口需要 Windows 桌面环境")
	})
}
