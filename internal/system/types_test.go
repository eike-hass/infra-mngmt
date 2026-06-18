package system

import "testing"

const gb = 1024 * 1024 * 1024

func sampleRow() DFRow {
	return DFRow{
		Key:   "images",
		Label: "Images",
		Prune: "docker image prune -a",
		Items: []DFItem{
			{ID: "a", Repo: "node", Tag: "20", Size: 100, InUse: true},
			{ID: "b", Repo: "redis", Tag: "7", Size: 50, InUse: true},
			{ID: "c", Repo: "<none>", Tag: "<none>", Size: 200, InUse: false},
			{ID: "d", Repo: "old", Tag: "latest", Size: 300, InUse: false},
		},
	}
}

func TestDFRowAggregates(t *testing.T) {
	r := sampleRow()
	if got := r.Total(); got != 4 {
		t.Errorf("Total() = %d, want 4", got)
	}
	if got := r.Active(); got != 2 {
		t.Errorf("Active() = %d, want 2", got)
	}
	if got := r.Size(); got != 650 {
		t.Errorf("Size() = %d, want 650", got)
	}
	if got := r.Reclaimable(); got != 500 {
		t.Errorf("Reclaimable() = %d, want 500 (only not-in-use)", got)
	}
}

func TestDFRowRollupOverride(t *testing.T) {
	r := sampleRow() // item-summed: size 650, reclaimable 500
	size, reclaim := int64(1000), int64(800)
	r.SizeOverride = &size
	r.ReclaimOverride = &reclaim
	if got := r.Size(); got != 1000 {
		t.Errorf("Size() with override = %d, want 1000", got)
	}
	if got := r.Reclaimable(); got != 800 {
		t.Errorf("Reclaimable() with override = %d, want 800", got)
	}
	// Total/Active still count items (override is only for byte rollups).
	if r.Total() != 4 || r.Active() != 2 {
		t.Errorf("override should not affect counts: total=%d active=%d", r.Total(), r.Active())
	}
}

func TestDFRowEmpty(t *testing.T) {
	var r DFRow
	if r.Total() != 0 || r.Active() != 0 || r.Size() != 0 || r.Reclaimable() != 0 {
		t.Errorf("zero row should aggregate to all zeros, got %+v", r)
	}
}

func TestEngineDFTotals(t *testing.T) {
	df := EngineDF{Rows: []DFRow{
		sampleRow(),
		{Key: "containers", Items: []DFItem{
			{ID: "x", Size: 10, InUse: true},
			{ID: "y", Size: 20, InUse: false},
		}},
	}}
	if got := df.TotalSize(); got != 680 {
		t.Errorf("TotalSize() = %d, want 680", got)
	}
	if got := df.TotalReclaimable(); got != 520 {
		t.Errorf("TotalReclaimable() = %d, want 520", got)
	}
}

func TestEngineDFSafeReclaim(t *testing.T) {
	df := EngineDF{Rows: []DFRow{
		{Key: "images", Items: []DFItem{
			{ID: "tagged-active", Repo: "node", Size: 100, InUse: true},
			{ID: "dangling", Repo: "<none>", Size: 200, SharedSize: 50, InUse: false}, // unique 150
			{ID: "tagged-idle", Repo: "old", Size: 500, InUse: false},                 // NOT safe (tagged)
		}},
		{Key: "buildcache", Items: []DFItem{{ID: "bc1", Size: 300, InUse: false}}},
		{Key: "volumes", Items: []DFItem{{Name: "data", Size: 9000, InUse: false}}},
	}}
	if got := df.DanglingImages(); got != 1 {
		t.Errorf("DanglingImages() = %d, want 1 (only the <none> image)", got)
	}
	if !df.HasSafeReclaim() {
		t.Error("HasSafeReclaim() = false, want true (dangling image + build cache)")
	}
	// build cache 300 + dangling unique (200−50=150); tagged-idle and the
	// volume are excluded.
	if got := df.SafeReclaim(); got != 450 {
		t.Errorf("SafeReclaim() = %d, want 450", got)
	}
}

func TestEngineDFSafeReclaimCapped(t *testing.T) {
	// A dangling image reporting no SharedSize would naively contribute its full
	// 5000, but the deduplicated images reclaimable is only 1000 — SafeReclaim
	// caps the dangling portion there so it can never exceed prune-all.
	recl := int64(1000)
	df := EngineDF{Rows: []DFRow{
		{Key: "images", ReclaimOverride: &recl, Items: []DFItem{
			{ID: "dangling", Repo: "<none>", Size: 5000, InUse: false},
		}},
	}}
	if got := df.SafeReclaim(); got != 1000 {
		t.Errorf("SafeReclaim() = %d, want 1000 (capped at deduplicated images reclaimable)", got)
	}
}

