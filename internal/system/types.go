// Package system models the data behind the System tab: WSL2 + Windows disk
// footprint and the `docker`/`podman system df` breakdown, plus the destructive
// operations (prune, remove) that act on them.
//
// The types here are the contract the web layer renders and the docker/podman
// clients produce. Aggregates (total/active/size/reclaimable counts, the
// compact gap) are DERIVED methods, never stored — so they stay truthful as
// items are pruned. This mirrors the design prototype's `aggRow`/`compactGap`
// helpers exactly; see docs/proposals and the handoff README.
package system

import (
	"fmt"
	"strings"
)

// Engine identifies a container engine.
type Engine string

const (
	EngineDocker Engine = "docker"
	EnginePodman Engine = "podman"
)

// Snapshot is the full payload behind the System tab. Each field is read
// independently and degrades on its own: a failed read sets the relevant Err
// rather than failing the whole view.
type Snapshot struct {
	Disk   DiskInfo
	Docker EngineDF
	Podman EngineDF
}

// ── Disk ─────────────────────────────────────────────────────────────────────

// DiskInfo is the Windows host drive plus every WSL2 distro's footprint.
type DiskInfo struct {
	Windows WindowsDrive
	Distros []Distro
	// Err, when non-empty, means the disk read failed; the card renders an
	// error state instead of the hero strip + table.
	Err string
}

// WindowsDrive is the Windows host drive the vhdx files grow into (usually C:).
type WindowsDrive struct {
	Drive string // e.g. "C:"
	Total int64  // bytes
	Used  int64  // bytes
}

// Free is the remaining space on the drive, floored at zero.
func (w WindowsDrive) Free() int64 { return max(0, w.Total-w.Used) }

// Low reports whether free space has dropped under 15% of the drive — the
// threshold at which the disk bar and "free" figure turn orange.
func (w WindowsDrive) Low() bool {
	return w.Total > 0 && float64(w.Free())/float64(w.Total) < 0.15
}

// Distro is one WSL2 distribution: its in-guest filesystem usage and the size
// of the backing ext4.vhdx on the Windows host.
type Distro struct {
	Name     string
	Version  int    // WSL version (1 or 2)
	Default  bool   // the `*`-marked default distro
	State    string // "Running" | "Stopped"
	Engine   string // "docker" | "podman" | "" — which engine this distro hosts
	FsTotal  int64  // bytes, df total inside the distro
	FsUsed   int64  // bytes, df used inside the distro
	FsKnown  bool   // false for stopped distros we don't boot just to measure
	VhdxSize int64  // bytes, size of the ext4.vhdx file on the Windows host
	VhdxPath string
}

// Running reports whether the distro is currently up.
func (d Distro) Running() bool { return d.State == "Running" }

// SubLabel is the distro's secondary identifier line, joining its WSL version,
// default marker, and hosted engine with " · " (e.g. "WSL2 · default · docker
// host"). Empty parts are dropped.
func (d Distro) SubLabel() string {
	parts := []string{fmt.Sprintf("WSL%d", d.Version)}
	if d.Default {
		parts = append(parts, "default")
	}
	if d.Engine != "" {
		parts = append(parts, d.Engine+" host")
	}
	return strings.Join(parts, " · ")
}

// FsFree is remaining space inside the distro filesystem, floored at zero.
func (d Distro) FsFree() int64 { return max(0, d.FsTotal-d.FsUsed) }

// FsLow reports whether the in-distro filesystem is under 15% free.
func (d Distro) FsLow() bool {
	return d.FsTotal > 0 && float64(d.FsFree())/float64(d.FsTotal) < 0.15
}

// CompactGap is the space a `compact` would return to the Windows drive:
// vhdxSize − fsUsed, floored at zero. The vhdx only grows; pruning lowers
// fsUsed (growing this gap), and only compacting shrinks the vhdx back down.
//
// Returns 0 when in-guest usage is unknown (FsKnown=false, e.g. a stopped
// distro we don't boot just to measure) — without fsUsed the gap can't be
// computed, and assuming 0 used would falsely report the whole vhdx as
// reclaimable.
func (d Distro) CompactGap() int64 {
	if !d.FsKnown {
		return 0
	}
	return max(0, d.VhdxSize-d.FsUsed)
}

// HasCompactGap reports whether this distro has anything to reclaim by
// compacting (boolean helper for templates).
func (d Distro) HasCompactGap() bool { return d.CompactGap() > 0 }

// Footprint is the sum of every distro's vhdx size on the Windows host.
func (d DiskInfo) Footprint() int64 {
	var n int64
	for _, x := range d.Distros {
		n += x.VhdxSize
	}
	return n
}

// Reclaimable is the total space all distros could return to the drive by
// compacting (sum of compact gaps).
func (d DiskInfo) Reclaimable() int64 {
	var n int64
	for _, x := range d.Distros {
		n += x.CompactGap()
	}
	return n
}

// HasReclaimable reports whether compacting would return any space (boolean
// helper for templates).
func (d DiskInfo) HasReclaimable() bool { return d.Reclaimable() > 0 }

// ── Engine df ──────────────────────────────────────────────────────────────

// EngineDF is one engine's `system df` breakdown. When Available is false the
// card renders the unreachable state (a "start <engine>" prompt) instead of
// the table.
type EngineDF struct {
	Engine    Engine
	Available bool
	Version   string
	Distro    string // the distro hosting this engine
	Rows      []DFRow
	// Err is a human-readable note about why a read failed; informational only
	// (Available already gates the unreachable UI).
	Err string
}

