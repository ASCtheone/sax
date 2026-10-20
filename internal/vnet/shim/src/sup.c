// sax vnet supervisor (Linux): runs a pane's command under a seccomp filter
// that hands connect/bind/getsockname to this process, so binaries that skip
// libc (Go, static) get the same virtual addresses as the LD_PRELOAD shim.
//
//   sax-vnet-sup <command> [args...]      (with SAX_VNET_* in the environment)
//
// The child installs the filter (SECCOMP_FILTER_FLAG_NEW_LISTENER) and passes
// the listener here before exec. For each notification the supervisor takes
// a duplicate of the target's socket (pidfd_getfd) and performs the
// rewritten call on it, so the target's socket, flags and epoll registration
// stay intact. Unrelated calls continue untouched. It is a child subreaper,
// so processes that outlive their parent stay supervised, and it exits with
// the command's status once no descendants are left.
//
// Needs Linux 5.9+ and permission to take another process's fds (blocked by
// Yama ptrace_scope >= 2 and by Docker's default seccomp profile). When
// supervision is not possible the command runs with LD_PRELOAD=$SAX_VNET_SHIM
// instead, and no supervisor process stays around.
//
//   sax-vnet-sup --check     exit 0 if supervision works here, 1 otherwise

#define _GNU_SOURCE

#include <errno.h>
#include <linux/audit.h>
#include <linux/filter.h>
#include <linux/seccomp.h>
#include <poll.h>
#include <signal.h>
#include <stddef.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/ioctl.h>
#include <sys/prctl.h>
#include <sys/signalfd.h>
#include <sys/socket.h>
#include <sys/stat.h>
#include <sys/syscall.h>
#include <sys/uio.h>
#include <sys/wait.h>
#include <unistd.h>

#include "common.h"

#ifndef __NR_pidfd_open
#define __NR_pidfd_open 434
#endif
#ifndef __NR_pidfd_getfd
#define __NR_pidfd_getfd 438
#endif

#if defined(__x86_64__)
#define SUP_ARCH AUDIT_ARCH_X86_64
#elif defined(__aarch64__)
#define SUP_ARCH AUDIT_ARCH_AARCH64
#else
#error "sax-vnet-sup: unsupported architecture"
#endif

#define DNS_PORT 53
#define MAX_VFDS 1024

// A listening socket whose getsockname must report the virtual address.
typedef struct {
    pid_t tgid;  // 0 = free slot
    int fd;
    ino_t ino;   // guards against fd reuse after close
    in_port_t vport;
} vfd_t;

static vfd_t vfds[MAX_VFDS];
static int warned_access;

// ---- small syscall helpers -------------------------------------------------

static int install_filter(void) {
    struct sock_filter f[] = {
        BPF_STMT(BPF_LD | BPF_W | BPF_ABS, offsetof(struct seccomp_data, arch)),
        BPF_JUMP(BPF_JMP | BPF_JEQ | BPF_K, SUP_ARCH, 1, 0),
        BPF_STMT(BPF_RET | BPF_K, SECCOMP_RET_ALLOW),
        BPF_STMT(BPF_LD | BPF_W | BPF_ABS, offsetof(struct seccomp_data, nr)),
        BPF_JUMP(BPF_JMP | BPF_JEQ | BPF_K, __NR_connect, 3, 0),
        BPF_JUMP(BPF_JMP | BPF_JEQ | BPF_K, __NR_bind, 2, 0),
        BPF_JUMP(BPF_JMP | BPF_JEQ | BPF_K, __NR_getsockname, 1, 0),
        BPF_STMT(BPF_RET | BPF_K, SECCOMP_RET_ALLOW),
        BPF_STMT(BPF_RET | BPF_K, SECCOMP_RET_USER_NOTIF),
    };
    struct sock_fprog prog = {.len = sizeof f / sizeof f[0], .filter = f};
    if (prctl(PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0) != 0) return -1;
    return (int)syscall(__NR_seccomp, SECCOMP_SET_MODE_FILTER, SECCOMP_FILTER_FLAG_NEW_LISTENER, &prog);
}

