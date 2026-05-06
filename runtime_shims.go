package main

import (
	"context"
	"net"
	"net/http"
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
	contextWithTimeout = context.WithTimeout
	httpRoundTripper   = http.DefaultTransport
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
