package main

import (
	"net"
	"os"
)

var (
	osReadFile      = os.ReadFile
	netDialTimeout  = net.DialTimeout
	netJoinHostPort = net.JoinHostPort
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
