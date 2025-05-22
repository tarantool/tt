package main

import (
	"crypto/tls"
	"crypto/x509"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"

	"mock/server/aeon/service"

	"github.com/tarantool/tt/cli/aeon/pb"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

var args = struct {
	is_ssl      *bool
	ca_file     *string
	cert_file   *string
	key_file    *string
	port        *int
	unix_socket *string
}{
	is_ssl:      flag.Bool("ssl", false, "Connection uses SSL if set, (default plain TCP)"),
	ca_file:     flag.String("ca", "", "The CA file"),
	cert_file:   flag.String("cert", "", "The TLS cert file"),
	key_file:    flag.String("key", "", "The TLS key file"),
	port:        flag.Int("port", 50051, "The server port"),
	unix_socket: flag.String("unix", "", "The Unix socket name"),
}

func getCertificate() tls.Certificate {
	if *args.cert_file == "" || *args.key_file == "" {
		log.Fatalln("Both 'key_file' and 'cert_file' required")
	}
	tls_cert, err := tls.LoadX509KeyPair(*args.cert_file, *args.key_file)
	if err != nil {
		log.Fatalf("Could not load server key pair: %v", err)
	}
	return tls_cert
}

func getTlsConfig() *tls.Config {
	if *args.ca_file == "" {
		return &tls.Config{
			Certificates: []tls.Certificate{getCertificate()},
			ClientAuth:   tls.NoClientCert,
		}
	}

	ca, err := os.ReadFile(*args.ca_file)
	if err != nil {
		log.Fatalf("Failed to read CA file: %v", err)
	}
	certPool := x509.NewCertPool()
	if !certPool.AppendCertsFromPEM(ca) {
		log.Fatalln("Failed to append CA data")
	}
	return &tls.Config{
		Certificates: []tls.Certificate{getCertificate()},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    certPool,
	}
}

func getServerOpts() []grpc.ServerOption {
	if !*args.is_ssl {
		return []grpc.ServerOption{}
	}
	creds := credentials.NewTLS(getTlsConfig())
	return []grpc.ServerOption{grpc.Creds(creds)}
}

func getListener() net.Listener {
	var protocol string
	var address string

	if *args.unix_socket != "" {
		protocol = "unix"
		address = *args.unix_socket
		if strings.HasPrefix(address, "@") {
			address = "\x00" + address[1:]
		}
	} else {
		protocol = "tcp"
		address = fmt.Sprintf("localhost:%d", *args.port)
	}
	lis, err := net.Listen(protocol, address)
	if err != nil {
		log.Fatalf("Failed to listen: %v", err)
	}
	return lis
}

func main() {
	log.Println("Start aeon mock server:", os.Args)

	flag.Parse()

	srv := grpc.NewServer(getServerOpts()...)
	pb.RegisterSQLServiceServer(srv, &service.Server{})
	pb.RegisterDiagServiceServer(srv, &service.Diag{})

	// Run gRPC server.
	wg := sync.WaitGroup{}
	wg.Add(1)
	go func() {
		if err := srv.Serve(getListener()); err != nil {
			log.Fatalf("Failed to serve: %v", err)
		}
		wg.Done()
	}()

	// Shutdown on signals.
	exit_sig := make(chan os.Signal, 1)
	signal.Notify(exit_sig,
		syscall.SIGTERM,
		syscall.SIGINT,
		syscall.SIGQUIT,
		syscall.SIGHUP,
	)
	s := <-exit_sig
	log.Println("Got terminate signal:", s)

	srv.GracefulStop()
	wg.Wait()
	log.Println("Exit aeon mock server.")
}
