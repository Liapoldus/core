package main

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"os"

	"github.com/quic-go/quic-go/http3"
)

func main() {
	certificate, err := os.ReadFile(os.Args[2])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(certificate) {
		fmt.Fprintln(os.Stderr, "test certificate could not be loaded")
		os.Exit(1)
	}
	transport := &http3.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, ServerName: "localhost"}}
	defer transport.Close()
	client := &http.Client{Transport: transport}
	response, err := client.Get(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer response.Body.Close()
	fmt.Printf("%d %d", response.ProtoMajor, response.StatusCode)
}
