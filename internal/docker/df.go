package docker

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/image"

	"github.com/eike-hass/infra-mngmt/internal/system"
)

// DiskUsageDF returns the engine's `system df -v` breakdown mapped into the
// shared system.EngineDF shape the System tab renders. The hosting-distro
// linkage is left to the caller (cosmetic footer copy); this read is purely
// about the engine's own objects.
func (c *Client) DiskUsageDF(ctx context.Context) (system.EngineDF, error) {
	if c == nil || c.cli == nil {
		return system.EngineDF{Engine: system.EngineDocker, Available: false}, fmt.Errorf("docker client not initialized")
	}
	du, err := c.cli.DiskUsage(ctx, types.DiskUsageOptions{})
	if err != nil {
		return system.EngineDF{Engine: system.EngineDocker, Available: false, Err: err.Error()}, err
	}
	ver := ""
	if v, verr := c.cli.ServerVersion(ctx); verr == nil {
		ver = v.Version
	}
	df := mapDiskUsage(du, "", time.Now())
	df.Version = ver
	return df, nil
}

// mapDiskUsage converts an SDK DiskUsage into a system.EngineDF. Pure (now is
// injected) so the field/in-use mapping is unit-tested without a daemon.
func mapDiskUsage(du types.DiskUsage, distro string, now time.Time) system.EngineDF {
	df := system.EngineDF{Engine: system.EngineDocker, Available: true, Distro: distro}

	// Images — in use when at least one container references the image.
	images := system.DFRow{Key: "images", Label: "Images", Prune: "docker image prune -a"}
	for _, im := range du.Images {
		if im == nil {
			continue
		}
		repo, tag := splitRepoTag(im.RepoTags)
		images.Items = append(images.Items, system.DFItem{
			ID:         im.ID,
			Repo:       repo,
			Tag:        tag,
			Created:    system.HumanizeAge(time.Unix(im.Created, 0), now),
			Size:       im.Size,
			SharedSize: max(0, im.SharedSize),
			InUse:      im.Containers > 0,
		})
	}

	// Containers — in use when running.
	ctrs := system.DFRow{Key: "containers", Label: "Containers", Prune: "docker container prune"}
	for _, ct := range du.Containers {
		if ct == nil {
			continue
		}
		ctrs.Items = append(ctrs.Items, system.DFItem{
			ID:      ct.ID,
			Name:    containerName(ct.Names),
			Image:   ct.Image,
			Status:  ct.Status,
			Created: system.HumanizeAge(time.Unix(ct.Created, 0), now),
			Size:    ct.SizeRw,
			InUse:   strings.EqualFold(ct.State, "running"),
		})
	}

	// Local volumes — in use when ref-counted by a container.
	vols := system.DFRow{Key: "volumes", Label: "Local Volumes", Prune: "docker volume prune -a"}
	for _, vol := range du.Volumes {
		if vol == nil {
			continue
		}
		var size, links int64
		if vol.UsageData != nil {
			if vol.UsageData.Size > 0 {
				size = vol.UsageData.Size
			}
			if vol.UsageData.RefCount > 0 {
				links = vol.UsageData.RefCount
			}
		}
		vols.Items = append(vols.Items, system.DFItem{
			Name:   vol.Name,
			Driver: vol.Driver,
			Links:  links,
			Size:   size,
			InUse:  links > 0,
		})
	}

	// Build cache — in use per the record's own flag.
	bc := system.DFRow{Key: "buildcache", Label: "Build Cache", Prune: "docker builder prune -a"}
	for _, cr := range du.BuildCache {
		if cr == nil {
			continue
		}
		bc.Items = append(bc.Items, system.DFItem{
			ID:      cr.ID,
			Type:    cr.Type,
			Created: system.HumanizeAge(cr.CreatedAt, now),
			Size:    cr.Size,
			InUse:   cr.InUse,
		})
	}

	// Images need deduplicated rollups: each image's Size includes layers shared
	// with other images, so summing per-item Size double-counts. Match docker's
	// own `system df`: total = daemon LayersSize; reclaimable = LayersSize minus
	// the unique size of images still referenced by a container.
	if du.LayersSize > 0 {
		total := du.LayersSize
		var usedByActive int64
		for _, im := range du.Images {
			if im == nil || im.Containers <= 0 || im.Size < 0 || im.SharedSize < 0 {
				continue
			}
			usedByActive += im.Size - im.SharedSize
		}
		reclaim := total - usedByActive
		if reclaim < 0 {
			reclaim = 0
		}
		images.SizeOverride = &total
		images.ReclaimOverride = &reclaim
	}

	df.Rows = []system.DFRow{images, ctrs, vols, bc}
	return df
}

