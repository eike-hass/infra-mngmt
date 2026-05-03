package docker

// Standard devcontainer labels — set automatically by VS Code and the
// devcontainer CLI on every container they create. No manual configuration
// needed; infra-mngmt uses these for auto-discovery.
const (
	LabelDevcontainerLocalFolder = "devcontainer.local_folder" // host workspace path
	LabelDevcontainerConfigFile  = "devcontainer.config_file"  // path to devcontainer.json

	// LabelDevcontainerLocalFolderLegacy is a typo variant emitted by some
	// older VS Code releases ("devcontaine" missing the trailing 'r').
	// We check both so discovery works regardless of VS Code version.
	LabelDevcontainerLocalFolderLegacy = "devcontaine.local_folder"
)

// Explicit opt-in labels for containers not created by VS Code (e.g. plain
// docker run). claude.managed=true is the only required one; the rest are
// optional overrides.
const (
	LabelManaged     = "claude.managed"       // "true" → include this container
	LabelConfigVol   = "claude.config.volume" // override: volume name for ~/.claude
	LabelDataVol     = "claude.data.volume"   // volume name for ~/.local/share/claude
	LabelProjectRoot = "claude.project.root"  // override: host path of project root
)
