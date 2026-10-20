// SPIKE: sax vnet interpose shim (macOS, DYLD_INSERT_LIBRARIES).
//
// Gives each session a *virtual* IP inside a router subnet without root:
//   bind(any|loopback|self : P)  -> real bind 127.0.0.1:0, registered as self:P
//   connect(vip : P)             -> connect 127.0.0.1:<registered real port>
//   connect(svc-vip : P)         -> service map -> target vip:port -> real port
//   connect(localhost : P)       -> own vip:P if registered, else passthrough
//   getaddrinfo("bob.lan")       -> names table
//   getsockname(fd)              -> virtual address the app asked for
//
// Table (per router) is plain files under $SAX_VNET_DIR; the real
// implementation moves this into the sax daemon + a shared mmap.
//   names            "<name> <ipv4>"
//   svc              "<vip> <vport> <target-ip> <target-port>"
//   ports/<ip>_<p>   "<real-port> <pid>"
//
// Env: SAX_VNET_DIR, SAX_VNET_IP, SAX_VNET_SUBNET (a.b.c.d/n)

#include <arpa/inet.h>
#include <errno.h>
#include <fcntl.h>
#include <netdb.h>
#include <netinet/in.h>
#include <pthread.h>
#include <signal.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/socket.h>
#include <unistd.h>

#define DYLD_INTERPOSE(_repl, _orig)                                        \
    __attribute__((used)) static struct {                                  \
        const void *repl;                                                   \
        const void *orig;                                                   \
    } _interpose_##_orig __attribute__((section("__DATA,__interpose"))) = { \
        (const void *)(unsigned long)&_repl, (const void *)(unsigned long)&_orig};

#define MAX_FDS 8192
#define LOOPBACK htonl(INADDR_LOOPBACK)

typedef struct {
    pid_t owner;          // 0 = slot unused
    in_addr_t vip;        // network order
    in_port_t vport;      // network order
    int listening_entry;  // registered in ports/
} vfd_t;

static struct {
    int enabled;
    char dir[512];
    in_addr_t self;  // network order
    in_addr_t net;   // network order
    in_addr_t mask;  // network order
} cfg;

static vfd_t vfds[MAX_FDS];
static pthread_mutex_t vfd_lock = PTHREAD_MUTEX_INITIALIZER;
static pthread_once_t cfg_once = PTHREAD_ONCE_INIT;

static void load_cfg(void) {
    const char *dir = getenv("SAX_VNET_DIR");
    const char *ip = getenv("SAX_VNET_IP");
    const char *subnet = getenv("SAX_VNET_SUBNET");
    if (!dir || !ip || !subnet) return;

    char buf[64];
    snprintf(buf, sizeof buf, "%s", subnet);
    char *slash = strchr(buf, '/');
    if (!slash) return;
    *slash = '\0';
    int bits = atoi(slash + 1);
    if (bits < 1 || bits > 32) return;

    struct in_addr self, net;
    if (inet_pton(AF_INET, ip, &self) != 1 || inet_pton(AF_INET, buf, &net) != 1) return;

    snprintf(cfg.dir, sizeof cfg.dir, "%s", dir);
    cfg.self = self.s_addr;
    cfg.mask = htonl(bits == 32 ? 0xffffffffu : ~((1u << (32 - bits)) - 1));
    cfg.net = net.s_addr & cfg.mask;
    cfg.enabled = 1;
}

static int on(void) {
    pthread_once(&cfg_once, load_cfg);
    return cfg.enabled;
}

static int in_subnet(in_addr_t a) { return (a & cfg.mask) == cfg.net; }
static int is_loopback(in_addr_t a) { return (ntohl(a) >> 24) == 127; }

static int is_stream(int fd) {
    int type = 0;
    socklen_t len = sizeof type;
    return getsockopt(fd, SOL_SOCKET, SO_TYPE, &type, &len) == 0 && type == SOCK_STREAM;
}

// Extracts an IPv4 view of addr. Returns 1 for any-address, 0 for a concrete
// address, -1 if it is not representable (real IPv6), -2 for invalid input.
static int v4_of(const struct sockaddr *sa, socklen_t len, in_addr_t *ip, in_port_t *port) {
    if (sa->sa_family == AF_INET && len >= (socklen_t)sizeof(struct sockaddr_in)) {
        const struct sockaddr_in *s = (const void *)sa;
        *ip = s->sin_addr.s_addr;
        *port = s->sin_port;
        return *ip == INADDR_ANY ? 1 : 0;
    }
    if (sa->sa_family == AF_INET6 && len >= (socklen_t)sizeof(struct sockaddr_in6)) {
        const struct sockaddr_in6 *s = (const void *)sa;
        *port = s->sin6_port;
        if (IN6_IS_ADDR_UNSPECIFIED(&s->sin6_addr)) { *ip = INADDR_ANY; return 1; }
        if (IN6_IS_ADDR_LOOPBACK(&s->sin6_addr)) { *ip = LOOPBACK; return 0; }
        if (IN6_IS_ADDR_V4MAPPED(&s->sin6_addr)) {
            memcpy(ip, &s->sin6_addr.s6_addr[12], 4);
            return 0;
        }
        return -1;
    }
    return -2;
}

