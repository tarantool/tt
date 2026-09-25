package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os/exec"
	"strings"

	"github.com/tarantool/tt/v3/cli/ttlog"
)

var (
	errNoValidIPFound = errors.New("no valid IP found")
)

// DaemonHandler is used to communicate with the daemon over HTTP.
type DaemonHandler struct {
	cmdPath string
	logger  ttlog.Logger
}

// resResult describes a failure during the command execution.
type resResult struct {
	Res any `json:"res"`
}

// errorResult describes a failure during the command execution.
type errorResult struct {
	Err string `json:"err"`
}

// NewDaemonHandler creates DaemonHandler.
func NewDaemonHandler(cmdPath string) *DaemonHandler {
	return &DaemonHandler{
		cmdPath: cmdPath,
		logger:  ttlog.NewCustomLogger(io.Discard, "", 0),
	}
}

// Logger sets logger for DaemonHandler.
func (handler *DaemonHandler) Logger(logger ttlog.Logger) *DaemonHandler {
	handler.logger = logger
	return handler
}

// ServeHTTP handles requests to the tt daemon.
func (handler *DaemonHandler) ServeHTTP(writer http.ResponseWriter, req *http.Request) {
	// Parse, check and call the command.
	var (
		res    any
		status int
		cmd    command
	)

	// Construct client IP msg.
	var clientIPMsg string

	clientIP, err := handler.getClientIP(req)
	if err != nil {
		clientIPMsg = err.Error()
	} else {
		clientIPMsg = clientIP
	}

	rawBody, err := parseCommand(req.Body, &cmd)
	if err != nil {
		status = http.StatusBadRequest
		res = &errorResult{err.Error()}
	} else {
		status = http.StatusOK
		// A daemon command must continue running if the HTTP client disconnects.
		commandCtx := context.WithoutCancel(req.Context())

		commandRes, err := handler.callCommand(commandCtx, &cmd)
		if err != nil {
			res = &errorResult{err.Error()}
		} else {
			res = &resResult{commandRes}
		}
	}

	// Construct json response.
	var jsonResMsg string

	jsonRes, err := json.Marshal(res)
	if err != nil {
		jsonResMsg = err.Error()
	} else {
		jsonResMsg = string(jsonRes)
	}

	// Log client IP, raw json request body, raw json response body.
	handler.logger.Printf("Client IP: %s; Request body: %s; Response body: %s",
		clientIPMsg, rawBody, jsonResMsg)

	// Write the result.
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)

	err = json.NewEncoder(writer).Encode(res)
	if err != nil {
		handler.logger.Printf("An error occurred while encoding the response: \"%v\"\n", err)
	}
}

// callCommand invokes the command and returns the execution result.
func (handler *DaemonHandler) callCommand(ctx context.Context, ttCmd *command) (string, error) {
	newArgs := append([]string{ttCmd.Name}, ttCmd.Params...)

	cmd := exec.CommandContext(ctx, handler.cmdPath, newArgs...)

	var (
		stderr bytes.Buffer
		stdout bytes.Buffer
	)

	cmd.Stderr = &stderr
	cmd.Stdout = &stdout

	err := cmd.Run()
	if err != nil {
		err = fmt.Errorf("%w: %s", err, stderr.String())
	}

	return stdout.String() + stderr.String(), err
}

// getClientIP gets the IP address of the client for an incoming HTTP request.
func (handler *DaemonHandler) getClientIP(req *http.Request) (string, error) {
	// Get IP from the X-REAL-IP header.
	// X-REAL-IP header contains only one
	// IP address of the client machine.
	// Note: this header can easily be spoofed
	// by the client.
	candidate := req.Header.Get("X-Real-IP")
	// Check IP is correct.
	netIP := net.ParseIP(candidate)
	if netIP != nil {
		return candidate, nil
	}

	// Get IP from X-FORWARDED-FOR header.
	// X-FORWARDED-FOR is a list of IP
	// addresses – proxy chaining.
	// Note: it can also be easily spoofed
	// by the client.
	ips := req.Header.Get("X-Forwarded-For")
	for ip := range strings.SplitSeq(ips, ",") {
		// Check IP is correct.
		netIP := net.ParseIP(ip)
		if netIP != nil {
			return ip, nil
		}
	}

	// Get IP from RemoteAddr attr.
	// RemoteAddr contains the IP address that
	// the response will be sent to. But in case the
	// client is connected through a proxy it will
	// give the IP address of the proxy.
	candidate, _, err := net.SplitHostPort(req.RemoteAddr)
	if err != nil {
		return "", fmt.Errorf("failed to parse the remote address: %w", err)
	}

	// Check IP is correct.
	netIP = net.ParseIP(candidate)
	if netIP != nil {
		return req.RemoteAddr, nil
	}

	return "", errNoValidIPFound
}
