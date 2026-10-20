// Test helper: a Go program (raw syscalls, no libc) for the supervisor.
//
//	goecho serve <port>        listen on :port, print the listen address,
//	                           echo one connection
//	goecho call <addr> <msg>   dial addr, send msg, print the reply
package main

import (
	"bufio"
	"fmt"
	"net"
	"os"
)

func main() {
	if len(os.Args) == 3 && os.Args[1] == "serve" {
		l, err := net.Listen("tcp", ":"+os.Args[2])
		if err != nil {
			fmt.Println("listen error:", err)
			os.Exit(1)
		}
		fmt.Println("listening", l.Addr())
		c, err := l.Accept()
		if err != nil {
			os.Exit(1)
		}
		line, _ := bufio.NewReader(c).ReadString('\n')
		fmt.Fprint(c, line)
		c.Close()
		return
	}
	if len(os.Args) == 4 && os.Args[1] == "call" {
		c, err := net.Dial("tcp", os.Args[2])
		if err != nil {
			fmt.Println("dial error:", err)
			os.Exit(1)
		}
		fmt.Fprintln(c, os.Args[3])
		line, _ := bufio.NewReader(c).ReadString('\n')
		fmt.Print(line)
		return
	}
	fmt.Fprintln(os.Stderr, "usage: goecho serve <port> | goecho call <addr> <msg>")
	os.Exit(2)
}
