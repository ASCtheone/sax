// sax vnet interpose shim, network half (macOS DYLD_INSERT_LIBRARIES,
// Linux LD_PRELOAD fallback when the seccomp supervisor is unavailable).
//
// Gives each session a *virtual* IP inside a router subnet without root:
//   bind(any|loopback|self : P)  -> real bind 127.0.0.1:0, registered as self:P
//   connect(vip : P)             -> connect 127.0.0.1:<registered real port>
//   connect(svc-vip : P)         -> service map -> target vip:port -> real port
//   connect(localhost : P)       -> own vip:P if registered, else passthrough
//   getaddrinfo("bob.lan")       -> names table
//   getsockname(fd)              -> virtual address the app asked for
//
// Tables live under $SAX_VNET_DIR and are written by sax (internal/vnet):
//   names            "<name> <ipv4>"
//   svc              "<vip> <vport> <target-ip> <target-port>"
//   ports/<ip>_<p>   "<real-port> <pid>"   (written here on bind)
//
// The decisions live in common.c, shared with the supervisor (sup.c).

#include <errno.h>
#include <netdb.h>
#include <pthread.h>
#include <stdio.h>
#include <string.h>
#include <sys/socket.h>
#include <unistd.h>

#include "common.h"
#include "shim.h"

#define MAX_FDS 8192

typedef struct {
    pid_t owner;          // 0 = slot unused
    in_addr_t vip;        // network order
    in_port_t vport;      // network order
    in_port_t real;       // network order; detects fd reuse after an unseen close
    int listening_entry;  // registered in ports/
} vfd_t;

static vfd_t vfds[MAX_FDS];
static pthread_mutex_t vfd_lock = PTHREAD_MUTEX_INITIALIZER;
static pthread_once_t cfg_once = PTHREAD_ONCE_INIT;

static void load_cfg_once(void) { vn_load_cfg(); }

static int on(void) {
    pthread_once(&cfg_once, load_cfg_once);
    return vn_cfg.enabled;
}

static int is_stream(int fd) {
    int type = 0;
    socklen_t len = sizeof type;
    return getsockopt(fd, SOL_SOCKET, SO_TYPE, &type, &len) == 0 && type == SOCK_STREAM;
}

static void vfd_set(int fd, vfd_t v) {
    if (fd < 0 || fd >= MAX_FDS) return;
    pthread_mutex_lock(&vfd_lock);
    vfds[fd] = v;
    pthread_mutex_unlock(&vfd_lock);
}

static vfd_t vfd_take(int fd) {
    vfd_t v = {0};
    if (fd < 0 || fd >= MAX_FDS) return v;
    pthread_mutex_lock(&vfd_lock);
    v = vfds[fd];
    if (v.owner == getpid()) memset(&vfds[fd], 0, sizeof vfds[fd]);
    pthread_mutex_unlock(&vfd_lock);
    return v;
}

static vfd_t vfd_get(int fd) {
    vfd_t v = {0};
    if (fd < 0 || fd >= MAX_FDS) return v;
    pthread_mutex_lock(&vfd_lock);
    v = vfds[fd];
    pthread_mutex_unlock(&vfd_lock);
    return v;
}

// ---- hooks ---------------------------------------------------------------

DECLARE_REAL(bind)
DECLARE_REAL(connect)
DECLARE_REAL(getsockname)
DECLARE_REAL(close)
DECLARE_REAL(getaddrinfo)

static in_port_t port_of(const struct sockaddr_storage *sa) {
    return sa->ss_family == AF_INET6 ? ((const struct sockaddr_in6 *)sa)->sin6_port
                                     : ((const struct sockaddr_in *)sa)->sin_port;
}

// port_free reports whether 127.0.0.1:real (network order) can be bound,
// i.e. a registry entry for it is stale.
static int port_free(in_port_t real) {
    int s = socket(AF_INET, SOCK_STREAM, 0);
    if (s < 0) return 0;
    int one = 1;
    setsockopt(s, SOL_SOCKET, SO_REUSEADDR, &one, sizeof one);
    struct sockaddr_storage a;
    socklen_t len = vn_real_addr(AF_INET, real, &a);
    int ok = REAL(bind)(s, (struct sockaddr *)&a, len) == 0;
    REAL(close)(s);
    return ok;
}

