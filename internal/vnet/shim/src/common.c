// Shared core of the vnet shim and supervisor; see common.h.

#include "common.h"

#include <arpa/inet.h>
#include <errno.h>
#include <signal.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <strings.h>
#include <unistd.h>

#define LOOPBACK htonl(INADDR_LOOPBACK)

VN_HIDDEN vn_cfg_t vn_cfg;

#ifdef __APPLE__
#define SET_LEN(s, field) ((s)->field = sizeof *(s))
#else
#define SET_LEN(s, field) ((void)0)
#endif

int vn_load_cfg(void) {
    const char *dir = getenv("SAX_VNET_DIR");
    const char *ip = getenv("SAX_VNET_IP");
    const char *subnet = getenv("SAX_VNET_SUBNET");
    if (!dir || !ip || !subnet) return 0;

    char buf[64];
    snprintf(buf, sizeof buf, "%s", subnet);
    char *slash = strchr(buf, '/');
    if (!slash) return 0;
    *slash = '\0';
    int bits = atoi(slash + 1);
    if (bits < 1 || bits > 32) return 0;

    struct in_addr self, net;
    if (inet_pton(AF_INET, ip, &self) != 1 || inet_pton(AF_INET, buf, &net) != 1) return 0;

    snprintf(vn_cfg.dir, sizeof vn_cfg.dir, "%s", dir);
    vn_cfg.self = self.s_addr;
    vn_cfg.mask = htonl(bits == 32 ? 0xffffffffu : ~((1u << (32 - bits)) - 1));
    vn_cfg.net = net.s_addr & vn_cfg.mask;
    vn_cfg.enabled = 1;
    return 1;
}

int vn_in_subnet(in_addr_t a) { return (a & vn_cfg.mask) == vn_cfg.net; }
int vn_is_loopback(in_addr_t a) { return (ntohl(a) >> 24) == 127; }

int vn_v4_of(const struct sockaddr *sa, socklen_t len, in_addr_t *ip, in_port_t *port) {
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

socklen_t vn_virtual_addr(int family, in_addr_t vip, in_port_t port, struct sockaddr_storage *out) {
    memset(out, 0, sizeof *out);
    if (family == AF_INET6) {
        struct sockaddr_in6 *s = (void *)out;
        SET_LEN(s, sin6_len);
        s->sin6_family = AF_INET6;
        s->sin6_port = port;
        s->sin6_addr.s6_addr[10] = 0xff;
        s->sin6_addr.s6_addr[11] = 0xff;
        memcpy(&s->sin6_addr.s6_addr[12], &vip, 4);
        return sizeof *s;
    }
    struct sockaddr_in *s = (void *)out;
    SET_LEN(s, sin_len);
    s->sin_family = AF_INET;
    s->sin_port = port;
    s->sin_addr.s_addr = vip;
    return sizeof *s;
}

socklen_t vn_real_addr(int family, in_port_t port, struct sockaddr_storage *out) {
    return vn_virtual_addr(family, LOOPBACK, port, out);
}

void vn_port_path(in_addr_t ip, in_port_t port, char *out, size_t n) {
    char ipstr[INET_ADDRSTRLEN];
    inet_ntop(AF_INET, &ip, ipstr, sizeof ipstr);
    snprintf(out, n, "%s/ports/%s_%u", vn_cfg.dir, ipstr, ntohs(port));
}

// Reads "<port> <pid>" from path; returns the port (host order) or 0.
static unsigned read_port_file(const char *path, int *pid_out) {
    FILE *f = fopen(path, "r");
    if (!f) return 0;
    unsigned port = 0;
    int pid = 0;
    int ok = fscanf(f, "%u %d", &port, &pid) == 2;
    fclose(f);
    if (!ok || port == 0 || port > 65535) return 0;
    if (kill(pid, 0) != 0 && errno == ESRCH) return 0;  // owner is gone
    if (pid_out) *pid_out = pid;
    return port;
}

in_port_t vn_lookup_port(in_addr_t ip, in_port_t port) {
    return vn_lookup_port_owner(ip, port, NULL);
}

in_port_t vn_lookup_port_owner(in_addr_t ip, in_port_t port, pid_t *owner) {
    char path[640];
    vn_port_path(ip, port, path, sizeof path);
    int pid = 0;
    unsigned real = read_port_file(path, &pid);
    if (owner) *owner = (pid_t)pid;
    if (!real) {
        unlink(path);  // absent or stale: either way it is gone now
        return 0;
    }
    return htons((in_port_t)real);
}

int vn_register_port(in_addr_t ip, in_port_t port, in_port_t real, pid_t owner) {
    char path[640], tmp[700];
    vn_port_path(ip, port, path, sizeof path);
    snprintf(tmp, sizeof tmp, "%s.%d", path, (int)getpid());
    FILE *f = fopen(tmp, "w");
    if (!f) return -1;
    fprintf(f, "%u %d\n", ntohs(real), (int)owner);
    fclose(f);
    return rename(tmp, path);
}

int vn_lookup_svc(in_addr_t *ip, in_port_t *port) {
    char path[600];
    snprintf(path, sizeof path, "%s/svc", vn_cfg.dir);
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

int vn_lookup_name(const char *name, char *ip_out, size_t n) {
    char want[256];
    snprintf(want, sizeof want, "%s", name);
    size_t l = strlen(want);
    if (l && want[l - 1] == '.') want[l - 1] = '\0';

    char path[600];
    snprintf(path, sizeof path, "%s/names", vn_cfg.dir);
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

in_port_t vn_dns_port(void) {
    char path[600];
    snprintf(path, sizeof path, "%s/dns", vn_cfg.dir);
    unsigned port = read_port_file(path, NULL);
    return port ? htons((in_port_t)port) : 0;
}

vn_action vn_plan_connect(const struct sockaddr *addr, socklen_t len, in_port_t *real, int *err) {
    in_addr_t ip;
    in_port_t port;
    if (vn_v4_of(addr, len, &ip, &port) != 0) return VN_PASS;

    int svc = vn_lookup_svc(&ip, &port);
    if (vn_is_loopback(ip)) {
        *real = vn_lookup_port(vn_cfg.self, port);
        return *real ? VN_REWRITE : VN_PASS;  // else: a real host service
    }
    if (vn_in_subnet(ip)) {
        *real = vn_lookup_port(ip, port);
        if (*real) return VN_REWRITE;
        *err = ECONNREFUSED;
        return VN_FAIL;
    }
    if (svc) {
        *err = ENETUNREACH;  // service pointing outside the router
        return VN_FAIL;
    }
    return VN_PASS;
}

vn_action vn_plan_bind(const struct sockaddr *addr, socklen_t len, in_port_t *vport, int *err,
                       int (*port_free)(in_port_t real), pid_t caller, int *register_it) {
    *register_it = 1;
    in_addr_t ip;
    int kind = vn_v4_of(addr, len, &ip, vport);
    if (kind < 0) return VN_PASS;
    if (kind == 0 && !vn_is_loopback(ip) && ip != vn_cfg.self) {
        if (vn_in_subnet(ip)) {
            *err = EADDRNOTAVAIL;
            return VN_FAIL;
        }
        return VN_PASS;  // a real host interface: leave it alone
    }
    if (*vport != 0) {
        pid_t owner = 0;
        in_port_t taken = vn_lookup_port_owner(vn_cfg.self, *vport, &owner);
        if (taken && !(port_free && port_free(taken))) {
            if (owner != caller) {
                *err = EADDRINUSE;
                return VN_FAIL;
            }
            *register_it = 0;  // the same app's second socket on this port
        }
    }
    return VN_REWRITE;
}
