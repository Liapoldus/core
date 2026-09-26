package main

import (
	"net"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
)

func main() {
	if len(os.Args) != 3 {
		os.Exit(2)
	}
	listener, err := net.Listen("tcp", os.Args[1])
	if err != nil {
		os.Exit(1)
	}
	tcpListener, ok := listener.(*net.TCPListener)
	if !ok {
		_ = listener.Close()
		os.Exit(1)
	}
	listenerFile, err := tcpListener.File()
	if err != nil {
		_ = listener.Close()
		os.Exit(1)
	}
	child := exec.Command(os.Args[2])
	child.Env = []string{}
	child.ExtraFiles = []*os.File{listenerFile}
	child.Stdin, child.Stdout, child.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := child.Start(); err != nil {
		_ = listenerFile.Close()
		_ = listener.Close()
		os.Exit(1)
	}
	_ = listenerFile.Close()
	_ = listener.Close()

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	waitDone := make(chan struct{})
	go func() {
		select {
		case received := <-signals:
			if child.Process != nil {
				_ = child.Process.Signal(received)
			}
		case <-waitDone:
		}
	}()
	err = child.Wait()
	close(waitDone)
	signal.Stop(signals)
	if err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			os.Exit(exit.ExitCode())
		}
		os.Exit(1)
	}
}
