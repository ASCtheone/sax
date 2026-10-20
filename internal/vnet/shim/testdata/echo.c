// Test helper for the shim: "echo serve <port>" accepts one connection and
// echoes it; "echo call <host> <port> <msg>" resolves host with getaddrinfo,
// connects, sends msg and prints the reply. Plain libc, so the shim applies.
#include <arpa/inet.h>
#include <netdb.h>
#include <netinet/in.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/socket.h>
#include <sys/syscall.h>
#include <unistd.h>

static int serve(int port) {
    int fd = socket(AF_INET, SOCK_STREAM, 0);
    struct sockaddr_in a = {0};
    a.sin_family = AF_INET;
    a.sin_port = htons(port);
    a.sin_addr.s_addr = htonl(INADDR_ANY);
    if (bind(fd, (struct sockaddr *)&a, sizeof a) != 0) { perror("bind"); return 1; }
    if (listen(fd, 1) != 0) { perror("listen"); return 1; }
    struct sockaddr_in got;
    socklen_t len = sizeof got;
    getsockname(fd, (struct sockaddr *)&got, &len);
    printf("listening %u\n", ntohs(got.sin_port));
    fflush(stdout);
    int c = accept(fd, NULL, NULL);
    char buf[256];
    ssize_t n = read(c, buf, sizeof buf);
    if (n > 0) write(c, buf, n);
    close(c);
    close(fd);
    return 0;
}

static int call(const char *host, const char *port, const char *msg) {
    struct addrinfo hints = {0}, *res;
    hints.ai_family = AF_INET;
    hints.ai_socktype = SOCK_STREAM;
    int rc = getaddrinfo(host, port, &hints, &res);
    if (rc != 0) { fprintf(stderr, "getaddrinfo: %s\n", gai_strerror(rc)); return 1; }
    int fd = socket(AF_INET, SOCK_STREAM, 0);
    if (connect(fd, res->ai_addr, res->ai_addrlen) != 0) { perror("connect"); return 1; }
    freeaddrinfo(res);
    write(fd, msg, strlen(msg));
    char buf[256] = {0};
    read(fd, buf, sizeof buf - 1);
    printf("%s\n", buf);
    close(fd);
    return 0;
}

static int listen_on(int family, const char *ip, int port) {
    int fd = socket(family, SOCK_STREAM, 0);
    int one = 1;
    setsockopt(fd, SOL_SOCKET, SO_REUSEADDR, &one, sizeof one);
    struct sockaddr_storage ss = {0};
    socklen_t len;
    if (family == AF_INET6) {
        struct sockaddr_in6 *a = (struct sockaddr_in6 *)&ss;
        a->sin6_family = AF_INET6;
        a->sin6_port = htons(port);
        inet_pton(AF_INET6, ip, &a->sin6_addr);
        len = sizeof *a;
    } else {
        struct sockaddr_in *a = (struct sockaddr_in *)&ss;
        a->sin_family = AF_INET;
        a->sin_port = htons(port);
        inet_pton(AF_INET, ip, &a->sin_addr);
        len = sizeof *a;
    }
    if (bind(fd, (struct sockaddr *)&ss, len) != 0) return -1;
    if (listen(fd, 1) != 0) return -1;
    return fd;
}

// dual: one process listens on 127.0.0.1:port and [::1]:port (Fastify,
// dev servers resolving localhost to both families).
static int dual(int port) {
    if (listen_on(AF_INET, "127.0.0.1", port) < 0) { perror("first"); return 1; }
    if (listen_on(AF_INET6, "::1", port) < 0) { perror("second"); return 1; }
    printf("dual ok\n");
    return 0;
}

// probe: check the port (listen, then close with the raw syscall like libuv
// does, which the hooks do not see), then really listen.
static int probe(int port) {
    for (int i = 0; i < 3; i++) {
        int fd = listen_on(AF_INET, "0.0.0.0", port);
        if (fd < 0) { perror("check"); return 1; }
#ifdef __APPLE__
#pragma clang diagnostic push
#pragma clang diagnostic ignored "-Wdeprecated-declarations"
#endif
        syscall(SYS_close, fd);
#ifdef __APPLE__
#pragma clang diagnostic pop
#endif
    }
    if (listen_on(AF_INET, "0.0.0.0", port) < 0) { perror("real listen"); return 1; }
    printf("probe ok\n");
    return 0;
}

int main(int argc, char **argv) {
    if (argc == 3 && strcmp(argv[1], "dual") == 0) return dual(atoi(argv[2]));
    if (argc == 3 && strcmp(argv[1], "probe") == 0) return probe(atoi(argv[2]));
    if (argc == 3 && strcmp(argv[1], "serve") == 0) return serve(atoi(argv[2]));
    if (argc == 5 && strcmp(argv[1], "call") == 0) return call(argv[2], argv[3], argv[4]);
    fprintf(stderr, "usage: echo serve <port> | echo call <host> <port> <msg>\n");
    return 2;
}
