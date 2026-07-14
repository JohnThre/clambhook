//go:build linux

package procattr

import (
	"bufio"
	"io"
	"os"
	"strconv"
	"strings"
)

func lookup(network, source string) (Process, bool) {
	port, ok := localPort(source)
	if !ok {
		return Process{}, false
	}
	files := []string{"/proc/net/tcp", "/proc/net/tcp6"}
	if isUDP(network) {
		files = []string{"/proc/net/udp", "/proc/net/udp6"}
	}
	inode, ok := inodeForPort(files, port)
	if !ok {
		return Process{}, false
	}
	pid, path, ok := pidForInode(inode)
	if !ok {
		return Process{}, false
	}
	return Process{PID: pid, Path: path, Name: baseName(path)}, true
}

// inodeForPort scans the given /proc/net/{tcp,udp}[6] files for a socket whose
// local port equals port and returns its inode.
func inodeForPort(files []string, port int) (string, bool) {
	for _, file := range files {
		f, err := os.Open(file)
		if err != nil {
			continue
		}
		inode, ok := parseProcNet(f, port)
		_ = f.Close()
		if ok {
			return inode, true
		}
	}
	return "", false
}

// parseProcNet parses a /proc/net/tcp-style table and returns the inode of the
// first row whose local address port matches port. The local address column
// (index 1) has the form "0100007F:1F90" where the port is hex after the colon.
func parseProcNet(r io.Reader, port int) (string, bool) {
	scanner := bufio.NewScanner(r)
	// Skip the header row.
	if !scanner.Scan() {
		return "", false
	}
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 10 {
			continue
		}
		local := fields[1]
		colon := strings.LastIndexByte(local, ':')
		if colon < 0 {
			continue
		}
		p, err := strconv.ParseUint(local[colon+1:], 16, 32)
		if err != nil || int(p) != port {
			continue
		}
		inode := fields[9]
		if inode == "" || inode == "0" {
			continue
		}
		return inode, true
	}
	return "", false
}

// pidForInode walks /proc/<pid>/fd looking for a symlink to socket:[inode] and
// returns the owning pid plus its executable path.
func pidForInode(inode string) (int, string, bool) {
	target := "socket:[" + inode + "]"
	procDir, err := os.Open("/proc")
	if err != nil {
		return 0, "", false
	}
	defer procDir.Close()
	names, err := procDir.Readdirnames(0)
	if err != nil {
		return 0, "", false
	}
	for _, name := range names {
		pid, err := strconv.Atoi(name)
		if err != nil || pid <= 0 {
			continue
		}
		fdDir := "/proc/" + name + "/fd"
		entries, err := os.ReadDir(fdDir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			link, err := os.Readlink(fdDir + "/" + entry.Name())
			if err != nil {
				continue
			}
			if link == target {
				path, _ := os.Readlink("/proc/" + name + "/exe")
				return pid, path, true
			}
		}
	}
	return 0, "", false
}
