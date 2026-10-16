package httpx

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func AgentOSListenAddressIsLoopback(address string, emptyHostIsLoopback bool) bool {
	address = strings.TrimSpace(address)
	if address == "" {
		return emptyHostIsLoopback
	}
	if emptyHostIsLoopback {
		if _, err := strconv.Atoi(address); err == nil {
			return true
		}
	}
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return false
	}
	host = strings.TrimSpace(host)
	if host == "" {
		return emptyHostIsLoopback
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	if zone := strings.LastIndex(host, "%"); zone >= 0 {
		host = host[:zone]
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

type AgentOSDataPlaneHandlers struct {
	Roles           http.Handler
	Tasks           http.Handler
	TaskByID        http.Handler
	Files           http.Handler
	Models          http.Handler
	ChatCompletions http.Handler
	Live            http.Handler
	Ready           http.Handler
	Metrics         http.Handler
	APINotFound     http.Handler
}

func NewAgentOSDataPlaneHandler(handlers AgentOSDataPlaneHandlers) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/api/v1/agent/roles", handlers.Roles)
	mux.Handle("/api/v1/agent/tasks", handlers.Tasks)
	mux.Handle("/api/v1/agent/tasks/", handlers.TaskByID)
	mux.Handle("/api/v1/agent/files", handlers.Files)
	mux.Handle("/v1/models", handlers.Models)
	mux.Handle("/v1/chat/completions", handlers.ChatCompletions)
	mux.Handle("/livez", handlers.Live)
	mux.Handle("/readyz", handlers.Ready)
	mux.Handle("/metrics", handlers.Metrics)
	mux.Handle("/api/v1/agent", handlers.APINotFound)
	mux.Handle("/api/v1/agent/", handlers.APINotFound)
	mux.Handle("/v1/", handlers.APINotFound)
	return mux
}

type PrintfLogger interface {
	Printf(format string, values ...interface{})
}

func StartDedicatedAgentOSDataPlane(address string, handler http.Handler, logger PrintfLogger) (*http.Server, error) {
	address = strings.TrimSpace(address)
	if address == "" {
		return nil, nil
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return nil, fmt.Errorf("listen on AgentOS data-plane address %s: %w", address, err)
	}
	server := &http.Server{
		Addr:              address,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    1 << 20,
	}
	if logger != nil {
		logger.Printf("[AgentOS] dedicated data plane listening on %s", listener.Addr())
	}
	go func() {
		if serveErr := server.Serve(listener); serveErr != nil &&
			!errors.Is(serveErr, http.ErrServerClosed) && logger != nil {
			logger.Printf("[AgentOS] dedicated data plane stopped unexpectedly: %v", serveErr)
		}
	}()
	return server, nil
}
