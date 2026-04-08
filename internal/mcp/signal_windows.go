//go:build windows

package mcp

import "syscall"

func sigterm() syscall.Signal { return syscall.SIGKILL }
