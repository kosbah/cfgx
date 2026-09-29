// Package server implements the HTTP server.
//
// It defines the Config interfaces it needs and imports no configuration
// library: the generated *ServeConfig and *DbConfig satisfy them through Go's
// structural typing.
package server

import "context"

// Config is the server configuration. The generated *ServeConfig satisfies it.
type Config interface {
	Port() int
	TLSEnabled() bool
	TLSCertPath() string
}

// DBConfig is the database configuration the server uses. The generated
// *DbConfig satisfies it.
type DBConfig interface {
	Host() string
	DSN() string
}

// Server is a minimal HTTP server stub.
type Server struct {
	cfg   Config
	dbCfg DBConfig
}

// New returns a Server for cfg that talks to the database described by db.
func New(cfg Config, db DBConfig) *Server {
	return &Server{cfg: cfg, dbCfg: db}
}

// Run starts the server. This stub returns immediately.
func (s *Server) Run(ctx context.Context) error {
	return ctx.Err()
}
