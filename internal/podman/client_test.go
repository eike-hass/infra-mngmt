package podman

import (
	"strconv"
	"testing"
	"time"
)

func TestMapPodmanImages(t *testing.T) {
	now := time.Date(2026, 6, 11, 12, 0, 0, 0, time.UTC)
	created := now.Add(-30 * 24 * time.Hour).Unix()
	raw := []byte(`[
	  {"Id":"2af1","Names":["docker.io/library/alpine:3.20"],"Size":12000000,"Created":` + itoa(created) + `,"Containers":1},
	  {"Id":"08a9","RepoTags":["<none>:<none>"],"Size":1300000000,"Created":` + itoa(created) + `,"Containers":0},
	  {"Id":"1bca","Names":["localhost/ml-eval:dev"],"Size":3800000000,"Created":` + itoa(created) + `,"Containers":0}
	]`)
	items := mapPodmanImages(raw, now)
	if len(items) != 3 {
		t.Fatalf("got %d images, want 3", len(items))
	}
	if items[0].Repo != "docker.io/library/alpine" || items[0].Tag != "3.20" || !items[0].InUse {
		t.Errorf("image[0] = %+v; want alpine:3.20 in-use", items[0])
	}
	if items[1].Repo != "<none>" || items[1].InUse {
		t.Errorf("image[1] = %+v; want dangling <none>, not in-use", items[1])
	}
	if items[2].InUse {
		t.Errorf("image[2] (Containers=0) should not be in use")
	}
}

func TestMapPodmanContainers(t *testing.T) {
	now := time.Date(2026, 6, 11, 12, 0, 0, 0, time.UTC)
	raw := []byte(`[
	  {"Id":"1a2b","Names":["eval-runner"],"Image":"fedora-toolbox:40","State":"running","Status":"Up 3 days","Size":{"rwSize":180000000}},
	  {"Id":"3c4d","Names":["busybox-tmp"],"Image":"busybox:latest","State":"exited","Status":"Exited (0) 4 days ago","Size":{"rwSize":4000000}}
	]`)
	items := mapPodmanContainers(raw, now)
	if len(items) != 2 {
		t.Fatalf("got %d containers, want 2", len(items))
	}
	if items[0].Name != "eval-runner" || !items[0].InUse || items[0].Size != 180000000 {
		t.Errorf("container[0] = %+v; want running eval-runner with rwSize", items[0])
	}
	if items[1].InUse {
		t.Errorf("exited container should not be in use: %+v", items[1])
	}
}

func TestMapPodmanContainersNoSize(t *testing.T) {
	// Without --size podman omits the Size object; map must not panic and size=0.
	raw := []byte(`[{"Id":"x","Names":["c"],"State":"running","Status":"Up"}]`)
	items := mapPodmanContainers(raw, time.Now())
	if len(items) != 1 || items[0].Size != 0 {
		t.Errorf("got %+v; want one container, size 0", items)
	}
}

func TestCollectVolumeRefsAndVolumes(t *testing.T) {
	ctrRaw := []byte(`[
	  {"Id":"1","Names":["c1"],"State":"running","Mounts":[{"Type":"volume","Name":"eval-cache"}]},
	  {"Id":"2","Names":["c2"],"State":"exited","Mounts":[{"Type":"bind","Name":""},{"Type":"volume","Name":"fedora-home"}]}
	]`)
	refs := collectVolumeRefs(ctrRaw)
	if refs["eval-cache"] != 1 || refs["fedora-home"] != 1 {
		t.Errorf("volume refs = %+v; want eval-cache=1, fedora-home=1", refs)
	}

	volRaw := []byte(`[
	  {"Name":"eval-cache","Driver":"local"},
	  {"Name":"fedora-home","Driver":"local"},
	  {"Name":"old-datasets","Driver":"local"}
	]`)
	items := mapPodmanVolumes(volRaw, refs)
	if len(items) != 3 {
		t.Fatalf("got %d volumes, want 3", len(items))
	}
	byName := map[string]bool{}
	for _, it := range items {
		byName[it.Name] = it.InUse
	}
	if !byName["eval-cache"] || !byName["fedora-home"] {
		t.Error("mounted volumes should be in use")
	}
	if byName["old-datasets"] {
		t.Error("unmounted volume should be reclaimable (not in use)")
	}
}