// Builds a real loopback sockaddr matching the socket's family.
static socklen_t real_addr(int family, in_port_t port, struct sockaddr_storage *out) {
    memset(out, 0, sizeof *out);
    if (family == AF_INET6) {
        struct sockaddr_in6 *s = (void *)out;
        s->sin6_len = sizeof *s;
        s->sin6_family = AF_INET6;
        s->sin6_port = port;
        s->sin6_addr.s6_addr[10] = 0xff;
        s->sin6_addr.s6_addr[11] = 0xff;
        in_addr_t lo = LOOPBACK;
        memcpy(&s->sin6_addr.s6_addr[12], &lo, 4);
        return sizeof *s;
    }
    struct sockaddr_in *s = (void *)out;
    s->sin_len = sizeof *s;
    s->sin_family = AF_INET;
    s->sin_port = port;
    s->sin_addr.s_addr = LOOPBACK;
    return sizeof *s;
}

static void port_path(in_addr_t ip, in_port_t port, char *out, size_t n) {
    char ipstr[INET_ADDRSTRLEN];
    inet_ntop(AF_INET, &ip, ipstr, sizeof ipstr);
    snprintf(out, n, "%s/ports/%s_%u", cfg.dir, ipstr, ntohs(port));
}

// Returns the real port (network order) registered for ip:port, or 0.
static in_port_t lookup_port(in_addr_t ip, in_port_t port) {
    char path[640];
    port_path(ip, port, path, sizeof path);
    FILE *f = fopen(path, "r");
    if (!f) return 0;
    unsigned real = 0;
    int pid = 0;
    int ok = fscanf(f, "%u %d", &real, &pid) == 2;
    fclose(f);
    if (!ok || real == 0 || real > 65535) return 0;
    if (kill(pid, 0) != 0 && errno == ESRCH) {  // owner died without cleanup
        unlink(path);
        return 0;
    }
    return htons((in_port_t)real);
}

static int register_port(in_addr_t ip, in_port_t port, in_port_t real) {
    char path[640], tmp[700];
    port_path(ip, port, path, sizeof path);
    snprintf(tmp, sizeof tmp, "%s.%d", path, getpid());
    FILE *f = fopen(tmp, "w");
    if (!f) return -1;
    fprintf(f, "%u %d\n", ntohs(real), getpid());
    fclose(f);
    return rename(tmp, path);
}

// Service map: vip:vport -> target ip:port. Returns 1 if rewritten.
static int lookup_svc(in_addr_t *ip, in_port_t *port) {
    char path[600];
    snprintf(path, sizeof path, "%s/svc", cfg.dir);
    FILE *f = fopen(path, "r");
    if (!f) return 0;
    char vip[64], tip[64];
    unsigned vport, tport;
    int hit = 0;
    while (!hit && fscanf(f, "%63s %u %63s %u", vip, &vport, tip, &tport) == 4) {
        struct in_addr v, t;
        if (inet_pton(AF_INET, vip, &v) != 1 || inet_pton(AF_INET, tip, &t) != 1) continue;
        if (v.s_addr == *ip && htons((in_port_t)vport) == *port) {
            *ip = t.s_addr;
            *port = htons((in_port_t)tport);
            hit = 1;
        }
    }
    fclose(f);
    return hit;
}

