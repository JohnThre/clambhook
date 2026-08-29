// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

#include "clambhook/procattr.h"

#include <ctype.h>
#include <dirent.h>
#include <errno.h>
#include <limits.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <strings.h>
#include <sys/types.h>
#include <unistd.h>

#include "internal.h"

#ifdef __APPLE__
#include <arpa/inet.h>
#include <CoreFoundation/CoreFoundation.h>
#include <libproc.h>
#include <Security/Security.h>
#include <sys/proc_info.h>
#endif

void ch_process_info_clear(ch_process_info *process) {
    if (process == NULL) return;
    free(process->name);
    free(process->path);
    free(process->code_sign_id);
    free(process->code_sign_status);
    memset(process, 0, sizeof(*process));
}

bool ch_procattr_local_port(const char *source, int *out_port) {
    if (source == NULL || out_port == NULL) return false;
    while (isspace((unsigned char)*source)) ++source;
    if (*source == '\0') return false;
    const char *separator = strrchr(source, ':');
    if (separator == NULL || separator[1] == '\0') return false;
    if (source[0] == '[') {
        const char *closing = strchr(source, ']');
        if (closing == NULL || closing + 1 != separator) return false;
    } else if (strchr(source, ':') != separator) {
        return false;
    }
    errno = 0;
    char *end = NULL;
    long port = strtol(separator + 1, &end, 10);
    while (end != NULL && isspace((unsigned char)*end)) ++end;
    if (errno != 0 || end == separator + 1 || end == NULL || *end != '\0' ||
        port <= 0L || port > 65535L) {
        return false;
    }
    *out_port = (int)port;
    return true;
}

static char *ch_procattr_basename(const char *path) {
    if (path == NULL || path[0] == '\0') return ch_strdup("");
    const char *separator = strrchr(path, '/');
    return ch_strdup(separator == NULL ? path : separator + 1);
}

#ifdef __APPLE__
static int ch_procattr_find_darwin(int port, int tcp, char *path,
                                  size_t path_length) {
    int pid_bytes = proc_listpids(PROC_ALL_PIDS, 0, NULL, 0);
    if (pid_bytes <= 0) return 0;
    size_t capacity = (size_t)pid_bytes / sizeof(pid_t);
    pid_t *pids = calloc(capacity, sizeof(*pids));
    if (pids == NULL) return 0;
    int received = proc_listpids(PROC_ALL_PIDS, 0, pids, pid_bytes);
    size_t count = received > 0 ? (size_t)received / sizeof(pid_t) : 0U;
    int found = 0;
    for (size_t index = 0U; index < count && found == 0; ++index) {
        pid_t pid = pids[index];
        if (pid <= 0) continue;
        int fd_bytes = proc_pidinfo(pid, PROC_PIDLISTFDS, 0, NULL, 0);
        if (fd_bytes <= 0) continue;
        struct proc_fdinfo *fds = malloc((size_t)fd_bytes);
        if (fds == NULL) continue;
        int fd_received = proc_pidinfo(pid, PROC_PIDLISTFDS, 0, fds,
                                       fd_bytes);
        size_t fd_count = fd_received > 0 ?
            (size_t)fd_received / sizeof(*fds) : 0U;
        for (size_t fd_index = 0U; fd_index < fd_count; ++fd_index) {
            if (fds[fd_index].proc_fdtype != PROX_FDTYPE_SOCKET) continue;
            struct socket_fdinfo socket_info;
            int detail = proc_pidfdinfo(
                pid, fds[fd_index].proc_fd, PROC_PIDFDSOCKETINFO,
                &socket_info, (int)sizeof(socket_info));
            if (detail < (int)sizeof(socket_info)) continue;
            int local_port = -1;
            if (socket_info.psi.soi_kind == SOCKINFO_TCP && tcp) {
                local_port = (int)ntohs((uint16_t)socket_info.psi.soi_proto
                    .pri_tcp.tcpsi_ini.insi_lport);
            } else if (socket_info.psi.soi_kind == SOCKINFO_IN && !tcp) {
                local_port = (int)ntohs((uint16_t)socket_info.psi.soi_proto
                    .pri_in.insi_lport);
            }
            if (local_port == port) {
                if (proc_pidpath(pid, path, (uint32_t)path_length) <= 0) {
                    path[0] = '\0';
                }
                found = (int)pid;
                break;
            }
        }
        free(fds);
    }
    free(pids);
    return found;
}

