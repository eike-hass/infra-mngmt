package containers

import "testing"

func TestValidateAccepts(t *testing.T) {
	f := File{Containers: []Container{
		{Name: "ident-browser"},
		{Name: "abc_123.thing"},
		{Name: "X"},
	}}
	if err := f.Validate(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateRejectsBadName(t *testing.T) {
	bad := []string{"", "-leading-dash", ".dot-start", "has space", "with/slash", "with;semi"}
	for _, n := range bad {
		f := File{Containers: []Container{{Name: n}}}
		if err := f.Validate(); err == nil {
			t.Errorf("expected error for name %q", n)
		}
	}
}

func TestValidateRejectsDuplicateCaseInsensitive(t *testing.T) {
	f := File{Containers: []Container{{Name: "ident-browser"}, {Name: "Ident-Browser"}}}
	if err := f.Validate(); err == nil {
		t.Error("expected duplicate-name error")
	}
}

func TestValidateAcceptsInlineLifecycle(t *testing.T) {
	f := File{Containers: []Container{
		{Name: "redis", Image: "redis:7"},
		{Name: "api", Image: "myapp:latest", DockerArgs: []string{"-p", "8080:8080", "--restart", "unless-stopped"}},
	}}
	if err := f.Validate(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateAcceptsComposeLifecycle(t *testing.T) {
	f := File{Containers: []Container{
		{Name: "stack", ComposeFile: "stacks/foo.yaml"},
	}}
	if err := f.Validate(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateRejectsMutuallyExclusiveLifecycles(t *testing.T) {
	f := File{Containers: []Container{
		{Name: "x", Image: "redis:7", ComposeFile: "foo.yaml"},
	}}
	if err := f.Validate(); err == nil {
		t.Error("expected error: image + compose_file should be exclusive")
	}
}

func TestValidateRejectsDockerArgsWithoutImage(t *testing.T) {
	f := File{Containers: []Container{
		{Name: "x", DockerArgs: []string{"-p", "80:80"}},
	}}
	if err := f.Validate(); err == nil {
		t.Error("expected error: docker_args without image")
	}
}

func TestValidateRejectsEmptyComposeFilePath(t *testing.T) {
	f := File{Containers: []Container{
		{Name: "x", ComposeFile: ""},
	}}
	// empty ComposeFile is fine (means external-only mode); the validator
	// should only reject paths that resolve to "." (e.g. a stray "./" entry).
	if err := f.Validate(); err != nil {
		t.Errorf("unexpected error for empty compose_file: %v", err)
	}

	f2 := File{Containers: []Container{
		{Name: "x", ComposeFile: "."},
	}}
	if err := f2.Validate(); err == nil {
		t.Error("expected error for compose_file: '.'")
	}
}

func TestValidateAcceptsMCPFSKind(t *testing.T) {
	f := File{Containers: []Container{
		{
			Name:        "workspace-vault",
			ComposeFile: "vaults/workspace-vault.yaml",
			Kind:        "mcp-fs",
			MCPFS:       &MCPFSConfig{Control: "http://workspace-vault:3002"},
		},
	}}
	if err := f.Validate(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateRejectsUnknownKind(t *testing.T) {
	f := File{Containers: []Container{
		{Name: "x", Kind: "made-up"},
	}}
	if err := f.Validate(); err == nil {
		t.Error("expected error for unknown kind")
	}
}

func TestValidateRejectsMCPFSWithoutAPI(t *testing.T) {
	f := File{Containers: []Container{
		{Name: "x", Kind: "mcp-fs"},
	}}
	if err := f.Validate(); err == nil {
		t.Error("expected error: mcp-fs kind requires api block")
	}

	f2 := File{Containers: []Container{
		{Name: "x", Kind: "mcp-fs", MCPFS: &MCPFSConfig{Control: ""}},
	}}
	if err := f2.Validate(); err == nil {
		t.Error("expected error: api.control must be non-empty")
	}
}

func TestValidateRejectsAPIWithoutMCPFSKind(t *testing.T) {
	f := File{Containers: []Container{
		{Name: "x", MCPFS: &MCPFSConfig{Control: "http://x"}},
	}}
	if err := f.Validate(); err == nil {
		t.Error("expected error: api block requires kind: mcp-fs")
	}
}
