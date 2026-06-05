package main

import (
	"context"
	"net"
	"os"
	"os/exec"
)

var (
	osReadFile         = os.ReadFile
	execCommandContext = exec.CommandContext
	execLookPath       = exec.LookPath
	osMkdirTemp        = os.MkdirTemp
	osWriteFile        = os.WriteFile
	osRemoveAll        = os.RemoveAll
	contextBackground  = context.Background
	// lookupHostIPs resolves a hostname to its IP addresses. It is a package
	// var so the SSRF guard's hostname check can be exercised deterministically
	// in tests (rebind names, metadata fronts) without real DNS.
	lookupHostIPs = net.LookupIP
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
