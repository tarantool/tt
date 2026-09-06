package main

import (
	"context"
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
	isSsl      *bool
	caFile     *string
	certFile   *string
	keyFile    *string
	port       *int
	unixSocket *string
}{
	isSsl:      flag.Bool("ssl", false, "Connection uses SSL if set, (default plain TCP)"),
	caFile:     flag.String("ca", "", "The CA file"),
	certFile:   flag.String("cert", "", "The TLS cert file"),
	keyFile:    flag.String("key", "", "The TLS key file"),
	port:       flag.Int("port", 50051, "The server port"),
	unixSocket: flag.String("unix", "", "The Unix socket name"),
}

func getCertificate() tls.Certificate {
	if *args.certFile == "" || *args.keyFile == "" {
		log.Fatalln("Both 'key_file' and 'cert_file' required")
	}
	tlsCert, err := tls.LoadX509KeyPair(*args.certFile, *args.keyFile)
	if err != nil {
		log.Fatalf("Could not load server key pair: %v", err)
	}
	return tlsCert
}

func getTLSConfig() *tls.Config {
	if *args.caFile == "" {
		return &tls.Config{
			Certificates: []tls.Certificate{getCertificate()},
			ClientAuth:   tls.NoClientCert,
		}
	}

	ca, err := os.ReadFile(*args.caFile)
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
	if !*args.isSsl {
		return []grpc.ServerOption{}
	}
	creds := credentials.NewTLS(getTLSConfig())
	return []grpc.ServerOption{grpc.Creds(creds)}
}

func getListener() net.Listener {
	var protocol string
	var address string

	if *args.unixSocket != "" {
		protocol = "unix"
		address = *args.unixSocket
		if strings.HasPrefix(address, "@") {
			address = "\x00" + address[1:]
		}
	} else {
		protocol = "tcp"
		address = fmt.Sprintf("localhost:%d", *args.port)
	}
	lis, err := (&net.ListenConfig{}).Listen(context.Background(), protocol, address)
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
	exitSig := make(chan os.Signal, 1)
	signal.Notify(exitSig,
		syscall.SIGTERM,
		syscall.SIGINT,
		syscall.SIGQUIT,
		syscall.SIGHUP,
	)
	s := <-exitSig
	log.Println("Got terminate signal:", s)

	srv.GracefulStop()
	wg.Wait()
	log.Println("Exit aeon mock server.")
}
