//go:build darwin

package procattr

/*
#include <libproc.h>
#include <sys/proc_info.h>
#include <arpa/inet.h>
#include <stdlib.h>
#include <string.h>

// chk_find_pid scans every process's socket file descriptors for one whose
// local port equals want_port and whose protocol matches want_tcp (1 = TCP,
// 0 = UDP). On the first match it writes the executable path into path
// (buffer size path_len) and returns the owning pid. Returns 0 when no socket
// matched and -1 on a hard enumeration error.
//
// The union access inside struct socket_fdinfo lives here in C so cgo never
// has to reinterpret the union bytes.
static int chk_find_pid(int want_port, int want_tcp, char *path, int path_len) {
	int npids_bytes = proc_listpids(PROC_ALL_PIDS, 0, NULL, 0);
	if (npids_bytes <= 0) {
		return -1;
	}
	int cap = npids_bytes / (int)sizeof(pid_t);
	if (cap <= 0) {
		return -1;
	}
	pid_t *pids = (pid_t *)calloc((size_t)cap, sizeof(pid_t));
	if (pids == NULL) {
		return -1;
	}
	int got = proc_listpids(PROC_ALL_PIDS, 0, pids, npids_bytes);
	if (got <= 0) {
		free(pids);
		return -1;
	}
	int count = got / (int)sizeof(pid_t);
	int result = 0;
	for (int i = 0; i < count && result == 0; i++) {
		pid_t pid = pids[i];
		if (pid <= 0) {
			continue;
		}
		int fdbytes = proc_pidinfo(pid, PROC_PIDLISTFDS, 0, NULL, 0);
		if (fdbytes <= 0) {
			continue;
		}
		struct proc_fdinfo *fds = (struct proc_fdinfo *)malloc((size_t)fdbytes);
		if (fds == NULL) {
			continue;
		}
		int fdgot = proc_pidinfo(pid, PROC_PIDLISTFDS, 0, fds, fdbytes);
		if (fdgot <= 0) {
			free(fds);
			continue;
		}
		int nfds = fdgot / (int)sizeof(struct proc_fdinfo);
		for (int j = 0; j < nfds; j++) {
			if (fds[j].proc_fdtype != PROX_FDTYPE_SOCKET) {
				continue;
			}
			struct socket_fdinfo si;
			int r = proc_pidfdinfo(pid, fds[j].proc_fd, PROC_PIDFDSOCKETINFO, &si, sizeof(si));
			if (r < (int)sizeof(si)) {
				continue;
			}
			int lport = -1;
			if (si.psi.soi_kind == SOCKINFO_TCP && want_tcp) {
				lport = (int)ntohs((uint16_t)si.psi.soi_proto.pri_tcp.tcpsi_ini.insi_lport);
			} else if (si.psi.soi_kind == SOCKINFO_IN && !want_tcp) {
				lport = (int)ntohs((uint16_t)si.psi.soi_proto.pri_in.insi_lport);
			} else {
				continue;
			}
			if (lport == want_port) {
				if (path != NULL && path_len > 0) {
					int n = proc_pidpath(pid, path, (uint32_t)path_len);
					if (n <= 0) {
						path[0] = '\0';
					}
				}
				result = (int)pid;
				break;
			}
		}
		free(fds);
	}
	free(pids);
	return result;
}
*/
import "C"

import "unsafe"

// proc_pidpath writes at most PROC_PIDPATHINFO_MAXSIZE (4*MAXPATHLEN) bytes.
const darwinPathBufSize = 4096

func lookup(network, source string) (Process, bool) {
	port, ok := localPort(source)
	if !ok {
		return Process{}, false
	}
	wantTCP := C.int(1)
	if isUDP(network) {
		wantTCP = 0
	}
	buf := make([]byte, darwinPathBufSize)
	pid := C.chk_find_pid(C.int(port), wantTCP, (*C.char)(unsafe.Pointer(&buf[0])), C.int(len(buf)))
	if pid <= 0 {
		return Process{}, false
	}
	path := C.GoString((*C.char)(unsafe.Pointer(&buf[0])))
	return Process{PID: int(pid), Path: path, Name: baseName(path)}, true
}
