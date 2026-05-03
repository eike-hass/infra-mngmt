package source

import (
	"context"
	"errors"

	"github.com/eike-hass/infra-mngmt/internal/entity"
)

var ErrReadOnly = errors.New("source is read-only")
var ErrNotFound = errors.New("entity not found")

type ChangeEvent struct {
	Kind entity.Kind
	Name string
	Op   string // "create" | "update" | "delete"
}

// Source is the central abstraction: every place .claude/ state can live
// (host filesystem, Docker volume, in-container agent) implements this interface.
// Callers never need to know where data came from.
type Source interface {
	ID() string
	Scope() entity.Scope
	Entities(ctx context.Context) ([]entity.Entity, error)
	Read(ctx context.Context, kind entity.Kind, name string) ([]byte, error)
	Write(ctx context.Context, kind entity.Kind, name string, data []byte) error
	// Watch returns a channel of change events, or nil if live updates are not supported.
	Watch(ctx context.Context) (<-chan ChangeEvent, error)
}
