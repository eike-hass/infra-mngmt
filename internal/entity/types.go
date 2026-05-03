package entity

type Kind string

const (
	KindCommand   Kind = "command"
	KindAgent     Kind = "agent"
	KindSkill     Kind = "skill"
	KindMemory    Kind = "memory"
	KindMCPServer Kind = "mcp_server"
	KindHook      Kind = "hook"
	KindClaudeMD  Kind = "claude_md"
)

func (k Kind) String() string { return string(k) }

type Scope struct {
	Global  bool
	Project string // absolute path to repo root; empty if global
}

func GlobalScope() Scope   { return Scope{Global: true} }
func ProjectScope(root string) Scope { return Scope{Project: root} }

func (s Scope) String() string {
	if s.Global {
		return "global"
	}
	return "project:" + s.Project
}

func (s Scope) Label() string {
	if s.Global {
		return "global"
	}
	return "project"
}

type Entity struct {
	ID     string // "<sourceID>:<kind>:<name>"
	Kind   Kind
	Name   string
	Scope  Scope
	Source string // EntitySource.ID()
	Path   string // absolute path to entity file or dir
	// Attrs holds kind-specific metadata. For KindMCPServer: "type", "url",
	// "command", "args". Populated by sources that can parse config; may be nil.
	Attrs map[string]string
}