static int lookup_name(const char *name, char *ip_out, size_t n) {
    char want[256];
    snprintf(want, sizeof want, "%s", name);
    size_t l = strlen(want);
    if (l && want[l - 1] == '.') want[l - 1] = '\0';

    char path[600];
    snprintf(path, sizeof path, "%s/names", cfg.dir);
    FILE *f = fopen(path, "r");
    if (!f) return 0;
    char nm[256], ip[64];
    int hit = 0;
    while (!hit && fscanf(f, "%255s %63s", nm, ip) == 2) {
        if (strcasecmp(nm, want) == 0) {
            snprintf(ip_out, n, "%s", ip);
            hit = 1;
        }
    }
    fclose(f);
    return hit;
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

static int sax_bind(int fd, const struct sockaddr *addr, socklen_t len) {
    if (!on() || !addr || !is_stream(fd)) return bind(fd, addr, len);

    in_addr_t ip;
    in_port_t vport;
    int kind = v4_of(addr, len, &ip, &vport);
    if (kind < 0) return bind(fd, addr, len);
    if (kind == 0 && !is_loopback(ip) && ip != cfg.self) {
        if (in_subnet(ip)) { errno = EADDRNOTAVAIL; return -1; }
        return bind(fd, addr, len);  // a real host interface: leave it alone
    }

    if (vport != 0 && lookup_port(cfg.self, vport) != 0) {
        errno = EADDRINUSE;
        return -1;
    }

    if (addr->sa_family == AF_INET6) {
        int off = 0;
        setsockopt(fd, IPPROTO_IPV6, IPV6_V6ONLY, &off, sizeof off);
    }
    struct sockaddr_storage real;
    socklen_t rlen = real_addr(addr->sa_family, 0, &real);
    if (bind(fd, (struct sockaddr *)&real, rlen) != 0) return -1;

    struct sockaddr_storage got;
    socklen_t glen = sizeof got;
    if (getsockname(fd, (struct sockaddr *)&got, &glen) != 0) return -1;
    in_port_t real_port = got.ss_family == AF_INET6 ? ((struct sockaddr_in6 *)&got)->sin6_port
                                                    : ((struct sockaddr_in *)&got)->sin_port;
    if (vport == 0) vport = real_port;

    if (register_port(cfg.self, vport, real_port) != 0) return -1;
    vfd_set(fd, (vfd_t){.owner = getpid(), .vip = cfg.self, .vport = vport, .listening_entry = 1});
    return 0;
}
DYLD_INTERPOSE(sax_bind, bind)

static int sax_connect(int fd, const struct sockaddr *addr, socklen_t len) {
    if (!on() || !addr || !is_stream(fd)) return connect(fd, addr, len);

    in_addr_t ip;
    in_port_t port;
    if (v4_of(addr, len, &ip, &port) != 0) return connect(fd, addr, len);

    int svc = lookup_svc(&ip, &port);
    in_port_t real;
    if (is_loopback(ip)) {
        real = lookup_port(cfg.self, port);
        if (!real) return connect(fd, addr, len);  // a real host service
    } else if (in_subnet(ip)) {
        real = lookup_port(ip, port);
        if (!real) { errno = ECONNREFUSED; return -1; }
    } else if (svc) {
        errno = ENETUNREACH;  // service pointing outside the router
        return -1;
    } else {
        return connect(fd, addr, len);
    }

    struct sockaddr_storage target;
    socklen_t tlen = real_addr(addr->sa_family, real, &target);
    return connect(fd, (struct sockaddr *)&target, tlen);
}
DYLD_INTERPOSE(sax_connect, connect)

static int sax_getsockname(int fd, struct sockaddr *addr, socklen_t *len) {
    int rc = getsockname(fd, addr, len);
    if (rc != 0 || !on()) return rc;
    vfd_t v = vfd_get(fd);
    if (v.owner != getpid()) return rc;
    if (addr->sa_family == AF_INET && *len >= (socklen_t)sizeof(struct sockaddr_in)) {
        struct sockaddr_in *s = (void *)addr;
        s->sin_addr.s_addr = v.vip;
        s->sin_port = v.vport;
    } else if (addr->sa_family == AF_INET6 && *len >= (socklen_t)sizeof(struct sockaddr_in6)) {
        struct sockaddr_in6 *s = (void *)addr;
        memset(&s->sin6_addr, 0, sizeof s->sin6_addr);
        s->sin6_addr.s6_addr[10] = 0xff;
        s->sin6_addr.s6_addr[11] = 0xff;
        memcpy(&s->sin6_addr.s6_addr[12], &v.vip, 4);
        s->sin6_port = v.vport;
    }
    return rc;
}
DYLD_INTERPOSE(sax_getsockname, getsockname)

static int sax_close(int fd) {
    if (on()) {
        vfd_t v = vfd_take(fd);
        if (v.owner == getpid() && v.listening_entry) {
            char path[640];
            port_path(v.vip, v.vport, path, sizeof path);
            unlink(path);
        }
    }
    return close(fd);
}
DYLD_INTERPOSE(sax_close, close)

static int sax_getaddrinfo(const char *node, const char *service, const struct addrinfo *hints,
                           struct addrinfo **res) {
    char ip[64];
    if (!on() || !node || !lookup_name(node, ip, sizeof ip))
        return getaddrinfo(node, service, hints, res);

    struct addrinfo h = {0};
    if (hints) h = *hints;
    h.ai_flags = (h.ai_flags | AI_NUMERICHOST) & ~AI_ADDRCONFIG;
    if (h.ai_family == AF_INET6) h.ai_flags |= AI_V4MAPPED;
    return getaddrinfo(ip, service, &h, res);
}
DYLD_INTERPOSE(sax_getaddrinfo, getaddrinfo)
