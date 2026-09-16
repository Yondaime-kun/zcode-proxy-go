//go:build windows

package tui

import (
	"os"
)

var resizeSignals = []os.Signal{}

func isResizeSignal(sig os.Signal) bool {
	return false
}
