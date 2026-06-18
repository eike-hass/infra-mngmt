package docker

import (
	"testing"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/volume"
)

func TestMapDiskUsage(t *testing.T) {
	now := time.Date(2026, 6, 11, 12, 0, 0, 0, time.UTC)
	threeWeeks := now.Add(-21 * 24 * time.Hour).Unix()

	du := types.DiskUsage{
		Images: []*image.Summary{
			{ID: "sha256:9f2b", RepoTags: []string{"node:20-slim"}, Size: 380, Created: threeWeeks, Containers: 1},
			{ID: "sha256:b219", RepoTags: []string{"<none>:<none>"}, Size: 2100, Created: threeWeeks, Containers: 0},
			{ID: "sha256:f6a0", RepoTags: []string{"ml-tools-dev:latest"}, Size: 6400, Created: threeWeeks, Containers: 0},
		},
		Containers: []*types.Container{
			{ID: "abc123", Names: []string{"/infra-mngmt-dev"}, Image: "node:20-slim", Status: "Up 2 hours", State: "running", SizeRw: 120},
			{ID: "e4f5a6", Names: []string{"/grafana-od"}, Image: "grafana:11", Status: "Exited (0) 3 days ago", State: "exited", SizeRw: 41},
		},
		Volumes: []*volume.Volume{
			{Name: "od_pgdata", Driver: "local", UsageData: &volume.UsageData{RefCount: 1, Size: 4100}},
			{Name: "ml_datasets", Driver: "local", UsageData: &volume.UsageData{RefCount: 0, Size: 2000}},
			{Name: "unknown_size", Driver: "local", UsageData: &volume.UsageData{RefCount: 0, Size: -1}},
		},
		BuildCache: []*types.BuildCache{
			{ID: "qx7k2m", Type: "regular", Size: 4800, InUse: false, CreatedAt: now.Add(-5 * 24 * time.Hour)},
			{ID: "p3n9vt", Type: "regular", Size: 3200, InUse: true},
		},
	}

	df := mapDiskUsage(du, "Ubuntu-24.04", now)

	if !df.Available || df.Distro != "Ubuntu-24.04" {
		t.Fatalf("df header = %+v", df)
	}
	if len(df.Rows) != 4 {
		t.Fatalf("got %d rows, want 4 (images, containers, volumes, buildcache)", len(df.Rows))
	}

	imgs, _ := df.Row("images")
	if imgs.Total() != 3 || imgs.Active() != 1 {
		t.Errorf("images total/active = %d/%d, want 3/1", imgs.Total(), imgs.Active())
	}
	if imgs.Reclaimable() != 2100+6400 {
		t.Errorf("images reclaimable = %d, want %d", imgs.Reclaimable(), 2100+6400)
	}
	// Dangling image renders repo/tag as <none>; created humanized.
	if imgs.Items[1].Repo != "<none>" || imgs.Items[1].Tag != "<none>" {
		t.Errorf("dangling image repo:tag = %s:%s, want <none>:<none>", imgs.Items[1].Repo, imgs.Items[1].Tag)
	}
	if imgs.Items[0].Created != "3 weeks ago" {
		t.Errorf("image created = %q, want '3 weeks ago'", imgs.Items[0].Created)
	}

	ctrs, _ := df.Row("containers")
	if ctrs.Active() != 1 || ctrs.Reclaimable() != 41 {
		t.Errorf("containers active/reclaimable = %d/%d, want 1/41", ctrs.Active(), ctrs.Reclaimable())
	}
	if ctrs.Items[0].Name != "infra-mngmt-dev" {
		t.Errorf("container name = %q, want leading slash stripped", ctrs.Items[0].Name)
	}

	vols, _ := df.Row("volumes")
	if vols.Active() != 1 || vols.Reclaimable() != 2000 {
		t.Errorf("volumes active/reclaimable = %d/%d, want 1/2000", vols.Active(), vols.Reclaimable())
	}
	// A -1 (unknown) size is clamped to 0, not propagated as negative.
	if vols.Items[2].Size != 0 {
		t.Errorf("unknown volume size = %d, want 0", vols.Items[2].Size)
	}

	bc, _ := df.Row("buildcache")
	if bc.Active() != 1 || bc.Reclaimable() != 4800 {
		t.Errorf("buildcache active/reclaimable = %d/%d, want 1/4800", bc.Active(), bc.Reclaimable())
	}
}

func TestMapDiskUsageImagesDedup(t *testing.T) {
	// Shared layers: summing per-item Size (800+900=1700) double-counts the
	// 600 shared base layer. Docker reports the deduplicated LayersSize (1000)
	// and reclaimable = LayersSize − unique-size-of-active-images.
	du := types.DiskUsage{
		LayersSize: 1000,
		Images: []*image.Summary{
			{ID: "active", RepoTags: []string{"node:20"}, Size: 800, SharedSize: 600, Containers: 1}, // unique 200
			{ID: "idle", RepoTags: []string{"old:1"}, Size: 900, SharedSize: 600, Containers: 0},
		},
	}
	df := mapDiskUsage(du, "d", time.Now())
	imgs, _ := df.Row("images")

	if got := imgs.Size(); got != 1000 {
		t.Errorf("images Size() = %d, want 1000 (deduplicated LayersSize, not 1700)", got)
	}
	if got := imgs.Reclaimable(); got != 800 {
		t.Errorf("images Reclaimable() = %d, want 800 (1000 − 200 unique active)", got)
	}
	// Per-item sizes stay full (as docker system df -v shows them).
	if imgs.Items[0].Size != 800 {
		t.Errorf("per-item size should stay full, got %d", imgs.Items[0].Size)
	}
}

func TestMapDiskUsageSkipsNils(t *testing.T) {
	du := types.DiskUsage{
		Images:     []*image.Summary{nil},
		Containers: []*types.Container{nil},
		Volumes:    []*volume.Volume{nil},
		BuildCache: []*types.BuildCache{nil},
	}
	df := mapDiskUsage(du, "d", time.Now())
	for _, r := range df.Rows {
		if r.Total() != 0 {
			t.Errorf("row %s should skip nil entries, got %d items", r.Key, r.Total())
		}
	}
}

func TestSplitRepoTag(t *testing.T) {
	cases := []struct {
		in        []string
		repo, tag string
	}{
		{[]string{"node:20-slim"}, "node", "20-slim"},
		{[]string{"grafana/grafana:11.1.0"}, "grafana/grafana", "11.1.0"},
		{[]string{"registry.io:5000/img:tag"}, "registry.io:5000/img", "tag"},
		{[]string{"<none>:<none>"}, "<none>", "<none>"},
		{[]string{}, "<none>", "<none>"},
		{[]string{"barerepo"}, "barerepo", "latest"},
	}
	for _, c := range cases {
		repo, tag := splitRepoTag(c.in)
		if repo != c.repo || tag != c.tag {
			t.Errorf("splitRepoTag(%v) = %s:%s, want %s:%s", c.in, repo, tag, c.repo, c.tag)
		}
	}
}

func TestContainerName(t *testing.T) {
	if got := containerName([]string{"/foo"}); got != "foo" {
		t.Errorf("containerName(/foo) = %q, want foo", got)
	}
	if got := containerName(nil); got != "" {
		t.Errorf("containerName(nil) = %q, want empty", got)
	}
}
