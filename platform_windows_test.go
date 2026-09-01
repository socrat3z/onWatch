//go:build windows

package main

import (
	"syscall"
	"testing"
)

func TestDaemonSysProcAttr_Windows(t *testing.T) {
	attr := daemonSysProcAttr()
	if attr == nil {
		t.Fatal("expected non-nil SysProcAttr")
	}
	if !attr.HideWindow {
		t.Fatal("expected HideWindow=true")
	}
	if attr.CreationFlags&syscall.CREATE_NEW_PROCESS_GROUP == 0 {
		t.Fatal("expected CREATE_NEW_PROCESS_GROUP flag")
	}
}
