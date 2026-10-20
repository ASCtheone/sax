// Shared core of the vnet shim (LD_PRELOAD / DYLD) and the Linux seccomp
// supervisor: router config, the listener registry and the rewrite
// decisions for bind() and connect(). Nothing here calls a hooked function,
// so it is safe to use from inside the interpose library.
#ifndef SAX_VNET_COMMON_H
#define SAX_VNET_COMMON_H

#include <netinet/in.h>
#include <sys/socket.h>
#include <sys/types.h>

#define VN_HIDDEN __attribute__((visibility("hidden")))

typedef struct {
    int enabled;
    char dir[512];
    in_addr_t self;  // network order
    in_addr_t net;   // network order
    in_addr_t mask;  // network order
} vn_cfg_t;

extern VN_HIDDEN vn_cfg_t vn_cfg;

// Loads SAX_VNET_DIR/IP/SUBNET from the environment into vn_cfg. Returns
// vn_cfg.enabled. Not thread-safe: callers serialize the first call.
VN_HIDDEN int vn_load_cfg(void);

VN_HIDDEN int vn_in_subnet(in_addr_t a);
VN_HIDDEN int vn_is_loopback(in_addr_t a);

// IPv4 view of a sockaddr: 1 for any-address, 0 for a concrete address,
// -1 if not representable (real IPv6), -2 for invalid input.
VN_HIDDEN int vn_v4_of(const struct sockaddr *sa, socklen_t len, in_addr_t *ip, in_port_t *port);

// Real loopback sockaddr (127.0.0.1 or ::ffff:127.0.0.1) for a family.
VN_HIDDEN socklen_t vn_real_addr(int family, in_port_t port, struct sockaddr_storage *out);

// Virtual sockaddr (vip:port) for a family, as getsockname should report it.
VN_HIDDEN socklen_t vn_virtual_addr(int family, in_addr_t vip, in_port_t port, struct sockaddr_storage *out);

// Listener registry: ports/<ip>_<port> = "<real-port> <pid>". Ports are in
// network order. lookup returns 0 when absent or the owner is gone.
VN_HIDDEN void vn_port_path(in_addr_t ip, in_port_t port, char *out, size_t n);
VN_HIDDEN in_port_t vn_lookup_port(in_addr_t ip, in_port_t port);
// Like vn_lookup_port, also returning the registering process.
VN_HIDDEN in_port_t vn_lookup_port_owner(in_addr_t ip, in_port_t port, pid_t *owner);
VN_HIDDEN int vn_register_port(in_addr_t ip, in_port_t port, in_port_t real, pid_t owner);

// Service map (svc file): rewrites vip:port to the target; 1 if rewritten.
VN_HIDDEN int vn_lookup_svc(in_addr_t *ip, in_port_t *port);

// Names file: "<name> <ip>". 1 if found.
VN_HIDDEN int vn_lookup_name(const char *name, char *ip_out, size_t n);

// The gateway's DNS stub for this router (dns file), network order; 0 if none.
VN_HIDDEN in_port_t vn_dns_port(void);

typedef enum { VN_PASS, VN_REWRITE, VN_FAIL } vn_action;

// Decides what to do with connect(addr) on a stream socket. VN_REWRITE sets
// *real to the loopback port to connect to; VN_FAIL sets *err.
VN_HIDDEN vn_action vn_plan_connect(const struct sockaddr *addr, socklen_t len, in_port_t *real, int *err);

// Decides what to do with bind(addr) on a stream socket by process caller.
// VN_REWRITE means "bind 127.0.0.1:0" and, when *register_it is set,
// "register self:*vport" (*vport may be 0 = pick).
//
// An existing entry for the port blocks the bind only while its real port
// is still bound (port_free tells; closes are not always seen, e.g. libuv
// on macOS) and it belongs to another process. The same process binding
// the port again (IPv4 + IPv6 listeners) gets a socket without a second
// registration: connections keep going to the first one.
VN_HIDDEN vn_action vn_plan_bind(const struct sockaddr *addr, socklen_t len, in_port_t *vport, int *err,
                                 int (*port_free)(in_port_t real), pid_t caller, int *register_it);

#endif