static int send_fd(int sock, int fd) {
    char byte = 'L';
    struct iovec iov = {.iov_base = &byte, .iov_len = 1};
    char ctrl[CMSG_SPACE(sizeof(int))] = {0};
    struct msghdr msg = {.msg_iov = &iov, .msg_iovlen = 1};
    if (fd >= 0) {
        msg.msg_control = ctrl;
        msg.msg_controllen = sizeof ctrl;
        struct cmsghdr *c = CMSG_FIRSTHDR(&msg);
        c->cmsg_level = SOL_SOCKET;
        c->cmsg_type = SCM_RIGHTS;
        c->cmsg_len = CMSG_LEN(sizeof(int));
        memcpy(CMSG_DATA(c), &fd, sizeof fd);
    } else {
        byte = 'F';
    }
    return sendmsg(sock, &msg, 0) == 1 ? 0 : -1;
}

static int recv_fd(int sock) {
    char byte;
    struct iovec iov = {.iov_base = &byte, .iov_len = 1};
    char ctrl[CMSG_SPACE(sizeof(int))] = {0};
    struct msghdr msg = {.msg_iov = &iov, .msg_iovlen = 1, .msg_control = ctrl, .msg_controllen = sizeof ctrl};
    if (recvmsg(sock, &msg, 0) != 1 || byte != 'L') return -1;
    struct cmsghdr *c = CMSG_FIRSTHDR(&msg);
    if (!c || c->cmsg_type != SCM_RIGHTS) return -1;
    int fd;
    memcpy(&fd, CMSG_DATA(c), sizeof fd);
    return fd;
}

static int read_mem(pid_t pid, unsigned long long addr, void *buf, size_t len) {
    struct iovec local = {.iov_base = buf, .iov_len = len};
    struct iovec remote = {.iov_base = (void *)(uintptr_t)addr, .iov_len = len};
    return process_vm_readv(pid, &local, 1, &remote, 1, 0) == (ssize_t)len ? 0 : -1;
}

static int write_mem(pid_t pid, unsigned long long addr, const void *buf, size_t len) {
    struct iovec local = {.iov_base = (void *)buf, .iov_len = len};
    struct iovec remote = {.iov_base = (void *)(uintptr_t)addr, .iov_len = len};
    return process_vm_writev(pid, &local, 1, &remote, 1, 0) == (ssize_t)len ? 0 : -1;
}

static pid_t tgid_of(pid_t tid) {
    char path[64], line[128];
    snprintf(path, sizeof path, "/proc/%d/status", (int)tid);
    FILE *f = fopen(path, "r");
    if (!f) return -1;
    pid_t tgid = -1;
    while (fgets(line, sizeof line, f))
        if (sscanf(line, "Tgid: %d", &tgid) == 1) break;
    fclose(f);
    return tgid;
}

// Duplicates fd from the target process into ours.
static int take_fd(pid_t tid, int fd) {
    pid_t tgid = tgid_of(tid);
    if (tgid <= 0) return -1;
    int pidfd = (int)syscall(__NR_pidfd_open, tgid, 0);
    if (pidfd < 0) return -1;
    int dup = (int)syscall(__NR_pidfd_getfd, pidfd, fd, 0);
    int saved = errno;
    close(pidfd);
    if (dup < 0 && !warned_access) {
        warned_access = 1;
        fprintf(stderr, "sax vnet: cannot reach the sockets of supervised processes (%s); "
                        "virtual addresses are off in this pane\n", strerror(saved));
    }
    return dup;
}

static int sock_type(int fd) {
    int type = 0;
    socklen_t len = sizeof type;
    return getsockopt(fd, SOL_SOCKET, SO_TYPE, &type, &len) == 0 ? type : -1;
}

static in_port_t sa_port(const struct sockaddr_storage *sa) {
    return sa->ss_family == AF_INET6 ? ((const struct sockaddr_in6 *)sa)->sin6_port
                                     : ((const struct sockaddr_in *)sa)->sin_port;
}

// port_free reports whether 127.0.0.1:real (network order) can be bound,
// i.e. a registry entry for it is stale.
static int port_free(in_port_t real) {
    int s = socket(AF_INET, SOCK_STREAM | SOCK_CLOEXEC, 0);
    if (s < 0) return 0;
    int one = 1;
    setsockopt(s, SOL_SOCKET, SO_REUSEADDR, &one, sizeof one);
    struct sockaddr_storage a;
    socklen_t len = vn_real_addr(AF_INET, real, &a);
    int ok = bind(s, (struct sockaddr *)&a, len) == 0;
    close(s);
    return ok;
}

// ---- getsockname bookkeeping -----------------------------------------------

