package source

import (
	"context"
	"errors"

	"github.com/eike-hass/infra-mngmt/internal/entity"
)

var ErrReadOnly = errors.New("source is read-only")
var ErrNotFound = errors.New("entity not found")
var ErrConflict = errors.New("entity already exists at target")

type ChangeEvent struct {
	Kind entity.Kind
	Name string
	Op   string // "create" | "update" | "delete"
}

// EntityFile is one file within an entity's payload. For single-file kinds
// (commands, agents, memory, claude_md) a payload is a single EntityFile with
// RelPath == "". For multi-file kinds (skill) RelPath is the path relative to
// the entity root (e.g. "SKILL.md", "scripts/foo.sh"). For settings-backed
// kinds (mcp_server, hook) the payload is a single EntityFile with RelPath ==
// "" whose Data is the JSON block to splice into the target's settings file.
type EntityFile struct {
	RelPath string
	Data    []byte
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

	// ReadFiles returns the full file set for an entity. Used by promote/copy.
	ReadFiles(ctx context.Context, kind entity.Kind, name string) ([]EntityFile, error)
	// WriteFiles writes a file set into this source. ErrReadOnly if not writable.
	WriteFiles(ctx context.Context, kind entity.Kind, name string, files []EntityFile) error
	// Clear removes the entity from this source. Returns ErrNotFound if it
	// doesn't exist (callers may ignore that), ErrReadOnly if writes aren't
	// supported. Used by promote's mirror=true mode to wipe stale files at the
	// destination before WriteFiles re-populates the entity (relevant for
	// multi-file kinds like skill where overwriting overlapping files would
	// otherwise leave removed files behind).
	Clear(ctx context.Context, kind entity.Kind, name string) error
	// Has reports whether (kind, name) currently exists in this source.
	Has(ctx context.Context, kind entity.Kind, name string) (bool, error)
	// Writable reports whether WriteFiles can succeed for this source. Static
	// property (does not depend on the entity); used by the promote picker to
	// disable read-only targets up-front instead of failing on click.
	Writable() bool
}
