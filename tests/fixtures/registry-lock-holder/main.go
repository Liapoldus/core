package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	if len(os.Args) != 2 {
		os.Exit(2)
	}
	file, err := os.OpenFile(os.Args[1], os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		os.Exit(3)
	}
	defer file.Close()
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		os.Exit(4)
	}
	_, _ = fmt.Fprintln(os.Stdout, "ready")
	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, syscall.SIGTERM, syscall.SIGINT)
	<-shutdown
}
