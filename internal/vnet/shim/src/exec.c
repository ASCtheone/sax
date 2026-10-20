// Exec laundering: keeps the shim alive across macOS SIP-protected binaries.
//
// dyld drops DYLD_* for restricted processes (/bin/sh, /usr/bin/env, ...), so
// anything they start runs without the shim. Two rewrites close the common
// gaps when a shimmed process execs:
//
//   script with "#!/usr/bin/env prog"  -> exec prog directly (skip env)
//   /bin/sh -c "script" (and bash/zsh/dash/ksh)
//                                       -> -c "export DYLD_INSERT_LIBRARIES=..;script"
//
// Every child also gets DYLD_INSERT_LIBRARIES and the SAX_VNET_* settings
// back if the caller passed a scrubbed environment.

#ifdef __APPLE__  // Linux has no SIP: LD_PRELOAD survives every exec.

#include <errno.h>
#include <fcntl.h>
#include <spawn.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>

#include "shim.h"

#define SHEBANG_MAX 256
#define PATH_MAX_LEN 1024

extern char **environ;

static const char *const restricted_dirs[] = {"/bin/", "/sbin/", "/usr/bin/", "/usr/sbin/",
                                              "/usr/libexec/", "/System/", NULL};
static const char *const posix_shells[] = {"sh", "bash", "zsh", "dash", "ksh", NULL};
static const char *const carried_env[] = {ENV_SHIM, "SAX_VNET_DIR", "SAX_VNET_IP",
                                          "SAX_VNET_SUBNET", NULL};

static __attribute__((constructor)) void mark_active(void) {
    // Lets the Node fallback tell "shim loaded in this very process" apart
    // from a value inherited from a shimmed parent.
    if (!getenv(ENV_SHIM)) return;
    char pid[24];
    snprintf(pid, sizeof pid, "%d", getpid());
    setenv(ENV_ACTIVE, pid, 1);
}

static int has_prefix(const char *s, const char *p) { return strncmp(s, p, strlen(p)) == 0; }

static int is_restricted(const char *path) {
    for (int i = 0; restricted_dirs[i]; i++)
        if (has_prefix(path, restricted_dirs[i])) return 1;
    return 0;
}

static int is_posix_shell(const char *path) {
    if (!is_restricted(path)) return 0;
    const char *base = strrchr(path, '/');
    base = base ? base + 1 : path;
    for (int i = 0; posix_shells[i]; i++)
        if (strcmp(base, posix_shells[i]) == 0) return 1;
    return 0;
}

static const char *env_get(char *const envp[], const char *key) {
    size_t n = strlen(key);
    for (int i = 0; envp && envp[i]; i++)
        if (strncmp(envp[i], key, n) == 0 && envp[i][n] == '=') return envp[i] + n + 1;
    return NULL;
}

// Owned allocations made while rewriting one exec, freed if it returns.
typedef struct {
    char **argv;
    char **envp;
    char *strings[16];
    int nstrings;
} rewrite_t;

static char *keep(rewrite_t *rw, char *s) {
    if (s && rw->nstrings < (int)(sizeof rw->strings / sizeof rw->strings[0]))
        rw->strings[rw->nstrings++] = s;
    return s;
}

static void rewrite_free(rewrite_t *rw) {
    for (int i = 0; i < rw->nstrings; i++) free(rw->strings[i]);
    free(rw->argv);
    free(rw->envp);
}

static int count(char *const v[]) {
    int n = 0;
    while (v && v[n]) n++;
    return n;
}

// Returns envp plus any missing carried settings and DYLD_INSERT_LIBRARIES.
static char *const *ensure_env(rewrite_t *rw, char *const envp[]) {
    const char *shim = getenv(ENV_SHIM);
    if (!shim) return envp;
    int n = count(envp);
    char **out = calloc(n + 8, sizeof *out);
    if (!out) return envp;
    memcpy(out, envp, n * sizeof *out);
    for (int i = 0; carried_env[i]; i++) {
        const char *v = getenv(carried_env[i]);
        if (v && !env_get(envp, carried_env[i])) {
            char *kv = NULL;
            if (asprintf(&kv, "%s=%s", carried_env[i], v) > 0) out[n++] = keep(rw, kv);
        }
    }
    if (!env_get(envp, ENV_DYLD)) {
        char *kv = NULL;
        if (asprintf(&kv, "%s=%s", ENV_DYLD, shim) > 0) out[n++] = keep(rw, kv);
    }
    rw->envp = out;
    return out;
}

// Finds name on PATH (from envp, else our own environment).
static int path_lookup(const char *name, char *const envp[], char *out, size_t n) {
    const char *path = env_get(envp, "PATH");
    if (!path) path = getenv("PATH");
    if (!path) return 0;
    const char *p = path;
    while (*p) {
        const char *end = strchr(p, ':');
        size_t len = end ? (size_t)(end - p) : strlen(p);
        if (len > 0 && snprintf(out, n, "%.*s/%s", (int)len, p, name) < (int)n &&
            access(out, X_OK) == 0)
            return 1;
        if (!end) break;
        p = end + 1;
    }
    return 0;
}

