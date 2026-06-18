package system

import "testing"

func TestDecodeMaybeUTF16_PlainUTF8(t *testing.T) {
	if got := decodeMaybeUTF16([]byte("plain ascii text")); got != "plain ascii text" {
		t.Errorf("decodeMaybeUTF16(ascii) = %q", got)
	}
}

func TestDecodeMaybeUTF16_BOMLed(t *testing.T) {
	// "Hi" as UTF-16LE with BOM.
	b := []byte{0xFF, 0xFE, 'H', 0x00, 'i', 0x00}
	if got := decodeMaybeUTF16(b); got != "Hi" {
		t.Errorf("decodeMaybeUTF16(bom utf16) = %q, want Hi", got)
	}
}

func TestDecodeMaybeUTF16_NoBOM(t *testing.T) {
	// "NAME" as UTF-16LE without a BOM — the heuristic must still catch it.
	b := []byte{'N', 0x00, 'A', 0x00, 'M', 0x00, 'E', 0x00}
	if got := decodeMaybeUTF16(b); got != "NAME" {
		t.Errorf("decodeMaybeUTF16(bomless utf16) = %q, want NAME", got)
	}
}

func TestLooksUTF16LE(t *testing.T) {
	if looksUTF16LE([]byte("regular ascii")) {
		t.Error("ascii should not look like UTF-16LE")
	}
	if !looksUTF16LE([]byte{'a', 0, 'b', 0, 'c', 0, 'd', 0}) {
		t.Error("interleaved NULs should look like UTF-16LE")
	}
	if looksUTF16LE([]byte{'x'}) {
		t.Error("single byte should not look like UTF-16LE")
	}
}

func TestResolveExeNotFound(t *testing.T) {
	// A binary that cannot exist resolves to an error rather than panicking —
	// this is the path that yields the card's graceful "interop unavailable"
	// error state when running off the WSL host.
	if _, err := resolveExe("definitely-not-a-real-binary.exe", "/nonexistent/path"); err == nil {
		t.Error("resolveExe should error for a missing binary")
	}
}
