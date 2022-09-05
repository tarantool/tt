package daemon

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/tarantool/tt/cli/daemon/api"
	"github.com/tarantool/tt/cli/ttlog"
)

const (
	defaultStopTimeout = 100 * time.Millisecond
)

// HTTPServer contains information for http server.
type HTTPServer struct {
	// listenIface is a network interface the IP address
	// should be found on to bind http server socket.
	listenIface string
	// port is a port number to be used for http server.
	port int
	// srv is http.Server instance.
	srv *http.Server
	// timeout is the time that was provided
	// to the HTTP server to shutdown correctly.
	timeout time.Duration
	// logger is  a log file the HTTP server will write to.
	logger *ttlog.Logger
}

// listenIP discovers IP address on the specified interface.
// If interface name is not specified returned value is 0.0.0.0;
// returns the first discovered IP address on the specified interface.
func (httpServer *HTTPServer) listenIP() (string, error) {
	if httpServer.listenIface == "" {
		return net.IPv4zero.String(), nil
	}

	iface, err := net.InterfaceByName(httpServer.listenIface)
	if err != nil {
		return "", err
	}

	if iface.Flags&net.FlagUp == 0 {
		return "", fmt.Errorf("Interface down")
	}

	addrs, err := iface.Addrs()
	if err != nil {
		return "", err
	}

	// The socket will bind to the first discovered
	// IP address on the specified interface.
	for _, addr := range addrs {
		var ip net.IP
		switch v := addr.(type) {
		case *net.IPNet:
			ip = v.IP
		case *net.IPAddr:
			ip = v.IP
		}

		if ip == nil {
			continue
		}

		// TODO: Support IPv6 by option
		// Not an IPv4 address.
		if ip.To4() == nil {
			continue
		}

		return ip.String(), nil
	}

	return "", fmt.Errorf("listen IP is not available")
}

// NewHTTPServer creates new HTTPServer.
func NewHTTPServer(listenInterface string, port int) *HTTPServer {
	return &HTTPServer{
		listenIface: listenInterface,
		port:        port,
		timeout:     defaultStopTimeout,
	}
}

// Timeout sets the time that was provided to the HTTP server
// to shutdown correctly.
func (httpServer *HTTPServer) Timeout(timeout time.Duration) *HTTPServer {
	httpServer.timeout = timeout
	return httpServer
}

// SetLogger sets a log file the HTTP server will write to.
func (httpServer *HTTPServer) SetLogger(logger *ttlog.Logger) {
	httpServer.logger = logger
}

// Start starts HTTP server.
func (httpServer *HTTPServer) Start(ttPath string) {
	ip, err := httpServer.listenIP()
	if err != nil {
		httpServer.logger.Fatalf("Can't get IP")
	}

	httpServerAddr := ip + ":" + strconv.Itoa(httpServer.port)
	httpServer.srv = &http.Server{
		Addr: httpServerAddr,
	}

	// Prepare HTTP server.
	daemonHandler := api.NewDaemonHandler(ttPath).Logger(httpServer.logger)
	http.Handle("/tarantool", daemonHandler)

	// Start HTTP server.
	socket, err := net.Listen("tcp4", httpServer.srv.Addr)
	if err != nil {
		httpServer.logger.Fatal(err)
	}

	if err := httpServer.srv.Serve(socket); err != http.ErrServerClosed {
		httpServer.logger.Fatalf("Can't start HTTP server")
	}
}

// Stop stops HTTP server.
func (httpServer *HTTPServer) Stop() error {
	var err error

	if httpServer.srv == nil {
		return fmt.Errorf("Server is not started")
	}

	ctx, cancel := context.WithTimeout(context.Background(), httpServer.timeout)
	if err = httpServer.srv.Shutdown(ctx); err != nil {
		httpServer.logger.Printf(`HTTP server shutdown error: "%v"`, err)
	}
	cancel()

	return err
}
