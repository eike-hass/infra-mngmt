package system

import (
	"encoding/json"
	"testing"
)

func TestParseWinDiskMultipleDistros(t *testing.T) {
	in := `{"drive":"C:","total":1000000000000,"used":850000000000,"distros":[` +
		`{"name":"Ubuntu-24.04","vhdxPath":"C:\\x\\ext4.vhdx","vhdxSize":229000000000},` +
		`{"name":"Ubuntu-22.04","vhdxPath":"C:\\y\\ext4.vhdx","vhdxSize":40000000000}]}`
	drive, byName, err := parseWinDisk([]byte(in))
	if err != nil {
		t.Fatalf("parseWinDisk: %v", err)
	}
	if drive.Drive != "C:" || drive.Total != 1000000000000 || drive.Used != 850000000000 {
		t.Errorf("drive = %+v", drive)
	}
	if len(byName) != 2 {
		t.Fatalf("got %d distros, want 2", len(byName))
	}
	if byName["Ubuntu-24.04"].VhdxSize != 229000000000 {
		t.Errorf("Ubuntu-24.04 vhdx = %d", byName["Ubuntu-24.04"].VhdxSize)
	}
}

func TestParseWinDiskSingleDistroUnwrapped(t *testing.T) {
	// PowerShell ConvertTo-Json emits a single-element array as a bare object —
	// the parser must accept that shape too.
	in := `{"drive":"C:","total":500,"used":100,"distros":{"name":"solo","vhdxPath":"p","vhdxSize":42}}`
	_, byName, err := parseWinDisk([]byte(in))
	if err != nil {
		t.Fatalf("parseWinDisk: %v", err)
	}
	if len(byName) != 1 || byName["solo"].VhdxSize != 42 {
		t.Errorf("single-object distros not handled: %+v", byName)
	}
}

func TestParseWinDiskNoDistros(t *testing.T) {
	for _, in := range []string{
		`{"drive":"C:","total":1,"used":0,"distros":null}`,
		`{"drive":"C:","total":1,"used":0}`,
	} {
		_, byName, err := parseWinDisk([]byte(in))
		if err != nil {
			t.Fatalf("parseWinDisk(%s): %v", in, err)
		}
		if len(byName) != 0 {
			t.Errorf("parseWinDisk(%s) distros = %+v, want empty", in, byName)
		}
	}
}

func TestParseWinDiskDefaultsDrive(t *testing.T) {
	_, _, err := parseWinDisk([]byte(`not json`))
	if err == nil {
		t.Error("invalid json should error")
	}
	drive, _, err := parseWinDisk([]byte(`{"total":1,"used":0,"distros":[]}`))
	if err != nil || drive.Drive != "C:" {
		t.Errorf("missing drive should default to C:, got %q (err %v)", drive.Drive, err)
	}
}

func TestDecodeOneOrMany(t *testing.T) {
	if got := decodeOneOrMany[winDistro](json.RawMessage(`[{"name":"a"},{"name":"b"}]`)); len(got) != 2 {
		t.Errorf("array form: got %d, want 2", len(got))
	}
	if got := decodeOneOrMany[winDistro](json.RawMessage(`{"name":"solo"}`)); len(got) != 1 || got[0].Name != "solo" {
		t.Errorf("object form: got %+v", got)
	}
	if got := decodeOneOrMany[winDistro](json.RawMessage(`null`)); got != nil {
		t.Errorf("null form: got %+v, want nil", got)
	}
	if got := decodeOneOrMany[winDistro](json.RawMessage(``)); got != nil {
		t.Errorf("empty form: got %+v, want nil", got)
	}
}

func TestTagEngines(t *testing.T) {
	d := DiskInfo{Distros: []Distro{
		{Name: "Ubuntu-24.04"},
		{Name: "podman-machine-default"},
		{Name: "Ubuntu-22.04"},
	}}
	d.TagEngines("Ubuntu-24.04", "podman-machine-default")
	if d.Distros[0].Engine != "docker" {
		t.Errorf("distro[0].Engine = %q, want docker", d.Distros[0].Engine)
	}
	if d.Distros[1].Engine != "podman" {
		t.Errorf("distro[1].Engine = %q, want podman", d.Distros[1].Engine)
	}
	if d.Distros[2].Engine != "" {
		t.Errorf("distro[2].Engine = %q, want empty", d.Distros[2].Engine)
	}
}
