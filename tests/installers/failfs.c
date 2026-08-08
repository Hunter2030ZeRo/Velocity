#define _GNU_SOURCE

#include <dlfcn.h>
#include <errno.h>
#include <fcntl.h>
#include <stdarg.h>
#include <stdlib.h>
#include <string.h>
#include <sys/types.h>
#include <unistd.h>

static int matches_env(const char *path, const char *name)
{
    const char *expected = getenv(name);
    return path != NULL && expected != NULL && strcmp(path, expected) == 0;
}

static int reject_rename(const char *destination)
{
    if (!matches_env(destination, "VELOCITY_FAIL_RENAME_DEST")) {
        return 0;
    }
    errno = EIO;
    return 1;
}

static int reject_write_open(const char *path, int flags)
{
    if ((flags & (O_WRONLY | O_RDWR)) == 0 ||
        !matches_env(path, "VELOCITY_FAIL_OPEN_DEST")) {
        return 0;
    }
    errno = EIO;
    return 1;
}

int rename(const char *old_path, const char *new_path)
{
    static int (*next_rename)(const char *, const char *);
    if (reject_rename(new_path)) {
        return -1;
    }
    if (next_rename == NULL) {
        next_rename = dlsym(RTLD_NEXT, "rename");
    }
    return next_rename(old_path, new_path);
}

int renameat(int old_dir, const char *old_path, int new_dir, const char *new_path)
{
    static int (*next_renameat)(int, const char *, int, const char *);
    if (reject_rename(new_path)) {
        return -1;
    }
    if (next_renameat == NULL) {
        next_renameat = dlsym(RTLD_NEXT, "renameat");
    }
    return next_renameat(old_dir, old_path, new_dir, new_path);
}

int renameat2(int old_dir, const char *old_path, int new_dir, const char *new_path,
              unsigned int flags)
{
    static int (*next_renameat2)(int, const char *, int, const char *, unsigned int);
    if (reject_rename(new_path)) {
        return -1;
    }
    if (next_renameat2 == NULL) {
        next_renameat2 = dlsym(RTLD_NEXT, "renameat2");
    }
    return next_renameat2(old_dir, old_path, new_dir, new_path, flags);
}

static mode_t open_mode(int flags, va_list arguments)
{
    return (flags & (O_CREAT | O_TMPFILE)) != 0 ? va_arg(arguments, mode_t) : 0;
}

int open(const char *path, int flags, ...)
{
    static int (*next_open)(const char *, int, ...);
    va_list arguments;
    mode_t mode;
    va_start(arguments, flags);
    mode = open_mode(flags, arguments);
    va_end(arguments);
    if (reject_write_open(path, flags)) {
        return -1;
    }
    if (next_open == NULL) {
        next_open = dlsym(RTLD_NEXT, "open");
    }
    return (flags & (O_CREAT | O_TMPFILE)) != 0
        ? next_open(path, flags, mode) : next_open(path, flags);
}

int open64(const char *path, int flags, ...)
{
    static int (*next_open64)(const char *, int, ...);
    va_list arguments;
    mode_t mode;
    va_start(arguments, flags);
    mode = open_mode(flags, arguments);
    va_end(arguments);
    if (reject_write_open(path, flags)) {
        return -1;
    }
    if (next_open64 == NULL) {
        next_open64 = dlsym(RTLD_NEXT, "open64");
    }
    return (flags & (O_CREAT | O_TMPFILE)) != 0
        ? next_open64(path, flags, mode) : next_open64(path, flags);
}

int openat(int directory, const char *path, int flags, ...)
{
    static int (*next_openat)(int, const char *, int, ...);
    va_list arguments;
    mode_t mode;
    va_start(arguments, flags);
    mode = open_mode(flags, arguments);
    va_end(arguments);
    if (reject_write_open(path, flags)) {
        return -1;
    }
    if (next_openat == NULL) {
        next_openat = dlsym(RTLD_NEXT, "openat");
    }
    return (flags & (O_CREAT | O_TMPFILE)) != 0
        ? next_openat(directory, path, flags, mode)
        : next_openat(directory, path, flags);
}

int openat64(int directory, const char *path, int flags, ...)
{
    static int (*next_openat64)(int, const char *, int, ...);
    va_list arguments;
    mode_t mode;
    va_start(arguments, flags);
    mode = open_mode(flags, arguments);
    va_end(arguments);
    if (reject_write_open(path, flags)) {
        return -1;
    }
    if (next_openat64 == NULL) {
        next_openat64 = dlsym(RTLD_NEXT, "openat64");
    }
    return (flags & (O_CREAT | O_TMPFILE)) != 0
        ? next_openat64(directory, path, flags, mode)
        : next_openat64(directory, path, flags);
}
