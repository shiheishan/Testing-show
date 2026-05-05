package main

import (
	"context"
	"net"
	"os"
	"os/exec"
)

var (
	osReadFile         = os.ReadFile
	netDialTimeout     = net.DialTimeout
	netJoinHostPort    = net.JoinHostPort
	execCommandContext = exec.CommandContext
	execLookPath       = exec.LookPath
	osMkdirTemp        = os.MkdirTemp
	osWriteFile        = os.WriteFile
	osRemoveAll        = os.RemoveAll
	contextBackground  = context.Background
)

func isTimeoutError(err error) bool {
	if err == nil {
		return false
	}
	if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
		return true
	}
	return false
}
