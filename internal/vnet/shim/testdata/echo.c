// Test helper for the shim: "echo serve <port>" accepts one connection and
// echoes it; "echo call <host> <port> <msg>" resolves host with getaddrinfo,
// connects, sends msg and prints the reply. Plain libc, so the shim applies.
#include <netdb.h>
#include <netinet/in.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/socket.h>
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

int main(int argc, char **argv) {
    if (argc == 3 && strcmp(argv[1], "serve") == 0) return serve(atoi(argv[2]));
    if (argc == 5 && strcmp(argv[1], "call") == 0) return call(argv[2], argv[3], argv[4]);
    fprintf(stderr, "usage: echo serve <port> | echo call <host> <port> <msg>\n");
    return 2;
}
