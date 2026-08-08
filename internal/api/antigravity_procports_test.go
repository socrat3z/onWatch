package api

import (
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
)

const procNetTCPSample = `  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 0100007F:F70C 00000000:0000 0A 00000000:00000000 00:00000000 00000000 65532        0 400100 1 0000 100 0 0 10 0
   1: 0100007F:1F90 00000000:0000 0A 00000000:00000000 00:00000000 00000000 65532        0 999999 1 0000 100 0 0 10 0
   2: 0100007F:0050 0100007F:C1B4 01 00000000:00000000 00:00000000 00000000 65532        0 400100 1 0000 100 0 0 10 0
`

func TestParseProcNetListenPorts(t *testing.T) {
	inodes := map[string]struct{}{"400100": {}}

	ports := parseProcNetListenPorts(procNetTCPSample, inodes)

	if len(ports) != 1 || ports[0] != 63244 {
		t.Fatalf("expected [63244] (0xF70C), got %v", ports)
	}
}

func TestParseProcNetListenPortsIgnoresUnownedAndNonListening(t *testing.T) {
	ports := parseProcNetListenPorts(procNetTCPSample, map[string]struct{}{"12345": {}})
	if len(ports) != 0 {
		t.Fatalf("expected no ports for unknown inode, got %v", ports)
	}

	// Inode 400100 also appears on an ESTABLISHED (st=01) row that must be skipped.
	ports = parseProcNetListenPorts(procNetTCPSample, map[string]struct{}{"400100": {}, "999999": {}})
	if len(ports) != 2 {
		t.Fatalf("expected both listening ports, got %v", ports)
	}
}

func TestParseProcStatPPIDHandlesSpacedComm(t *testing.T) {
	ppid, ok := parseProcStatPPID("42 (agy language server) S 7 42 42 0 -1 4194304")
	if !ok || ppid != 7 {
		t.Fatalf("expected ppid 7, got %d (ok=%v)", ppid, ok)
	}

	if _, ok := parseProcStatPPID("garbage"); ok {
		t.Fatal("expected failure on malformed stat")
	}
}

func TestParseSocketInode(t *testing.T) {
	if inode, ok := parseSocketInode("socket:[400100]"); !ok || inode != "400100" {
		t.Fatalf("got %q ok=%v", inode, ok)
	}
	for _, bad := range []string{"/dev/null", "socket:[]", "socket:[400100", "pipe:[7]"} {
		if _, ok := parseSocketInode(bad); ok {
			t.Fatalf("expected %q to be rejected", bad)
		}
	}
}

// writeFakeProc builds a minimal procfs tree: pid 100 with child 101 that owns
// the listening socket, mirroring agy delegating to a child language server.
func writeFakeProc(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	mkProc := func(pid, ppid int, sockets []string) {
		dir := filepath.Join(root, strconv.Itoa(pid))
		if err := os.MkdirAll(filepath.Join(dir, "fd"), 0o755); err != nil {
			t.Fatal(err)
		}
		stat := strconv.Itoa(pid) + " (agy) S " + strconv.Itoa(ppid) + " 1 1 0 -1 0"
		if err := os.WriteFile(filepath.Join(dir, "stat"), []byte(stat), 0o644); err != nil {
			t.Fatal(err)
		}
		for i, target := range sockets {
			link := filepath.Join(dir, "fd", strconv.Itoa(i+3))
			if err := os.Symlink(target, link); err != nil {
				t.Skipf("symlinks unavailable: %v", err)
			}
		}
	}

	mkProc(100, 1, []string{"/dev/null"})
	mkProc(101, 100, []string{"socket:[400100]"})

	if err := os.MkdirAll(filepath.Join(root, "net"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "net", "tcp"), []byte(procNetTCPSample), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestDiscoverPortsProcFindsChildListener(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("procfs layout test uses unix symlinks")
	}
	root := writeFakeProc(t)

	ports, err := discoverPortsProc(root, 100)
	if err != nil {
		t.Fatalf("discoverPortsProc: %v", err)
	}
	if len(ports) != 1 || ports[0] != 63244 {
		t.Fatalf("expected [63244], got %v", ports)
	}
}

func TestDiscoverPortsProcMissingPID(t *testing.T) {
	root := t.TempDir()
	if _, err := discoverPortsProc(root, 4242); err == nil {
		t.Fatal("expected error for missing pid")
	}
}

// TestDiscoverPortsProcRealListener validates the discovery path against a real
// kernel procfs, which is the environment that broke in Docker.
func TestDiscoverPortsProcRealListener(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("procfs is linux-only")
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	want := ln.Addr().(*net.TCPAddr).Port

	ports, err := discoverPortsProc("/proc", os.Getpid())
	if err != nil {
		t.Fatalf("discoverPortsProc: %v", err)
	}
	for _, p := range ports {
		if p == want {
			return
		}
	}
	t.Fatalf("listening port %d not found in %v", want, ports)
}