static void vfd_add(pid_t tgid, int fd, ino_t ino, in_port_t vport) {
    int slot = -1;
    for (int i = 0; i < MAX_VFDS; i++) {
        if (vfds[i].tgid == tgid && vfds[i].fd == fd) { slot = i; break; }
        if (slot < 0 && (vfds[i].tgid == 0 || (kill(vfds[i].tgid, 0) != 0 && errno == ESRCH))) slot = i;
    }
    if (slot < 0) slot = (int)(ino % MAX_VFDS);  // table full of live entries: evict one
    vfds[slot] = (vfd_t){.tgid = tgid, .fd = fd, .ino = ino, .vport = vport};
}

static vfd_t *vfd_find(pid_t tgid, int fd) {
    for (int i = 0; i < MAX_VFDS; i++)
        if (vfds[i].tgid == tgid && vfds[i].fd == fd) return &vfds[i];
    return NULL;
}

// ---- notification handlers -------------------------------------------------

static void reply(struct seccomp_notif_resp *resp, int err) {
    resp->flags = 0;
    resp->error = err ? -err : 0;
    resp->val = 0;
}

static int notif_valid(int lfd, __u64 id) { return ioctl(lfd, SECCOMP_IOCTL_NOTIF_ID_VALID, &id) == 0; }

// Reads the sockaddr argument; returns its length or 0 if not IPv4/IPv6.
static socklen_t read_sockaddr(const struct seccomp_notif *req, struct sockaddr_storage *sa) {
    socklen_t len = (socklen_t)req->data.args[2];
    if (len > sizeof *sa) len = sizeof *sa;
    if (len < sizeof(sa_family_t)) return 0;
    memset(sa, 0, sizeof *sa);
    if (read_mem(req->pid, req->data.args[1], sa, len) != 0) return 0;
    return (sa->ss_family == AF_INET || sa->ss_family == AF_INET6) ? len : 0;
}

static void handle_connect(int lfd, const struct seccomp_notif *req, struct seccomp_notif_resp *resp) {
    struct sockaddr_storage sa;
    socklen_t len = read_sockaddr(req, &sa);
    if (!len || !notif_valid(lfd, req->id)) return;
    int dup = take_fd(req->pid, (int)req->data.args[0]);
    if (dup < 0) return;

    int type = sock_type(dup);
    struct sockaddr_storage t;
    if (type == SOCK_STREAM) {
        in_port_t real;
        int err = 0;
        switch (vn_plan_connect((struct sockaddr *)&sa, len, &real, &err)) {
        case VN_PASS: break;
        case VN_FAIL: reply(resp, err); break;
        case VN_REWRITE: {
            socklen_t tl = vn_real_addr(sa.ss_family, real, &t);
            reply(resp, connect(dup, (struct sockaddr *)&t, tl) == 0 ? 0 : errno);
            break;
        }
        }
    } else if (type == SOCK_DGRAM && sa_port(&sa) == htons(DNS_PORT)) {
        in_port_t dns = vn_dns_port();
        if (dns) {
            socklen_t tl = vn_real_addr(sa.ss_family, dns, &t);
            if (connect(dup, (struct sockaddr *)&t, tl) == 0) reply(resp, 0);
            // else: e.g. an IPv6-only socket; let the original query through
        }
    }
    close(dup);
}

static void handle_bind(int lfd, const struct seccomp_notif *req, struct seccomp_notif_resp *resp) {
    struct sockaddr_storage sa;
    socklen_t len = read_sockaddr(req, &sa);
    if (!len || !notif_valid(lfd, req->id)) return;
    int fd = (int)req->data.args[0];
    int dup = take_fd(req->pid, fd);
    if (dup < 0) return;
    if (sock_type(dup) != SOCK_STREAM) {
        close(dup);
        return;
    }

    in_port_t vport;
    int err = 0;
    switch (vn_plan_bind((struct sockaddr *)&sa, len, &vport, &err, port_free)) {
    case VN_PASS: break;
    case VN_FAIL: reply(resp, err); break;
    case VN_REWRITE: {
        if (sa.ss_family == AF_INET6) {
            int off = 0;
            setsockopt(dup, IPPROTO_IPV6, IPV6_V6ONLY, &off, sizeof off);
        }
        struct sockaddr_storage t, got;
        socklen_t tl = vn_real_addr(sa.ss_family, 0, &t), gl = sizeof got;
        if (bind(dup, (struct sockaddr *)&t, tl) != 0 || getsockname(dup, (struct sockaddr *)&got, &gl) != 0) {
            reply(resp, errno);
            break;
        }
        in_port_t real = sa_port(&got);
        if (vport == 0) vport = real;
        pid_t tgid = tgid_of(req->pid);
        struct stat st;
        if (tgid <= 0 || fstat(dup, &st) != 0 || vn_register_port(vn_cfg.self, vport, real, tgid) != 0) {
            reply(resp, EADDRNOTAVAIL);
            break;
        }
        vfd_add(tgid, fd, st.st_ino, vport);
        reply(resp, 0);
        break;
    }
    }
    close(dup);
}

