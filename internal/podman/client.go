// Package podman is a thin shell-out client for reading `podman system df`
// state and running prune/remove operations. Unlike docker (which has an
// official Go SDK wired in), podman is driven via its CLI with JSON output:
// the host may not have podman at all, so the client detects availability and
// degrades to an "unreachable" EngineDF rather than erroring the whole view.
//
// The JSON→EngineDF mappers are pure and unit-tested; the exec orchestration
// is thin and untested, mirroring how internal/bridge treats its runner.
package podman

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/eike-hass/infra-mngmt/internal/system"
)

// defaultMachine is the conventional WSL distro a podman machine runs in; used
// for the footer copy when machine introspection isn't available.
const defaultMachine = "podman-machine-default"

// commandContext is the exec hook, overridable in tests.
var commandContext = exec.CommandContext

// Client shells out to the podman binary.
type Client struct {
	bin string
}

// New resolves the podman binary ($PATH, then Windows interop locations). It
// does not verify the engine is up — call DiskUsageDF, which sets Available.
func New() *Client {
	bin := "podman"
	if p, err := exec.LookPath("podman"); err == nil {
		bin = p
	} else {
		for _, cand := range []string{"/c/Windows/System32/podman.exe", "/usr/bin/podman", "/usr/local/bin/podman"} {
			if _, statErr := os.Stat(cand); statErr == nil {
				bin = cand
				break
			}
		}
	}
	return &Client{bin: bin}
}

func (c *Client) run(ctx context.Context, args ...string) ([]byte, error) {
	out, err := commandContext(ctx, c.bin, args...).Output()
	if err != nil {
		return nil, fmt.Errorf("podman %s: %w", strings.Join(args, " "), err)
	}
	return out, nil
}

// DiskUsageDF reads podman's images/containers/volumes and maps them into a
// system.EngineDF. Any failure to reach podman yields an unreachable EngineDF
// (Available=false) — the common case on hosts without podman installed.
func (c *Client) DiskUsageDF(ctx context.Context) system.EngineDF {
	df := system.EngineDF{Engine: system.EnginePodman, Distro: defaultMachine}

	ver, err := c.version(ctx)
	if err != nil {
		df.Available = false
		df.Err = err.Error()
		return df
	}
	df.Available = true
	df.Version = ver

	now := time.Now()
	imgRaw, ierr := c.run(ctx, "images", "--format", "json")
	ctrRaw, cerr := c.run(ctx, "ps", "-a", "--size", "--format", "json")
	volRaw, verr := c.run(ctx, "volume", "ls", "--format", "json")
	dfRaw, _ := c.run(ctx, "system", "df", "--format", "json") // best-effort: dedup rollup
	if ierr != nil || cerr != nil || verr != nil {
		// Engine answered version but a listing failed — surface what we can.
		df.Err = firstErr(ierr, cerr, verr).Error()
	}

	volRefs := collectVolumeRefs(ctrRaw)
	images := system.DFRow{Key: "images", Label: "Images", Prune: "podman image prune -a", Items: mapPodmanImages(imgRaw, now)}
	// Images share base layers, so summing per-item Size double-counts. Apply a
	// deduplicated rollup like the docker card does — preferring podman's own
	// `system df`, falling back to per-image unique size (Size−SharedSize).
	if sz, rc := imagesDedup(imgRaw, dfRaw); sz != nil {
		images.SizeOverride = sz
		images.ReclaimOverride = rc
	}
	df.Rows = []system.DFRow{
		images,
		{Key: "containers", Label: "Containers", Prune: "podman container prune", Items: mapPodmanContainers(ctrRaw, now)},
		{Key: "volumes", Label: "Local Volumes", Prune: "podman volume prune", Items: mapPodmanVolumes(volRaw, volRefs)},
	}
	return df
}

// imagesDedup returns deduplicated size/reclaimable overrides for the images
// row, or (nil, nil) to leave the item-summed rollup in place. It prefers
// podman's authoritative `system df` numbers; if those can't be parsed it falls
// back to summing per-image unique size (Size − SharedSize), which at least
// removes the shared-layer multiplication. Returns nil only when neither source
// is usable (e.g. an old podman with no SharedSize), preserving old behavior.
func imagesDedup(imgRaw, dfRaw []byte) (size, reclaim *int64) {
	if r, ok := parsePodmanSystemDF(dfRaw)["images"]; ok && r.Size > 0 {
		s, rc := r.Size, r.Reclaim
		return &s, &rc
	}
	var imgs []pmImage
	if len(imgRaw) == 0 || json.Unmarshal(imgRaw, &imgs) != nil {
		return nil, nil
	}
	var total, recl int64
	anyShared := false
	for _, im := range imgs {
		if im.SharedSize > 0 {
			anyShared = true
		}
		uniq := nonNeg(im.Size - im.SharedSize)
		total += uniq
		if im.Containers == 0 {
			recl += uniq
		}
	}
	if !anyShared {
		return nil, nil // no SharedSize data → unique-sum == full-sum, no gain
	}
	return &total, &recl
}

