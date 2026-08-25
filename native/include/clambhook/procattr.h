#ifndef CLAMBHOOK_PROCATTR_H
#define CLAMBHOOK_PROCATTR_H

#include <stdbool.h>

#ifdef __cplusplus
extern "C" {
#endif

typedef struct ch_process_info {
    int pid;
    char *name;
    char *path;
    char *code_sign_id;
    char *code_sign_status;
} ch_process_info;

/* Best-effort local socket ownership lookup. */
bool ch_procattr_lookup(const char *network, const char *source,
                        ch_process_info *out_process);
void ch_process_info_clear(ch_process_info *process);

/* Exposed to freeze source-address parsing in platform-neutral tests. */
bool ch_procattr_local_port(const char *source, int *out_port);

#ifdef __cplusplus
}
#endif

#endif