// "#!/usr/bin/env prog [arg]" -> run prog directly with the script.
static const char *launder_shebang(rewrite_t *rw, const char *path, char *const argv[],
                                   char *const envp[], char *const **argv_out) {
    int fd = open(path, O_RDONLY | O_CLOEXEC);
    if (fd < 0) return path;
    char buf[SHEBANG_MAX];
    ssize_t got = read(fd, buf, sizeof buf - 1);
    close(fd);
    if (got < 3 || buf[0] != '#' || buf[1] != '!') return path;
    buf[got] = '\0';
    char *nl = strchr(buf, '\n');
    if (nl) *nl = '\0';

    char *save = NULL;
    char *interp = strtok_r(buf + 2, " \t", &save);
    if (!interp || strcmp(interp, "/usr/bin/env") != 0) return path;
    char *prog = strtok_r(NULL, " \t", &save);
    if (!prog || prog[0] == '-' || strchr(prog, '=')) return path;  // env options: leave alone
    char *extra = strtok_r(NULL, " \t", &save);  // one shebang argument, like the kernel

    char resolved[PATH_MAX_LEN];
    if (!path_lookup(prog, envp, resolved, sizeof resolved) || is_restricted(resolved)) return path;

    int argc = count(argv);
    char **out = calloc(argc + 4, sizeof *out);
    if (!out) return path;
    int k = 0;
    out[k++] = keep(rw, strdup(prog));
    if (extra) out[k++] = keep(rw, strdup(extra));
    out[k++] = (char *)path;
    for (int i = 1; i < argc; i++) out[k++] = argv[i];
    rw->argv = out;
    *argv_out = out;
    return keep(rw, strdup(resolved));
}

// sh -c "script" -> sh -c "export DYLD_INSERT_LIBRARIES='..'\nscript".
static void prefix_shell_script(rewrite_t *rw, char *const argv[], char *const **argv_out) {
    const char *shim = getenv(ENV_SHIM);
    if (!shim || strchr(shim, '\'')) return;
    int argc = count(argv);
    for (int i = 1; i + 1 < argc; i++) {
        const char *a = argv[i];
        if (a[0] != '-') return;  // reached operands: no -c script
        if (a[1] == '-' || strchr(a, 'c') == NULL) continue;
        char *script = NULL;
        if (asprintf(&script, "export %s='%s'\n%s", ENV_DYLD, shim, argv[i + 1]) < 0) return;
        char **out = calloc(argc + 1, sizeof *out);
        if (!out) {
            free(script);
            return;
        }
        memcpy(out, argv, argc * sizeof *out);
        out[i + 1] = keep(rw, script);
        rw->argv = out;
        *argv_out = out;
        return;
    }
}

// Applies both rewrites; returns the path to execute.
static const char *rewrite(rewrite_t *rw, const char *path, char *const argv[], char *const envp[],
                           char *const **argv_out, char *const **envp_out) {
    *argv_out = argv;
    *envp_out = ensure_env(rw, envp);
    if (!getenv(ENV_SHIM) || !path) return path;
    if (is_posix_shell(path)) {
        prefix_shell_script(rw, argv, argv_out);
        return path;
    }
    return launder_shebang(rw, path, argv, *envp_out, argv_out);
}

static int sax_execve(const char *path, char *const argv[], char *const envp[]) {
    rewrite_t rw = {0};
    char *const *a, *const *e;
    const char *p = rewrite(&rw, path, argv, envp, &a, &e);
    int rc = execve(p, a, e);
    int saved = errno;
    rewrite_free(&rw);
    errno = saved;
    return rc;
}
DYLD_INTERPOSE(sax_execve, execve)

static int sax_posix_spawn(pid_t *pid, const char *path, const posix_spawn_file_actions_t *fa,
                           const posix_spawnattr_t *attr, char *const argv[], char *const envp[]) {
    rewrite_t rw = {0};
    char *const *a, *const *e;
    const char *p = rewrite(&rw, path, argv, envp ? envp : environ, &a, &e);
    int rc = posix_spawn(pid, p, fa, attr, a, e);
    rewrite_free(&rw);
    return rc;
}
DYLD_INTERPOSE(sax_posix_spawn, posix_spawn)

static int sax_posix_spawnp(pid_t *pid, const char *file, const posix_spawn_file_actions_t *fa,
                            const posix_spawnattr_t *attr, char *const argv[], char *const envp[]) {
    char resolved[PATH_MAX_LEN];
    char *const *env = envp ? envp : environ;
    if (!getenv(ENV_SHIM) || strchr(file, '/') ||
        !path_lookup(file, env, resolved, sizeof resolved))
        return posix_spawnp(pid, file, fa, attr, argv, envp);
    return sax_posix_spawn(pid, resolved, fa, attr, argv, envp);
}
DYLD_INTERPOSE(sax_posix_spawnp, posix_spawnp)

#endif  // __APPLE__