static void handle_getsockname(int lfd, const struct seccomp_notif *req, struct seccomp_notif_resp *resp) {
    int fd = (int)req->data.args[0];
    pid_t tgid = tgid_of(req->pid);
    vfd_t *v = tgid > 0 ? vfd_find(tgid, fd) : NULL;
    if (!v) return;

    int dup = take_fd(req->pid, fd);
    if (dup < 0) return;
    struct stat st;
    struct sockaddr_storage real;
    socklen_t rl = sizeof real;
    int same = fstat(dup, &st) == 0 && st.st_ino == v->ino && getsockname(dup, (struct sockaddr *)&real, &rl) == 0;
    close(dup);
    if (!same) {
        v->tgid = 0;  // the fd now names another socket
        return;
    }

    socklen_t ulen;
    if (read_mem(req->pid, req->data.args[2], &ulen, sizeof ulen) != 0 || !notif_valid(lfd, req->id)) return;
    struct sockaddr_storage virt;
    socklen_t vlen = vn_virtual_addr(real.ss_family, vn_cfg.self, v->vport, &virt);
    size_t n = ulen < vlen ? ulen : vlen;
    if (write_mem(req->pid, req->data.args[1], &virt, n) != 0 ||
        write_mem(req->pid, req->data.args[2], &vlen, sizeof vlen) != 0)
        return;
    reply(resp, 0);
}

static void handle_notification(int lfd, struct seccomp_notif *req, struct seccomp_notif_resp *resp,
                                size_t req_size, size_t resp_size) {
    memset(req, 0, req_size);
    if (ioctl(lfd, SECCOMP_IOCTL_NOTIF_RECV, req) != 0) return;
    memset(resp, 0, resp_size);
    resp->id = req->id;
    resp->flags = SECCOMP_USER_NOTIF_FLAG_CONTINUE;

    if (req->data.nr == __NR_connect)
        handle_connect(lfd, req, resp);
    else if (req->data.nr == __NR_bind)
        handle_bind(lfd, req, resp);
    else if (req->data.nr == __NR_getsockname)
        handle_getsockname(lfd, req, resp);

    ioctl(lfd, SECCOMP_IOCTL_NOTIF_SEND, resp);  // ENOENT: target died meanwhile
}

// ---- process management ----------------------------------------------------

static void exec_command(char **argv) {
    execvp(argv[0], argv);
    fprintf(stderr, "sax-vnet-sup: %s: %s\n", argv[0], strerror(errno));
    _exit(127);
}

// exec_preloaded runs the command with the libc-level shim instead.
static void exec_preloaded(char **argv) {
    const char *shim = getenv("SAX_VNET_SHIM");
    if (shim) setenv("LD_PRELOAD", shim, 1);
    exec_command(argv);
}

// supervision_possible checks, before forking, that this kernel and policy
// let us take a process's fds: Yama scope 2+ forbids it outright, and a
// seccomp policy that blocks pidfd_getfd shows up on our own fd.
static int supervision_possible(void) {
    FILE *f = fopen("/proc/sys/kernel/yama/ptrace_scope", "r");
    if (f) {
        int scope = 0;
        int ok = fscanf(f, "%d", &scope) == 1;
        fclose(f);
        if (ok && scope >= 2) return 0;
    }
    int pidfd = (int)syscall(__NR_pidfd_open, getpid(), 0);
    if (pidfd < 0) return 0;
    int dup = (int)syscall(__NR_pidfd_getfd, pidfd, pidfd, 0);
    close(pidfd);
    if (dup < 0) return 0;
    close(dup);
    return 1;
}

static void child_main(int sock, char **argv) {
    int lfd = install_filter();
    send_fd(sock, lfd);
    if (lfd < 0) {  // kernel without user notification: libc-level shim
        close(sock);
        exec_preloaded(argv);
    }
    close(lfd);
    char ack;
    if (read(sock, &ack, 1) != 1) _exit(126);  // supervisor is gone
    close(sock);
    exec_command(argv);
}

