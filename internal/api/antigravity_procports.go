package api

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// procTCPListen is the hex state code for TCP_LISTEN in /proc/net/tcp{,6}.
const procTCPListen = "0A"

// procNetFiles are the network tables scanned for listening sockets.
var procNetFiles = []string{"tcp", "tcp6"}

// discoverPortsProc finds listening TCP ports owned by pid (or any of its
// descendants) by matching the processes' socket inodes against /proc/net/tcp
// and /proc/net/tcp6. Unlike ss/netstat this needs no external binaries, so it
// works inside minimal container images where iproute2 and net-tools are absent
// (see the Docker runtime stage in the Dockerfile).
func discoverPortsProc(procRoot string, pid int) ([]int, error) {
	inodes, err := procSocketInodes(procRoot, pid)
	if err != nil {
		return nil, err
	}
	if len(inodes) == 0 {
		return nil, nil
	}

	seen := make(map[int]struct{})
	var ports []int
	for _, name := range procNetFiles {
		data, rerr := os.ReadFile(filepath.Join(procRoot, "net", name))
		if rerr != nil {
			continue
		}
		for _, port := range parseProcNetListenPorts(string(data), inodes) {
			if _, dup := seen[port]; dup {
				continue
			}
			seen[port] = struct{}{}
			ports = append(ports, port)
		}
	}
	return ports, nil
}

// parseProcNetListenPorts extracts the local ports of listening sockets whose
// inode is in inodes. Lines look like:
//
//	sl local_address rem_address st tx_rx tr_tm retrnsmt uid timeout inode ...
//	 0: 0100007F:1F90 00000000:0000 0A ...            1000        0 12345 1 ...
func parseProcNetListenPorts(content string, inodes map[string]struct{}) []int {
	var ports []int
	for _, line := range strings.Split(content, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 10 || fields[3] != procTCPListen {
			continue
		}
		if _, ok := inodes[fields[9]]; !ok {
			continue
		}
		sep := strings.LastIndex(fields[1], ":")
		if sep < 0 {
			continue
		}
		port, err := strconv.ParseUint(fields[1][sep+1:], 16, 32)
		if err != nil || port == 0 || port > 65535 {
			continue
		}
		ports = append(ports, int(port))
	}
	return ports
}

// procSocketInodes collects the socket inodes held by pid and its descendants.
// agy may serve its quota API from a child language-server process, so limiting
// the scan to pid alone would miss the listener.
func procSocketInodes(procRoot string, pid int) (map[string]struct{}, error) {
	pids := procProcessTree(procRoot, pid)

	inodes := make(map[string]struct{})
	var firstErr error
	for _, p := range pids {
		fdDir := filepath.Join(procRoot, strconv.Itoa(p), "fd")
		entries, err := os.ReadDir(fdDir)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		for _, e := range entries {
			target, err := os.Readlink(filepath.Join(fdDir, e.Name()))
			if err != nil {
				continue // fd closed between listing and readlink
			}
			if inode, ok := parseSocketInode(target); ok {
				inodes[inode] = struct{}{}
			}
		}
	}
	if len(inodes) == 0 && firstErr != nil {
		return nil, fmt.Errorf("antigravity: reading %s fds: %w", procRoot, firstErr)
	}
	return inodes, nil
}

// parseSocketInode extracts "12345" from an fd link target of "socket:[12345]".
func parseSocketInode(target string) (string, bool) {
	const prefix = "socket:["
	if !strings.HasPrefix(target, prefix) || !strings.HasSuffix(target, "]") {
		return "", false
	}
	inode := target[len(prefix) : len(target)-1]
	if inode == "" {
		return "", false
	}
	return inode, true
}

// procProcessTree returns pid followed by all of its descendants found in
// procRoot. On any read failure it degrades to just pid.
func procProcessTree(procRoot string, pid int) []int {
	children := procChildren(procRoot)

	tree := []int{pid}
	for i := 0; i < len(tree); i++ {
		tree = append(tree, children[tree[i]]...)
	}
	return tree
}

// procChildren maps each parent PID to its direct children by reading the ppid
// field of every /proc/<pid>/stat.
func procChildren(procRoot string) map[int][]int {
	children := make(map[int][]int)
	entries, err := os.ReadDir(procRoot)
	if err != nil {
		return children
	}
	for _, e := range entries {
		p, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		data, err := os.ReadFile(filepath.Join(procRoot, e.Name(), "stat"))
		if err != nil {
			continue
		}
		if ppid, ok := parseProcStatPPID(string(data)); ok {
			children[ppid] = append(children[ppid], p)
		}
	}
	return children
}

// parseProcStatPPID reads the ppid from a /proc/<pid>/stat line. The comm field
// is parenthesized and may contain spaces, so parsing starts after its close.
func parseProcStatPPID(stat string) (int, bool) {
	close := strings.LastIndex(stat, ")")
	if close < 0 {
		return 0, false
	}
	fields := strings.Fields(stat[close+1:])
	if len(fields) < 2 {
		return 0, false
	}
	ppid, err := strconv.Atoi(fields[1])
	if err != nil {
		return 0, false
	}
	return ppid, true
}
