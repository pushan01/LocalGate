package server

import (
	"context"
	"net"
	"net/http"
	"time"
)

type DeploymentOptions struct {
	Bind    string
	Handler http.Handler
}

type DeploymentServer struct {
	opts DeploymentOptions
	srv  *http.Server
	ln   net.Listener
}

func NewDeployment(opts DeploymentOptions) *DeploymentServer {
	handler := opts.Handler
	if handler == nil {
		handler = http.NotFoundHandler()
	}
	return &DeploymentServer{
		opts: opts,
		srv: &http.Server{
			Addr:              opts.Bind,
			Handler:           handler,
			ReadHeaderTimeout: 5 * time.Second,
			IdleTimeout:       60 * time.Second,
		},
	}
}

func (s *DeploymentServer) Start() error {
	ln, err := net.Listen("tcp", s.opts.Bind)
	if err != nil {
		return err
	}
	s.ln = ln
	return s.srv.Serve(ln)
}

func (s *DeploymentServer) Shutdown(ctx context.Context) error {
	return s.srv.Shutdown(ctx)
}
