//go:build ios

package mygo

import (
	"github.com/egoist/mygo/internal/ios"
	"github.com/egoist/mygo/internal/platform"
)

func newBackend() platform.Backend { return ios.New() }
