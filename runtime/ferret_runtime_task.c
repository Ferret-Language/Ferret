#include "ferret_runtime_internal.h"

#if defined(_WIN32)
ferret_raw ferret_task_run_raw(FerretTaskEntryRaw entry, ferret_raw arg) {
    (void)entry;
    (void)arg;
    return NULL;
}

void ferret_task_wait(ferret_raw raw_handle) {
    (void)raw_handle;
}
#else
#include <pthread.h>
#include <stdlib.h>

typedef struct {
    pthread_t thread;
} FerretTaskHandle;

typedef struct {
    FerretTaskEntryRaw entry;
    ferret_raw arg;
} FerretTaskStart;

static void *ferret_task_trampoline(void *raw) {
    FerretTaskStart *start = (FerretTaskStart *)raw;
    FerretTaskEntryRaw entry;
    ferret_raw arg;

    if (start == NULL) {
        return NULL;
    }
    entry = start->entry;
    arg = start->arg;
    free(start);
    if (entry != NULL) {
        entry(arg);
    }
    return NULL;
}

ferret_raw ferret_task_run_raw(FerretTaskEntryRaw entry, ferret_raw arg) {
    FerretTaskHandle *handle;
    FerretTaskStart *start;

    if (entry == NULL) {
        return NULL;
    }
    handle = (FerretTaskHandle *)malloc(sizeof(FerretTaskHandle));
    start = (FerretTaskStart *)malloc(sizeof(FerretTaskStart));
    if (handle == NULL || start == NULL) {
        free(handle);
        free(start);
        return NULL;
    }
    start->entry = entry;
    start->arg = arg;
    if (pthread_create(&handle->thread, NULL, ferret_task_trampoline, start) != 0) {
        free(start);
        free(handle);
        return NULL;
    }
    return (ferret_raw)handle;
}

void ferret_task_wait(ferret_raw raw_handle) {
    FerretTaskHandle *handle = (FerretTaskHandle *)raw_handle;
    if (handle == NULL) {
        return;
    }
    (void)pthread_join(handle->thread, NULL);
    free(handle);
}
#endif