static char *ch_procattr_cf_string(CFStringRef value) {
    if (value == NULL) return NULL;
    CFIndex length = CFStringGetLength(value);
    CFIndex maximum = CFStringGetMaximumSizeForEncoding(
        length, kCFStringEncodingUTF8) + 1;
    if (maximum <= 1) return ch_strdup("");
    char *text = malloc((size_t)maximum);
    if (text == NULL) return NULL;
    if (!CFStringGetCString(value, text, maximum, kCFStringEncodingUTF8)) {
        free(text);
        return NULL;
    }
    return text;
}

static void ch_procattr_enrich_darwin(ch_process_info *process) {
    if (process->path == NULL || process->path[0] == '\0') return;
    CFURLRef url = CFURLCreateFromFileSystemRepresentation(
        kCFAllocatorDefault, (const UInt8 *)process->path,
        (CFIndex)strlen(process->path), false);
    SecStaticCodeRef code = NULL;
    OSStatus created = url == NULL ? errSecParam :
        SecStaticCodeCreateWithPath(url, kSecCSDefaultFlags, &code);
    if (url != NULL) CFRelease(url);
    if (created != errSecSuccess || code == NULL) {
        process->code_sign_status = ch_strdup("unsigned");
        return;
    }
    OSStatus valid = SecStaticCodeCheckValidity(code, kSecCSDefaultFlags,
                                                NULL);
    process->code_sign_status = ch_strdup(
        valid == errSecSuccess ? "valid" :
        valid == errSecCSUnsigned ? "unsigned" : "error");
    CFDictionaryRef information = NULL;
    if (SecCodeCopySigningInformation(
            code, kSecCSSigningInformation, &information) == errSecSuccess &&
        information != NULL) {
        CFArrayRef certificates = (CFArrayRef)CFDictionaryGetValue(
            information, kSecCodeInfoCertificates);
        if (certificates != NULL && CFArrayGetCount(certificates) > 0) {
            SecCertificateRef certificate = (SecCertificateRef)
                CFArrayGetValueAtIndex(certificates, 0);
            CFStringRef summary = SecCertificateCopySubjectSummary(certificate);
            process->code_sign_id = ch_procattr_cf_string(summary);
            if (summary != NULL) CFRelease(summary);
        }
        if (process->code_sign_id == NULL) {
            process->code_sign_id = ch_procattr_cf_string(
                (CFStringRef)CFDictionaryGetValue(
                    information, kSecCodeInfoIdentifier));
        }
        CFRelease(information);
    }
    CFRelease(code);
}
#endif

#ifdef __linux__
static int ch_procattr_inode_for_file(const char *path, int port,
                                      char *inode, size_t inode_length) {
    FILE *file = fopen(path, "r");
    if (file == NULL) return 0;
    char *line = NULL;
    size_t capacity = 0U;
    int first = 1;
    int found = 0;
    while (getline(&line, &capacity, file) >= 0) {
        if (first) {
            first = 0;
            continue;
        }
        char *fields[10];
        size_t count = 0U;
        char *save = NULL;
        for (char *token = strtok_r(line, " \t\r\n", &save);
             token != NULL && count < 10U;
             token = strtok_r(NULL, " \t\r\n", &save)) {
            fields[count++] = token;
        }
        if (count < 10U) continue;
        const char *separator = strrchr(fields[1], ':');
        if (separator == NULL) continue;
        errno = 0;
        char *end = NULL;
        unsigned long parsed = strtoul(separator + 1, &end, 16);
        if (errno != 0 || end == separator + 1 || *end != '\0' ||
            parsed != (unsigned long)port || strcmp(fields[9], "0") == 0) {
            continue;
        }
        (void)snprintf(inode, inode_length, "%s", fields[9]);
        found = 1;
        break;
    }
    free(line);
    (void)fclose(file);
    return found;
}

