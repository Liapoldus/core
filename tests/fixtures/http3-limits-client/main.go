package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
)

func main() {
	roots := x509.NewCertPool()
	pem, err := os.ReadFile(os.Args[2])
	if err != nil || !roots.AppendCertsFromPEM(pem) {
		os.Exit(2)
	}

	switch os.Args[3] {
	case "connections":
		checkConnections(roots)
	case "streams":
		checkStreams(roots)
	case "idle":
		checkIdle(roots)
	default:
		os.Exit(2)
	}
}

func client(roots *x509.CertPool) (*http.Client, *http3.Transport) {
	transport := &http3.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, ServerName: "localhost"}}
	return &http.Client{Transport: transport}, transport
}

func checkConnections(roots *x509.CertPool) {
	var wait sync.WaitGroup
	var successes int
	var mutex sync.Mutex
	for range 2 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			client, transport := client(roots)
			defer transport.Close()
			response, err := client.Get(os.Args[1])
			if err != nil {
				return
			}
			defer response.Body.Close()
			_, _ = io.Copy(io.Discard, response.Body)
			if response.StatusCode == http.StatusOK {
				mutex.Lock()
				successes++
				mutex.Unlock()
			}
		}()
	}
	wait.Wait()
	fmt.Print(successes)
}

func checkStreams(roots *x509.CertPool) {
	client, transport := client(roots)
	defer transport.Close()
	var wait sync.WaitGroup
	for range 2 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			response, err := client.Get(os.Args[1])
			if err == nil {
				defer response.Body.Close()
				_, _ = io.Copy(io.Discard, response.Body)
			}
		}()
	}
	wait.Wait()
}

func checkIdle(roots *x509.CertPool) {
	address := strings.TrimSuffix(strings.TrimPrefix(os.Args[1], "https://"), "/")
	certificateName := tls.Config{RootCAs: roots, ServerName: "localhost", NextProtos: []string{http3.NextProtoH3}}
	connection, err := quic.DialAddr(context.Background(), address, &certificateName, nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(3)
	}
	defer connection.CloseWithError(0, "")
	deadline, err := strconv.Atoi(os.Args[4])
	if err != nil {
		os.Exit(2)
	}
	select {
	case <-connection.Context().Done():
		fmt.Print("closed")
	case <-time.After(time.Duration(deadline) * time.Second):
		fmt.Print("open")
	}
}
