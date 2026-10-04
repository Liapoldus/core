package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/Liapoldus/core/internal/infrastructure/storage"
)

func main() {
	if len(os.Args) != 2 {
		os.Exit(2)
	}
	unlock, err := storage.AcquireSQLiteStateLock(os.Args[1], 0o700)
	if err != nil {
		os.Exit(1)
	}
	defer unlock()
	fmt.Println("locked")
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop
}