// splitRepoTag extracts the display repository and tag from an image's
// RepoTags. A dangling image (no tags, or the literal "<none>:<none>") renders
// as "<none>" / "<none>".
func splitRepoTag(repoTags []string) (repo, tag string) {
	if len(repoTags) == 0 || repoTags[0] == "<none>:<none>" || repoTags[0] == "" {
		return "<none>", "<none>"
	}
	rt := repoTags[0]
	if i := strings.LastIndex(rt, ":"); i >= 0 {
		return rt[:i], rt[i+1:]
	}
	return rt, "latest"
}

// containerName returns the primary container name with Docker's leading slash
// stripped.
func containerName(names []string) string {
	if len(names) == 0 {
		return ""
	}
	return strings.TrimPrefix(names[0], "/")
}

// ── Destructive operations (Phase 2) ─────────────────────────────────────────

// PruneCategory prunes one df category and returns the bytes reclaimed. key is
// one of "images" | "containers" | "volumes" | "buildcache".
func (c *Client) PruneCategory(ctx context.Context, key string) (uint64, error) {
	if c == nil || c.cli == nil {
		return 0, fmt.Errorf("docker client not initialized")
	}
	switch key {
	case "images":
		// dangling=false ⇒ remove all unused images, matching `image prune -a`.
		rep, err := c.cli.ImagesPrune(ctx, filters.NewArgs(filters.Arg("dangling", "false")))
		return rep.SpaceReclaimed, err
	case "containers":
		rep, err := c.cli.ContainersPrune(ctx, filters.Args{})
		return rep.SpaceReclaimed, err
	case "volumes":
		// all=true ⇒ include named unused volumes, matching `volume prune -a`
		// and our reclaimable tally (which counts every not-in-use volume).
		rep, err := c.cli.VolumesPrune(ctx, filters.NewArgs(filters.Arg("all", "true")))
		return rep.SpaceReclaimed, err
	case "buildcache":
		rep, err := c.cli.BuildCachePrune(ctx, types.BuildCachePruneOptions{All: true})
		if rep == nil {
			return 0, err
		}
		return rep.SpaceReclaimed, err
	default:
		return 0, fmt.Errorf("unknown prune category %q", key)
	}
}

// SafePrune runs only the loss-free cleanups: dangling (untagged) images and
// build cache. Unlike PruneCategory("images") it does NOT pass dangling=false,
// so tagged-but-unused images are kept; volumes and stopped containers are not
// touched at all. Returns total bytes reclaimed.
func (c *Client) SafePrune(ctx context.Context) (uint64, error) {
	if c == nil || c.cli == nil {
		return 0, fmt.Errorf("docker client not initialized")
	}
	var total uint64
	var firstErr error
	// Empty filter set ⇒ dangling images only (the gentle `docker image prune`).
	if rep, err := c.cli.ImagesPrune(ctx, filters.NewArgs()); err != nil {
		firstErr = err
	} else {
		total += rep.SpaceReclaimed
	}
	if rep, err := c.cli.BuildCachePrune(ctx, types.BuildCachePruneOptions{All: true}); err != nil {
		if firstErr == nil {
			firstErr = err
		}
	} else if rep != nil {
		total += rep.SpaceReclaimed
	}
	return total, firstErr
}

// PruneAll prunes every category (equivalent to `docker system prune -a
// --volumes`) and returns the total bytes reclaimed.
func (c *Client) PruneAll(ctx context.Context) (uint64, error) {
	var total uint64
	var firstErr error
	for _, key := range []string{"containers", "images", "volumes", "buildcache"} {
		n, err := c.PruneCategory(ctx, key)
		total += n
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return total, firstErr
}

// RemoveItem removes a single not-in-use object from a category. Build-cache
// records cannot be removed individually (the engine API only exposes a
// whole-cache prune) — callers must use PruneCategory("buildcache") instead.
func (c *Client) RemoveItem(ctx context.Context, key, id string) error {
	if c == nil || c.cli == nil {
		return fmt.Errorf("docker client not initialized")
	}
	switch key {
	case "images":
		_, err := c.cli.ImageRemove(ctx, id, image.RemoveOptions{PruneChildren: true})
		return err
	case "containers":
		return c.cli.ContainerRemove(ctx, id, container.RemoveOptions{})
	case "volumes":
		return c.cli.VolumeRemove(ctx, id, false)
	case "buildcache":
		return fmt.Errorf("build cache cannot be removed per-item; prune the category")
	default:
		return fmt.Errorf("unknown category %q", key)
	}
}