static int ch_procattr_find_linux(int port, int tcp, char *path,
                                  size_t path_length) {
    static const char *const tcp_files[] = {
        "/proc/net/tcp", "/proc/net/tcp6"
    };
    static const char *const udp_files[] = {
        "/proc/net/udp", "/proc/net/udp6"
    };
    const char *const *files = tcp ? tcp_files : udp_files;
    char inode[64] = {0};
    for (size_t index = 0U; index < 2U && inode[0] == '\0'; ++index) {
        (void)ch_procattr_inode_for_file(files[index], port, inode,
                                        sizeof(inode));
    }
    if (inode[0] == '\0') return 0;
    char socket_target[96];
    (void)snprintf(socket_target, sizeof(socket_target), "socket:[%s]", inode);
    DIR *proc = opendir("/proc");
    if (proc == NULL) return 0;
    int found = 0;
    struct dirent *entry;
    while ((entry = readdir(proc)) != NULL && found == 0) {
        char *end = NULL;
        long pid = strtol(entry->d_name, &end, 10);
        if (end == entry->d_name || *end != '\0' || pid <= 0L ||
            pid > INT_MAX) continue;
        char fd_path[128];
        (void)snprintf(fd_path, sizeof(fd_path), "/proc/%ld/fd", pid);
        DIR *fds = opendir(fd_path);
        if (fds == NULL) continue;
        struct dirent *fd_entry;
        while ((fd_entry = readdir(fds)) != NULL) {
            char link_path[PATH_MAX];
            char link_value[128];
            int link_length = snprintf(link_path, sizeof(link_path), "%s/%s",
                                       fd_path, fd_entry->d_name);
            if (link_length < 0 || (size_t)link_length >= sizeof(link_path)) {
                continue;
            }
            ssize_t length = readlink(link_path, link_value,
                                      sizeof(link_value) - 1U);
            if (length <= 0) continue;
            link_value[(size_t)length] = '\0';
            if (strcmp(link_value, socket_target) == 0) {
                char executable[64];
                (void)snprintf(executable, sizeof(executable),
                               "/proc/%ld/exe", pid);
                ssize_t path_bytes = readlink(executable, path,
                                              path_length - 1U);
                if (path_bytes > 0) path[(size_t)path_bytes] = '\0';
                else path[0] = '\0';
                found = (int)pid;
                break;
            }
        }
        (void)closedir(fds);
    }
    (void)closedir(proc);
    return found;
}
#endif

bool ch_procattr_lookup(const char *network, const char *source,
                        ch_process_info *out_process) {
    if (network == NULL || source == NULL || out_process == NULL) return false;
    memset(out_process, 0, sizeof(*out_process));
    int port = 0;
    if (!ch_procattr_local_port(source, &port)) return false;
    int tcp = strncasecmp(network, "udp", 3U) != 0;
    char path[4096] = {0};
    int pid = 0;
#ifdef __APPLE__
    pid = ch_procattr_find_darwin(port, tcp, path, sizeof(path));
#elif defined(__linux__)
    pid = ch_procattr_find_linux(port, tcp, path, sizeof(path));
#else
    (void)tcp;
#endif
    if (pid <= 0) return false;
    out_process->pid = pid;
    out_process->path = ch_strdup(path);
    out_process->name = ch_procattr_basename(path);
    if (out_process->path == NULL || out_process->name == NULL) {
        ch_process_info_clear(out_process);
        return false;
    }
#ifdef __APPLE__
    ch_procattr_enrich_darwin(out_process);
#endif
    return true;
}
