#include "cnet.h"
#include <stdlib.h>

cnet_buf_t *cnet_buf_alloc(size_t cap) {
    cnet_buf_t *buf = malloc(sizeof(cnet_buf_t));
    if (!buf) return NULL;
    buf->data = malloc(cap);
    if (!buf->data) {
        free(buf);
        return NULL;
    }
    buf->len = 0;
    buf->cap = cap;
    return buf;
}

void cnet_buf_free(cnet_buf_t *buf) {
    if (buf) {
        free(buf->data);
        free(buf);
    }
}