func TestEngineDFNoSafeReclaim(t *testing.T) {
	// Only a tagged-idle image and a volume — nothing loss-free to remove.
	df := EngineDF{Rows: []DFRow{
		{Key: "images", Items: []DFItem{{Repo: "old", Size: 500, InUse: false}}},
		{Key: "volumes", Items: []DFItem{{Name: "d", Size: 100, InUse: false}}},
	}}
	if df.HasSafeReclaim() {
		t.Error("HasSafeReclaim() = true, want false (tagged image + volume aren't safe)")
	}
	if df.DanglingImages() != 0 {
		t.Errorf("DanglingImages() = %d, want 0", df.DanglingImages())
	}
}

func TestEngineDFRowLookup(t *testing.T) {
	df := EngineDF{Rows: []DFRow{sampleRow()}}
	if r, ok := df.Row("images"); !ok || r.Key != "images" {
		t.Errorf("Row(images) = %+v, %v; want the images row", r, ok)
	}
	if _, ok := df.Row("nope"); ok {
		t.Error("Row(nope) should report ok=false")
	}
}

func TestDFItemKey(t *testing.T) {
	if got := (DFItem{ID: "id1", Name: "n1"}).Key(); got != "id1" {
		t.Errorf("Key() with ID = %q, want id1", got)
	}
	if got := (DFItem{Name: "vol1"}).Key(); got != "vol1" {
		t.Errorf("Key() without ID = %q, want vol1 (volumes key on name)", got)
	}
}

func TestWindowsDriveFreeAndLow(t *testing.T) {
	w := WindowsDrive{Drive: "C:", Total: 100 * gb, Used: 90 * gb}
	if got := w.Free(); got != 10*gb {
		t.Errorf("Free() = %d, want %d", got, 10*gb)
	}
	if !w.Low() {
		t.Error("90/100 used (10% free) should be Low()")
	}
	w2 := WindowsDrive{Total: 100 * gb, Used: 50 * gb}
	if w2.Low() {
		t.Error("50% free should not be Low()")
	}
}

func TestWindowsDriveFreeFloorsAtZero(t *testing.T) {
	// Used can momentarily exceed Total across two reads; never report negative.
	w := WindowsDrive{Total: 10, Used: 12}
	if got := w.Free(); got != 0 {
		t.Errorf("Free() = %d, want 0 (floored)", got)
	}
}

func TestDistroCompactGap(t *testing.T) {
	// vhdx grew to 214GB while only 198GB is used inside → 16GB reclaimable.
	d := Distro{VhdxSize: 214 * gb, FsUsed: 198 * gb, FsKnown: true}
	if got := d.CompactGap(); got != 16*gb {
		t.Errorf("CompactGap() = %d, want %d", got, 16*gb)
	}
	// vhdx never smaller than fsUsed in practice, but floor defensively.
	if got := (Distro{VhdxSize: 10, FsUsed: 20, FsKnown: true}).CompactGap(); got != 0 {
		t.Errorf("CompactGap() floored = %d, want 0", got)
	}
	// A stopped distro (usage unknown) must report 0, not its whole vhdx.
	if got := (Distro{VhdxSize: 100 * gb, FsKnown: false}).CompactGap(); got != 0 {
		t.Errorf("CompactGap() with unknown fs = %d, want 0 (not the full vhdx)", got)
	}
}

func TestDistroFsHelpers(t *testing.T) {
	d := Distro{State: "Running", FsTotal: 100 * gb, FsUsed: 95 * gb}
	if !d.Running() {
		t.Error("State=Running should report Running()")
	}
	if got := d.FsFree(); got != 5*gb {
		t.Errorf("FsFree() = %d, want %d", got, 5*gb)
	}
	if !d.FsLow() {
		t.Error("5% free should be FsLow()")
	}
}

func TestDistroSubLabel(t *testing.T) {
	cases := []struct {
		d    Distro
		want string
	}{
		{Distro{Version: 2, Default: true, Engine: "docker"}, "WSL2 · default · docker host"},
		{Distro{Version: 2, Engine: "podman"}, "WSL2 · podman host"},
		{Distro{Version: 2}, "WSL2"},
		{Distro{Version: 1, Default: true}, "WSL1 · default"},
	}
	for _, c := range cases {
		if got := c.d.SubLabel(); got != c.want {
			t.Errorf("SubLabel(%+v) = %q, want %q", c.d, got, c.want)
		}
	}
}

func TestDiskInfoFootprintAndReclaimable(t *testing.T) {
	d := DiskInfo{Distros: []Distro{
		{VhdxSize: 214 * gb, FsUsed: 198 * gb, FsKnown: true}, // gap 16
		{VhdxSize: 47 * gb, FsUsed: 41 * gb, FsKnown: true},   // gap 6
		{VhdxSize: 38 * gb, FsUsed: 33 * gb, FsKnown: true},   // gap 5
		{VhdxSize: 60 * gb, FsKnown: false},                   // stopped: counts to footprint, 0 reclaimable
	}}
	if got := d.Footprint(); got != (214+47+38+60)*gb {
		t.Errorf("Footprint() = %d, want %d (all vhdx, incl. stopped)", got, (214+47+38+60)*gb)
	}
	if got := d.Reclaimable(); got != (16+6+5)*gb {
		t.Errorf("Reclaimable() = %d, want %d (stopped distro contributes 0)", got, (16+6+5)*gb)
	}
}