HOOK_LINKAGE int HOOK(bind)(int fd, const struct sockaddr *addr, socklen_t len) {
    if (!on() || !addr || !is_stream(fd)) return REAL(bind)(fd, addr, len);

    in_port_t vport;
    int err = 0, register_it = 1;
    switch (vn_plan_bind(addr, len, &vport, &err, port_free, getpid(), &register_it)) {
    case VN_PASS: return REAL(bind)(fd, addr, len);
    case VN_FAIL: errno = err; return -1;
    case VN_REWRITE: break;
    }

    if (addr->sa_family == AF_INET6) {
        int off = 0;
        setsockopt(fd, IPPROTO_IPV6, IPV6_V6ONLY, &off, sizeof off);
    }
    struct sockaddr_storage real;
    socklen_t rlen = vn_real_addr(addr->sa_family, 0, &real);
    if (REAL(bind)(fd, (struct sockaddr *)&real, rlen) != 0) return -1;

    struct sockaddr_storage got;
    socklen_t glen = sizeof got;
    if (REAL(getsockname)(fd, (struct sockaddr *)&got, &glen) != 0) return -1;
    in_port_t real_port = port_of(&got);
    if (vport == 0) vport = real_port;

    if (register_it && vn_register_port(vn_cfg.self, vport, real_port, getpid()) != 0) return -1;
    vfd_set(fd, (vfd_t){.owner = getpid(), .vip = vn_cfg.self, .vport = vport, .real = real_port,
                        .listening_entry = register_it});
    return 0;
}
EXPORT_HOOK(bind)

HOOK_LINKAGE int HOOK(connect)(int fd, const struct sockaddr *addr, socklen_t len) {
    if (!on() || !addr || !is_stream(fd)) return REAL(connect)(fd, addr, len);

    in_port_t real;
    int err = 0;
    switch (vn_plan_connect(addr, len, &real, &err)) {
    case VN_PASS: return REAL(connect)(fd, addr, len);
    case VN_FAIL: errno = err; return -1;
    case VN_REWRITE: break;
    }
    struct sockaddr_storage target;
    socklen_t tlen = vn_real_addr(addr->sa_family, real, &target);
    return REAL(connect)(fd, (struct sockaddr *)&target, tlen);
}
EXPORT_HOOK(connect)

HOOK_LINKAGE int HOOK(getsockname)(int fd, struct sockaddr *addr, socklen_t *len) {
    int rc = REAL(getsockname)(fd, addr, len);
    if (rc != 0 || !on()) return rc;
    vfd_t v = vfd_get(fd);
    if (v.owner != getpid()) return rc;
    struct sockaddr_storage cur;
    memset(&cur, 0, sizeof cur);
    memcpy(&cur, addr, *len < sizeof cur ? *len : sizeof cur);
    if (port_of(&cur) != v.real) return rc;  // fd now names another socket

    struct sockaddr_storage virt;
    socklen_t vlen = vn_virtual_addr(addr->sa_family, v.vip, v.vport, &virt);
    if (*len >= vlen) memcpy(addr, &virt, vlen);
    return rc;
}
EXPORT_HOOK(getsockname)

HOOK_LINKAGE int HOOK(close)(int fd) {
    if (on()) {
        vfd_t v = vfd_take(fd);
        if (v.owner == getpid() && v.listening_entry) {
            char path[640];
            vn_port_path(v.vip, v.vport, path, sizeof path);
            unlink(path);
        }
    }
    return REAL(close)(fd);
}
EXPORT_HOOK(close)

HOOK_LINKAGE int HOOK(getaddrinfo)(const char *node, const char *service, const struct addrinfo *hints,
                                   struct addrinfo **res) {
    char ip[64];
    if (!on() || !node || !vn_lookup_name(node, ip, sizeof ip))
        return REAL(getaddrinfo)(node, service, hints, res);

    struct addrinfo h = {0};
    if (hints) h = *hints;
    h.ai_flags = (h.ai_flags | AI_NUMERICHOST) & ~AI_ADDRCONFIG;
    if (h.ai_family == AF_INET6) h.ai_flags |= AI_V4MAPPED;
    return REAL(getaddrinfo)(ip, service, &h, res);
}
EXPORT_HOOK(getaddrinfo)