// pmDFRollup is one category's deduplicated byte totals from `podman system df`.
type pmDFRollup struct{ Size, Reclaim int64 }

// parsePodmanSystemDF parses `podman system df --format json` into per-category
// rollups. Tolerant of shape across podman versions: each entry is read as a
// loose map, Type is matched by substring, and Size/Reclaimable are accepted as
// either a raw byte number or a human string ("4.2GB", "2.1GB (50%)").
func parsePodmanSystemDF(raw []byte) map[string]pmDFRollup {
	out := map[string]pmDFRollup{}
	var entries []map[string]json.RawMessage
	if len(raw) == 0 || json.Unmarshal(raw, &entries) != nil {
		return out
	}
	for _, e := range entries {
		var typ string
		_ = json.Unmarshal(e["Type"], &typ)
		typ = strings.ToLower(typ)
		key := ""
		switch {
		case strings.Contains(typ, "image"):
			key = "images"
		case strings.Contains(typ, "container"):
			key = "containers"
		case strings.Contains(typ, "volume"):
			key = "volumes"
		default:
			continue
		}
		out[key] = pmDFRollup{Size: parseDFBytes(e["Size"]), Reclaim: parseDFBytes(e["Reclaimable"])}
	}
	return out
}

// parseDFBytes reads a df size field that may be a JSON number (bytes) or a
// human-formatted string.
func parseDFBytes(raw json.RawMessage) int64 {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return 0
	}
	if raw[0] == '"' {
		var s string
		if json.Unmarshal(raw, &s) == nil {
			return parseHumanSize(s)
		}
		return 0
	}
	var n float64
	if json.Unmarshal(raw, &n) == nil {
		return int64(n)
	}
	return 0
}

// parseHumanSize converts podman's HumanSize output (base-1000, e.g. "4.2GB",
// "512MB", "0B") to bytes. A trailing "(NN%)" or anything after a space is
// ignored. Returns 0 on anything unparseable.
func parseHumanSize(s string) int64 {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, " ("); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	j := 0
	for j < len(s) && (s[j] == '.' || (s[j] >= '0' && s[j] <= '9')) {
		j++
	}
	if j == 0 {
		return 0
	}
	num, err := strconv.ParseFloat(s[:j], 64)
	if err != nil {
		return 0
	}
	mult := 1.0
	switch unit := strings.ToUpper(strings.TrimSpace(s[j:])); {
	case strings.HasPrefix(unit, "K"):
		mult = 1e3
	case strings.HasPrefix(unit, "M"):
		mult = 1e6
	case strings.HasPrefix(unit, "G"):
		mult = 1e9
	case strings.HasPrefix(unit, "T"):
		mult = 1e12
	case strings.HasPrefix(unit, "P"):
		mult = 1e15
	}
	return int64(num * mult)
}

func firstErr(errs ...error) error {
	for _, e := range errs {
		if e != nil {
			return e
		}
	}
	return fmt.Errorf("unknown error")
}

// ── version ──────────────────────────────────────────────────────────────

type pmVersion struct {
	Client  struct{ Version string }
	Server  struct{ Version string }
	Version string
}

func (c *Client) version(ctx context.Context) (string, error) {
	out, err := c.run(ctx, "version", "--format", "json")
	if err != nil {
		return "", err
	}
	return parsePodmanVersion(out)
}

func parsePodmanVersion(b []byte) (string, error) {
	var v pmVersion
	if err := json.Unmarshal(b, &v); err != nil {
		return "", fmt.Errorf("parse podman version: %w", err)
	}
	switch {
	case v.Client.Version != "":
		return v.Client.Version, nil
	case v.Server.Version != "":
		return v.Server.Version, nil
	default:
		return v.Version, nil
	}
}

// ── JSON mappers (pure, tested) ──────────────────────────────────────────

type pmImage struct {
	ID         string   `json:"Id"`
	Names      []string `json:"Names"`
	RepoTags   []string `json:"RepoTags"`
	Size       int64    `json:"Size"`
	SharedSize int64    `json:"SharedSize"`
	Created    int64    `json:"Created"`
	Containers int      `json:"Containers"`
}

func mapPodmanImages(raw []byte, now time.Time) []system.DFItem {
	var imgs []pmImage
	if len(raw) == 0 || json.Unmarshal(raw, &imgs) != nil {
		return nil
	}
	out := make([]system.DFItem, 0, len(imgs))
	for _, im := range imgs {
		repo, tag := podmanRepoTag(im)
		out = append(out, system.DFItem{
			ID:         im.ID,
			Repo:       repo,
			Tag:        tag,
			Created:    system.HumanizeAge(time.Unix(im.Created, 0), now),
			Size:       nonNeg(im.Size),
			SharedSize: nonNeg(im.SharedSize),
			InUse:      im.Containers > 0,
		})
	}
	return out
}

