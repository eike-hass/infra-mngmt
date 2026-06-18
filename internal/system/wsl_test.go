package system

import "testing"

func TestParseWSLList(t *testing.T) {
	// Mirrors real `wsl --list --verbose` layout, including the default "*".
	in := "  NAME                     STATE           VERSION\n" +
		"* Ubuntu-24.04             Running         2\n" +
		"  podman-machine-default   Running         2\n" +
		"  Ubuntu-22.04             Stopped         2\n"
	got := parseWSLList(in)
	if len(got) != 3 {
		t.Fatalf("got %d distros, want 3: %+v", len(got), got)
	}
	if got[0].Name != "Ubuntu-24.04" || !got[0].Default || !got[0].Running() || got[0].Version != 2 {
		t.Errorf("distro[0] = %+v; want default running Ubuntu-24.04 v2", got[0])
	}
	if got[1].Name != "podman-machine-default" || got[1].Default {
		t.Errorf("distro[1] = %+v; want non-default podman-machine-default", got[1])
	}
	if got[2].Name != "Ubuntu-22.04" || got[2].Running() {
		t.Errorf("distro[2] = %+v; want stopped Ubuntu-22.04", got[2])
	}
}

func TestParseWSLListSkipsHeaderAndBlanks(t *testing.T) {
	if got := parseWSLList("\n  NAME   STATE   VERSION  \n\n"); len(got) != 0 {
		t.Errorf("header+blanks only should yield no distros, got %+v", got)
	}
}

func TestParseWSLListNameWithSpacesGuard(t *testing.T) {
	// Defensive: the parser treats the last two fields as STATE/VERSION, so a
	// hypothetical multi-token name still resolves name correctly.
	got := parseWSLList("  My Distro Name   Stopped   2\n")
	if len(got) != 1 || got[0].Name != "My Distro Name" || got[0].Running() {
		t.Errorf("got %+v; want stopped 'My Distro Name'", got)
	}
}

func TestNormalizeState(t *testing.T) {
	cases := map[string]string{"Running": "Running", "running": "Running", "Stopped": "Stopped", "Installing": "Stopped", "": "Stopped"}
	for in, want := range cases {
		if got := normalizeState(in); got != want {
			t.Errorf("normalizeState(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseDF(t *testing.T) {
	in := "Filesystem      1B-blocks          Used     Available Use% Mounted on\n" +
		"/dev/sdd     1081101176832  213674192896  812363405312  21% /\n"
	total, used, ok := parseDF(in)
	if !ok {
		t.Fatal("parseDF ok=false, want true")
	}
	if total != 1081101176832 || used != 213674192896 {
		t.Errorf("parseDF = (%d, %d), want (1081101176832, 213674192896)", total, used)
	}
}

func TestParseDFNoData(t *testing.T) {
	if _, _, ok := parseDF("Filesystem 1B-blocks Used Available Use% Mounted on\n"); ok {
		t.Error("header-only df should yield ok=false")
	}
	if _, _, ok := parseDF("garbage line here\n"); ok {
		t.Error("non-numeric df should yield ok=false")
	}
}