// DFRow is one df category (Images, Containers, Local Volumes, Build Cache).
type DFRow struct {
	Key   string // "images" | "containers" | "volumes" | "buildcache"
	Label string // display label, e.g. "Local Volumes"
	Prune string // the exact CLI command shown in the confirm modal
	Items []DFItem

	// SizeOverride / ReclaimOverride, when non-nil, replace the item-summed
	// rollup. Docker images need this: shared layers make naive per-item
	// summing double-count (each image's Size includes layers shared with
	// others), so docker's own `system df` dedups via the daemon's LayersSize.
	// The per-item Size shown in the detail table stays the full image size,
	// exactly as `docker system df -v` presents it.
	SizeOverride    *int64
	ReclaimOverride *int64
}

// DFItem is a single object within a category. Only the fields relevant to its
// category are populated (an image uses Repo/Tag/ID; a volume uses Name/Driver/
// Links; a container uses Name/Image/Status; build cache uses ID/Type).
type DFItem struct {
	ID      string
	Name    string
	Repo    string
	Tag     string
	Image   string
	Status  string
	Driver  string
	Links   int64
	Type    string
	Created string // humanized age, e.g. "3 weeks ago"
	Size    int64
	// SharedSize is the bytes of this image's layers shared with other images
	// (images only; 0 otherwise). Size−SharedSize is the unique footprint that
	// removing this image alone would actually free.
	SharedSize int64
	InUse      bool
}

// Key returns the stable identifier for an item: its ID, or its Name for
// objects keyed by name (volumes).
func (it DFItem) Key() string {
	if it.ID != "" {
		return it.ID
	}
	return it.Name
}

// Total is the number of objects in the category.
func (r DFRow) Total() int { return len(r.Items) }

// Active is the number of in-use objects in the category.
func (r DFRow) Active() int {
	n := 0
	for _, it := range r.Items {
		if it.InUse {
			n++
		}
	}
	return n
}

// Size is the total bytes occupied by the category — the deduplicated override
// when set (docker images), else the sum of item sizes.
func (r DFRow) Size() int64 {
	if r.SizeOverride != nil {
		return *r.SizeOverride
	}
	var n int64
	for _, it := range r.Items {
		n += it.Size
	}
	return n
}

// Reclaimable is the bytes a prune of this category would free — the
// deduplicated override when set (docker images), else the sum of not-in-use
// item sizes.
func (r DFRow) Reclaimable() int64 {
	if r.ReclaimOverride != nil {
		return *r.ReclaimOverride
	}
	var n int64
	for _, it := range r.Items {
		if !it.InUse {
			n += it.Size
		}
	}
	return n
}

// HasReclaim reports whether the category has any reclaimable bytes. A boolean
// helper so templates avoid int64-vs-int comparison (which html/template's
// comparison funcs reject).
func (r DFRow) HasReclaim() bool { return r.Reclaimable() > 0 }

// TotalSize sums every category's size.
func (df EngineDF) TotalSize() int64 {
	var n int64
	for _, r := range df.Rows {
		n += r.Size()
	}
	return n
}

// TotalReclaimable sums every category's reclaimable bytes.
func (df EngineDF) TotalReclaimable() int64 {
	var n int64
	for _, r := range df.Rows {
		n += r.Reclaimable()
	}
	return n
}

// HasReclaim reports whether any category has reclaimable bytes (drives the
// "prune all" button / "nothing to reclaim" copy).
func (df EngineDF) HasReclaim() bool { return df.TotalReclaimable() > 0 }

// DanglingImages counts not-in-use untagged ("<none>") images — the leftovers a
// `image prune` (without -a) removes. Safe to delete: nothing references them.
func (df EngineDF) DanglingImages() int {
	n := 0
	if r, ok := df.Row("images"); ok {
		for _, it := range r.Items {
			if !it.InUse && it.Repo == "<none>" {
				n++
			}
		}
	}
	return n
}

// HasSafeReclaim reports whether a safe prune would free anything (dangling
// images or reclaimable build cache).
func (df EngineDF) HasSafeReclaim() bool {
	if r, ok := df.Row("buildcache"); ok && r.Reclaimable() > 0 {
		return true
	}
	return df.DanglingImages() > 0
}

// SafeReclaim estimates the bytes a safe prune would free: reclaimable build
// cache plus the dangling images' UNIQUE layers (Size−SharedSize — the part
// removing them actually frees, not the shared base layers tagged images keep).
// The dangling portion is capped at the images' deduplicated reclaimable so the
// figure can never exceed "prune all" even if an engine reports SharedSize
// poorly. It's a conservative estimate (real freed bytes — in the post-op
// toast — may be a little higher when layers are shared only among dangling
// images), never an inflated one. Returns 0 when nothing can be safely freed.
func (df EngineDF) SafeReclaim() int64 {
	var bc int64
	if r, ok := df.Row("buildcache"); ok {
		bc = r.Reclaimable()
	}
	var danglingUnique int64
	if r, ok := df.Row("images"); ok {
		for _, it := range r.Items {
			if !it.InUse && it.Repo == "<none>" {
				danglingUnique += max(0, it.Size-it.SharedSize)
			}
		}
		if lim := r.Reclaimable(); danglingUnique > lim {
			danglingUnique = lim // never exceed all-unused dedup → stays ≤ prune-all
		}
	}
	return bc + danglingUnique
}

// Row returns the category row with the given key, or false if absent.
func (df EngineDF) Row(key string) (DFRow, bool) {
	for _, r := range df.Rows {
		if r.Key == key {
			return r, true
		}
	}
	return DFRow{}, false
}