// podmanRepoTag prefers RepoTags, falling back to Names (podman populates
// either depending on subcommand/version); a tagless image is "<none>".
func podmanRepoTag(im pmImage) (repo, tag string) {
	cand := ""
	if len(im.RepoTags) > 0 {
		cand = im.RepoTags[0]
	} else if len(im.Names) > 0 {
		cand = im.Names[0]
	}
	if cand == "" || cand == "<none>:<none>" {
		return "<none>", "<none>"
	}
	if i := strings.LastIndex(cand, ":"); i >= 0 {
		return cand[:i], cand[i+1:]
	}
	return cand, "latest"
}

type pmMount struct {
	Type string `json:"Type"`
	Name string `json:"Name"`
}

type pmContainer struct {
	ID     string    `json:"Id"`
	Names  []string  `json:"Names"`
	Image  string    `json:"Image"`
	State  string    `json:"State"`
	Status string    `json:"Status"`
	Mounts []pmMount `json:"Mounts"`
	Size   *struct {
		RwSize int64 `json:"rwSize"`
	} `json:"Size"`
	Created int64 `json:"Created"`
}

func mapPodmanContainers(raw []byte, now time.Time) []system.DFItem {
	ctrs := parsePodmanContainers(raw)
	if ctrs == nil {
		return nil
	}
	out := make([]system.DFItem, 0, len(ctrs))
	for _, ct := range ctrs {
		var size int64
		if ct.Size != nil {
			size = nonNeg(ct.Size.RwSize)
		}
		name := ""
		if len(ct.Names) > 0 {
			name = strings.TrimPrefix(ct.Names[0], "/")
		}
		out = append(out, system.DFItem{
			ID:      ct.ID,
			Name:    name,
			Image:   ct.Image,
			Status:  ct.Status,
			Created: system.HumanizeAge(time.Unix(ct.Created, 0), now),
			Size:    size,
			InUse:   strings.EqualFold(ct.State, "running"),
		})
	}
	return out
}

func parsePodmanContainers(raw []byte) []pmContainer {
	var ctrs []pmContainer
	if len(raw) == 0 || json.Unmarshal(raw, &ctrs) != nil {
		return nil
	}
	return ctrs
}

// collectVolumeRefs counts, per volume name, how many containers mount it —
// podman's volume listing has no ref-count of its own, so usage is derived
// from container mounts.
func collectVolumeRefs(containersRaw []byte) map[string]int64 {
	refs := map[string]int64{}
	for _, ct := range parsePodmanContainers(containersRaw) {
		for _, m := range ct.Mounts {
			if strings.EqualFold(m.Type, "volume") && m.Name != "" {
				refs[m.Name]++
			}
		}
	}
	return refs
}

type pmVolume struct {
	Name   string `json:"Name"`
	Driver string `json:"Driver"`
}

func mapPodmanVolumes(raw []byte, refs map[string]int64) []system.DFItem {
	var vols []pmVolume
	if len(raw) == 0 || json.Unmarshal(raw, &vols) != nil {
		return nil
	}
	out := make([]system.DFItem, 0, len(vols))
	for _, v := range vols {
		links := refs[v.Name]
		out = append(out, system.DFItem{
			Name:   v.Name,
			Driver: v.Driver,
			Links:  links,
			InUse:  links > 0,
		})
	}
	return out
}

func nonNeg(n int64) int64 {
	if n < 0 {
		return 0
	}
	return n
}

// ── Destructive operations (Phase 3) ─────────────────────────────────────

// PruneCategory prunes one category via the podman CLI. Podman's prune output
// isn't reliably machine-parseable for a byte total, so this returns only an
// error; the caller re-reads df to show the new state.
func (c *Client) PruneCategory(ctx context.Context, key string) error {
	var args []string
	switch key {
	case "images":
		args = []string{"image", "prune", "-a", "-f"}
	case "containers":
		args = []string{"container", "prune", "-f"}
	case "volumes":
		args = []string{"volume", "prune", "-f"}
	default:
		return fmt.Errorf("unknown prune category %q", key)
	}
	_, err := c.run(ctx, args...)
	return err
}

// PruneAll runs `podman system prune -a --volumes -f`.
func (c *Client) PruneAll(ctx context.Context) error {
	_, err := c.run(ctx, "system", "prune", "-a", "--volumes", "-f")
	return err
}

// SafePrune runs only the loss-free cleanup podman supports: dangling
// (untagged) images via `podman image prune -f` (no -a, so tagged images are
// kept). Podman has no build-cache concept, so that's the whole operation.
func (c *Client) SafePrune(ctx context.Context) error {
	_, err := c.run(ctx, "image", "prune", "-f")
	return err
}

// RemoveItem removes a single object by id/name.
func (c *Client) RemoveItem(ctx context.Context, key, id string) error {
	var args []string
	switch key {
	case "images":
		args = []string{"rmi", id}
	case "containers":
		args = []string{"rm", id}
	case "volumes":
		args = []string{"volume", "rm", id}
	default:
		return fmt.Errorf("unknown category %q", key)
	}
	_, err := c.run(ctx, args...)
	return err
}