static int exit_code(int status) {
    if (WIFEXITED(status)) return WEXITSTATUS(status);
    if (WIFSIGNALED(status)) {
        int sig = WTERMSIG(status);
        signal(sig, SIG_DFL);
        sigset_t s;
        sigemptyset(&s);
        sigaddset(&s, sig);
        sigprocmask(SIG_UNBLOCK, &s, NULL);
        raise(sig);
        return 128 + sig;
    }
    return 1;
}

int main(int argc, char **argv) {
    if (argc < 2) {
        fprintf(stderr, "usage: sax-vnet-sup <command> [args...] | sax-vnet-sup --check\n");
        return 2;
    }
    if (strcmp(argv[1], "--check") == 0) return supervision_possible() ? 0 : 1;
    if (!vn_load_cfg()) exec_command(argv + 1);  // not in a router: nothing to do
    if (!supervision_possible()) exec_preloaded(argv + 1);

    prctl(PR_SET_CHILD_SUBREAPER, 1, 0, 0, 0);
    int sp[2];
    if (socketpair(AF_UNIX, SOCK_STREAM | SOCK_CLOEXEC, 0, sp) != 0) exec_command(argv + 1);

    pid_t child = fork();
    if (child < 0) exec_command(argv + 1);
    if (child == 0) {
        close(sp[0]);
        child_main(sp[1], argv + 1);
    }
    close(sp[1]);
    int lfd = recv_fd(sp[0]);
    if (lfd >= 0 && write(sp[0], "k", 1) != 1) {
        close(lfd);
        lfd = -1;
    }
    close(sp[0]);

    // Ctrl-C/Ctrl-\/Ctrl-Z reach the command through the terminal; the
    // supervisor only forwards termination requests and waits.
    sigset_t sigs;
    sigemptyset(&sigs);
    sigaddset(&sigs, SIGCHLD);
    sigaddset(&sigs, SIGTERM);
    sigaddset(&sigs, SIGHUP);
    sigaddset(&sigs, SIGINT);
    sigaddset(&sigs, SIGQUIT);
    sigaddset(&sigs, SIGTSTP);  // Ctrl-Z stops the command, not its supervisor
    sigprocmask(SIG_BLOCK, &sigs, NULL);
    int sfd = signalfd(-1, &sigs, SFD_CLOEXEC);

    struct seccomp_notif_sizes sizes = {0};
    syscall(__NR_seccomp, SECCOMP_GET_NOTIF_SIZES, 0, &sizes);
    size_t req_size = sizes.seccomp_notif > sizeof(struct seccomp_notif) ? sizes.seccomp_notif : sizeof(struct seccomp_notif);
    size_t resp_size = sizes.seccomp_notif_resp > sizeof(struct seccomp_notif_resp) ? sizes.seccomp_notif_resp
                                                                                       : sizeof(struct seccomp_notif_resp);
    struct seccomp_notif *req = calloc(1, req_size);
    struct seccomp_notif_resp *resp = calloc(1, resp_size);

    int main_status = 0, main_done = 0;
    for (;;) {
        struct pollfd fds[2] = {{.fd = lfd, .events = POLLIN}, {.fd = sfd, .events = POLLIN}};
        if (poll(fds, 2, -1) < 0) {
            if (errno == EINTR) continue;
            break;
        }
        if (fds[0].revents & POLLIN) handle_notification(lfd, req, resp, req_size, resp_size);
        if (fds[0].revents & (POLLHUP | POLLERR | POLLNVAL)) {
            close(lfd);
            lfd = -1;  // no supervised task is left
        }
        if (!(fds[1].revents & POLLIN)) continue;

        struct signalfd_siginfo si;
        if (read(sfd, &si, sizeof si) != sizeof si) continue;
        if (si.ssi_signo == SIGTERM || si.ssi_signo == SIGHUP) {
            if (!main_done) kill(child, (int)si.ssi_signo);
            continue;
        }
        if (si.ssi_signo != SIGCHLD) continue;

        int status;
        pid_t p;
        while ((p = waitpid(-1, &status, WNOHANG)) > 0) {
            if (p == child) {
                main_done = 1;
                main_status = status;
            }
        }
        if (main_done && p < 0 && errno == ECHILD) break;  // no descendants left
    }
    return exit_code(main_status);
}
