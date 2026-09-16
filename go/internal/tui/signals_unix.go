//go:build !windows

package tui

import (
	"os"
	"syscall"
)

var resizeSignals = []os.Signal{syscall.SIGWINCH}

func isResizeSignal(sig os.Signal) bool {
	return sig == syscall.SIGWINCH
}
