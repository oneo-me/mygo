//go:build darwin && !ios

package mygo

import (
	"github.com/egoist/mygo/internal/darwin"
	"github.com/egoist/mygo/internal/platform"
)

func newBackend() platform.Backend { return darwin.New() }

func enableNotificationSenders() { darwin.EnableNotificationSenders() }
