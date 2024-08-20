package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os/exec"
	"strings"

	"github.com/tarantool/tt/cli/ttlog"
)

// DaemonHandler is used to communicate with the daemon over HTTP.
type DaemonHandler struct {
	cmdPath string
	logger  ttlog.Logger
}

// resResult describes a failure during the command execution.
type resResult struct {
	Res interface{} `json:"res"`
}

// errorResult describes a failure during the command execution.
type errorResult struct {
	Err string `json:"err"`
}

// callCommand invokes the command and returns the execution result.
func (handler *DaemonHandler) callCommand(ttCmd *command) (string, error) {
	newArgs := append([]string{ttCmd.Name}, ttCmd.Params...)

	cmd := exec.Command(handler.cmdPath, newArgs...)

	var stderr bytes.Buffer
	var stdout bytes.Buffer

	cmd.Stderr = &stderr
	cmd.Stdout = &stdout
	err := cmd.Run()
	if err != nil {
		err = errors.New(fmt.Sprint(err) + ": " + stderr.String())
	}

	return stdout.String() + stderr.String(), err
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

// getClientIP gets the IP address of the client for an incoming HTTP request.
func (handler *DaemonHandler) getClientIP(req *http.Request) (string, error) {
	// Get IP from the X-REAL-IP header.
	// X-REAL-IP header contains only one
	// IP address of the client machine.
	// Note: this header can easily be spoofed
	// by the client.
	ip := req.Header.Get("X-REAL-IP")
	// Check IP is correct.
	netIP := net.ParseIP(ip)
	if netIP != nil {
		return ip, nil
	}

	// Get IP from X-FORWARDED-FOR header.
	// X-FORWARDED-FOR is a list of IP
	// addresses – proxy chaining.
	// Note: it can also be easily spoofed
	// by the client.
	ips := req.Header.Get("X-FORWARDED-FOR")
	splitIps := strings.Split(ips, ",")
	for _, ip := range splitIps {
		// Check IP is correct
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
	ip, _, err := net.SplitHostPort(req.RemoteAddr)
	if err != nil {
		return "", err
	}

	// Check IP is correct.
	netIP = net.ParseIP(ip)
	if netIP != nil {
		return req.RemoteAddr, nil
	}

	return "", fmt.Errorf("no valid IP found")
}

// ServeHTTP handles requests to the tt daemon.
func (handler *DaemonHandler) ServeHTTP(wr http.ResponseWriter, req *http.Request) {
	// Parse, check and call the command.
	var res interface{}
	var status int
	var cmd command

	// Construct client IP msg.
	var clientIpMsg string
	if ip, err := handler.getClientIP(req); err != nil {
		clientIpMsg = err.Error()
	} else {
		clientIpMsg = ip
	}

	rawBody, err := parseCommand(req.Body, &cmd)
	if err != nil {
		status = http.StatusBadRequest
		res = &errorResult{err.Error()}
	} else {
		status = http.StatusOK
		commandRes, err := handler.callCommand(&cmd)
		if err != nil {
			res = &errorResult{err.Error()}
		} else {
			res = &resResult{commandRes}
		}
	}

	// Construct json response.
	var jsonResMsg string
	if jsonRes, err := json.Marshal(res); err != nil {
		jsonResMsg = err.Error()
	} else {
		jsonResMsg = string(jsonRes)
	}

	// Log client IP, raw json request body, raw json response body.
	handler.logger.Printf("Client IP: %s; Request body: %s; Response body: %s",
		clientIpMsg, rawBody, jsonResMsg)

	// Write the result.
	wr.Header().Set("Content-Type", "application/json")
	wr.WriteHeader(status)
	if err := json.NewEncoder(wr).Encode(res); err != nil {
		handler.logger.Printf("An error occurred while encoding the response: \"%v\"\n", err)
	}
}
