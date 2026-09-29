// Package database implements database access.
//
// It defines the Config interface it needs and imports no configuration
// library: the generated *DbConfig satisfies it through Go's structural typing.
package database

import (
	"context"
	"errors"
)

// Config is the database configuration. The generated *DbConfig satisfies it.
type Config interface {
	Host() string
	Port() int
	Name() string
	Password() string
	DSN() string
}

// DB is a minimal database stub.
type DB struct{ cfg Config }

// New returns a DB for cfg.
func New(cfg Config) *DB { return &DB{cfg: cfg} }

// Ping checks that the database is configured. This stub never connects.
func (db *DB) Ping(ctx context.Context) error {
	if db.cfg.DSN() == "" {
		return errors.New("database: empty DSN")
	}
	return ctx.Err()
}

// Migrate runs the schema migrations. This stub only calls Ping.
func (db *DB) Migrate(ctx context.Context) error {
	return db.Ping(ctx)
}