func TestParsePodmanVersion(t *testing.T) {
	if v, err := parsePodmanVersion([]byte(`{"Client":{"Version":"5.1.2"},"Server":{"Version":"5.1.2"}}`)); err != nil || v != "5.1.2" {
		t.Errorf("Client.Version = %q (err %v), want 5.1.2", v, err)
	}
	if v, _ := parsePodmanVersion([]byte(`{"Version":"4.9.0"}`)); v != "4.9.0" {
		t.Errorf("flat Version = %q, want 4.9.0", v)
	}
	if _, err := parsePodmanVersion([]byte(`nope`)); err == nil {
		t.Error("invalid version json should error")
	}
}

func TestParseHumanSize(t *testing.T) {
	cases := map[string]int64{
		"4.2GB":          4_200_000_000,
		"512MB":          512_000_000,
		"0B":             0,
		"1.5kB":          1_500,
		"2.1GB (50%)":    2_100_000_000,
		"3GB extra junk": 3_000_000_000,
		"":               0,
		"garbage":        0,
	}
	for in, want := range cases {
		if got := parseHumanSize(in); got != want {
			t.Errorf("parseHumanSize(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestParseDFBytes(t *testing.T) {
	if got := parseDFBytes([]byte(`123456`)); got != 123456 {
		t.Errorf("numeric = %d, want 123456", got)
	}
	if got := parseDFBytes([]byte(`"4.2GB"`)); got != 4_200_000_000 {
		t.Errorf("string = %d, want 4.2e9", got)
	}
	if got := parseDFBytes([]byte(``)); got != 0 {
		t.Errorf("empty = %d, want 0", got)
	}
}

func TestParsePodmanSystemDF(t *testing.T) {
	// string-shaped (common) form
	str := []byte(`[{"Type":"Images","Total":6,"Active":2,"Size":"4.2GB","Reclaimable":"2.1GB (50%)"},
	                {"Type":"Local Volumes","Size":"1GB","Reclaimable":"0B"}]`)
	m := parsePodmanSystemDF(str)
	if m["images"].Size != 4_200_000_000 || m["images"].Reclaim != 2_100_000_000 {
		t.Errorf("images rollup = %+v", m["images"])
	}
	if m["volumes"].Size != 1_000_000_000 {
		t.Errorf("volumes rollup = %+v", m["volumes"])
	}
	// numeric-shaped form
	num := []byte(`[{"Type":"Images","Size":900000000,"Reclaimable":800000000}]`)
	if parsePodmanSystemDF(num)["images"].Size != 900000000 {
		t.Errorf("numeric images size not parsed")
	}
	if len(parsePodmanSystemDF([]byte(`garbage`))) != 0 {
		t.Error("garbage df should parse to empty map")
	}
}

func TestImagesDedupPrefersSystemDF(t *testing.T) {
	imgRaw := []byte(`[{"Id":"a","Size":800,"SharedSize":600,"Containers":1}]`)
	dfRaw := []byte(`[{"Type":"Images","Size":1000,"Reclaimable":700}]`)
	size, reclaim := imagesDedup(imgRaw, dfRaw)
	if size == nil || *size != 1000 || reclaim == nil || *reclaim != 700 {
		t.Errorf("should use system df: size=%v reclaim=%v", size, reclaim)
	}
}

func TestImagesDedupFallsBackToUniqueSum(t *testing.T) {
	// No usable df → unique-sum: active 800-600=200 (used), idle 900-600=300.
	// total = 200+300 = 500; reclaim (Containers==0 only) = 300.
	imgRaw := []byte(`[{"Id":"a","Size":800,"SharedSize":600,"Containers":1},
	                   {"Id":"b","Size":900,"SharedSize":600,"Containers":0}]`)
	size, reclaim := imagesDedup(imgRaw, nil)
	if size == nil || *size != 500 {
		t.Errorf("fallback total = %v, want 500", size)
	}
	if reclaim == nil || *reclaim != 300 {
		t.Errorf("fallback reclaim = %v, want 300", reclaim)
	}
}

func TestImagesDedupNoSharedNoOverride(t *testing.T) {
	// Old podman with no SharedSize → leave item-sum (return nil).
	imgRaw := []byte(`[{"Id":"a","Size":800,"Containers":1}]`)
	if size, _ := imagesDedup(imgRaw, nil); size != nil {
		t.Errorf("no SharedSize should yield no override, got %v", *size)
	}
}

func TestMapPodmanEmptyInputs(t *testing.T) {
	if mapPodmanImages(nil, time.Now()) != nil {
		t.Error("nil image json should map to nil")
	}
	if mapPodmanContainers([]byte(`garbage`), time.Now()) != nil {
		t.Error("garbage container json should map to nil")
	}
	if mapPodmanVolumes(nil, nil) != nil {
		t.Error("nil volume json should map to nil")
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
