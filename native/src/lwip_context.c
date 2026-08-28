#include "lwip_context.h"

#include <pthread.h>
#include <stdint.h>
#include <stdlib.h>

#include "lwip/init.h"

static pthread_once_t ch_lwip_once = PTHREAD_ONCE_INIT;
static pthread_mutex_t ch_lwip_mutex;

static void ch_lwip_initialize_once(void) {
    pthread_mutexattr_t attributes;
    if (pthread_mutexattr_init(&attributes) != 0) abort();
    if (pthread_mutexattr_settype(&attributes, PTHREAD_MUTEX_RECURSIVE) != 0) {
        (void)pthread_mutexattr_destroy(&attributes);
        abort();
    }
    if (pthread_mutex_init(&ch_lwip_mutex, &attributes) != 0) {
        (void)pthread_mutexattr_destroy(&attributes);
        abort();
    }
    (void)pthread_mutexattr_destroy(&attributes);
    lwip_init();
}

void ch_lwip_context_initialize(void) {
    (void)pthread_once(&ch_lwip_once, ch_lwip_initialize_once);
}

void ch_lwip_context_lock(void) {
    ch_lwip_context_initialize();
    (void)pthread_mutex_lock(&ch_lwip_mutex);
}

void ch_lwip_context_unlock(void) {
    (void)pthread_mutex_unlock(&ch_lwip_mutex);
}
