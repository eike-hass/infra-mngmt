package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/eike-hass/infra-mngmt/internal/entity"
	"github.com/eike-hass/infra-mngmt/internal/source"
)

// helper to build a server with two host-style sources.
func twoSourceServer(srcA, srcB source.Source) *Server {
	return New([]source.Source{srcA, srcB}, nil, "", nil, nil, nil, nil, nil)
}

func TestPromoteCopiesFileBackedEntity(t *testing.T) {
	a := newMockSource("host:/proj-a", entity.ProjectScope("/proj-a"))
	b := newMockSource("host:/proj-b", entity.ProjectScope("/proj-b"))
	e := a.addEntity(entity.KindCommand, "deploy", []byte("body"))

	srv := twoSourceServer(a, b)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodPost,
		"/api/promote?from="+e.ID+"&to="+b.ID(), nil))

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rr.Code, rr.Body.String())
	}
	if got := string(b.files["command:deploy"]); got != "body" {
		t.Errorf("target file content = %q, want %q", got, "body")
	}
	if !strings.Contains(rr.Body.String(), "copied command/deploy") {
		t.Errorf("response missing success message:\n%s", rr.Body.String())
	}
}

func TestPromoteConflictWithoutOverwriteDoesNotWrite(t *testing.T) {
	a := newMockSource("host:/a", entity.GlobalScope())
	b := newMockSource("host:/b", entity.GlobalScope())
	e := a.addEntity(entity.KindCommand, "x", []byte("from-a"))
	b.addEntity(entity.KindCommand, "x", []byte("orig-b"))

	srv := twoSourceServer(a, b)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodPost,
		"/api/promote?from="+e.ID+"&to="+b.ID(), nil))

	if rr.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rr.Code)
	}
	if got := string(b.files["command:x"]); got != "orig-b" {
		t.Errorf("target was overwritten: got %q, want %q", got, "orig-b")
	}
	body := rr.Body.String()
	if !strings.Contains(body, "already has command/x") {
		t.Errorf("conflict body missing message:\n%s", body)
	}
	// The conflict fragment should include an overwrite confirm button
	// pointing back at the same from/to with overwrite=true.
	if !strings.Contains(body, "overwrite=true") {
		t.Errorf("conflict body missing overwrite=true link:\n%s", body)
	}
}

func TestPromoteOverwriteReplacesTarget(t *testing.T) {
	a := newMockSource("host:/a", entity.GlobalScope())
	b := newMockSource("host:/b", entity.GlobalScope())
	e := a.addEntity(entity.KindCommand, "x", []byte("from-a"))
	b.addEntity(entity.KindCommand, "x", []byte("orig-b"))

	srv := twoSourceServer(a, b)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodPost,
		"/api/promote?from="+e.ID+"&to="+b.ID()+"&overwrite=true", nil))

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rr.Code, rr.Body.String())
	}
	if got := string(b.files["command:x"]); got != "from-a" {
		t.Errorf("target should be overwritten: got %q, want %q", got, "from-a")
	}
}

func TestPromoteRejectsReadOnlyTarget(t *testing.T) {
	a := newMockSource("host:/a", entity.GlobalScope())
	b := newMockSource("vol:b", entity.GlobalScope())
	b.readOnly = true
	e := a.addEntity(entity.KindCommand, "x", []byte("body"))

	srv := twoSourceServer(a, b)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodPost,
		"/api/promote?from="+e.ID+"&to="+b.ID(), nil))

	if rr.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "read-only") {
		t.Errorf("body should mention read-only:\n%s", rr.Body.String())
	}
}

func TestPromoteSameSourceRejected(t *testing.T) {
	a := newMockSource("host:/a", entity.GlobalScope())
	e := a.addEntity(entity.KindCommand, "x", []byte("body"))
	srv := twoSourceServer(a, newMockSource("host:/b", entity.GlobalScope()))

	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodPost,
		"/api/promote?from="+e.ID+"&to="+a.ID(), nil))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rr.Code)
	}
}

func TestPromoteMissingFrom(t *testing.T) {
	srv := twoSourceServer(
		newMockSource("host:/a", entity.GlobalScope()),
		newMockSource("host:/b", entity.GlobalScope()),
	)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/promote?to=host:/b", nil))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rr.Code)
	}
}

func TestPromoteUnknownEntity(t *testing.T) {
	a := newMockSource("host:/a", entity.GlobalScope())
	b := newMockSource("host:/b", entity.GlobalScope())
	srv := twoSourceServer(a, b)

	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodPost,
		"/api/promote?from=ghost&to="+b.ID(), nil))
	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rr.Code)
	}
}

func TestPromotePickerListsOtherSources(t *testing.T) {
	a := newMockSource("host:/a", entity.GlobalScope())
	b := newMockSource("host:/b", entity.ProjectScope("/proj-b"))
	c := newMockSource("host:/c", entity.ProjectScope("/proj-c"))
	e := a.addEntity(entity.KindCommand, "x", []byte("body"))
	srv := New([]source.Source{a, b, c}, nil, "", nil, nil, nil, nil, nil)

	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodGet,
		"/partials/promote-picker?id="+e.ID, nil))
	if rr.Code != 200 {
		t.Fatalf("status = %d", rr.Code)
	}
	body := rr.Body.String()
	// Both other sources should appear, the source itself should not.
	if !strings.Contains(body, "to=host%3A%2Fb") && !strings.Contains(body, "to=host:/b") {
		t.Errorf("picker missing source b:\n%s", body)
	}
	if !strings.Contains(body, "to=host%3A%2Fc") && !strings.Contains(body, "to=host:/c") {
		t.Errorf("picker missing source c:\n%s", body)
	}
	if strings.Contains(body, "to=host%3A%2Fa") || strings.Contains(body, "to=host:/a") {
		t.Errorf("picker should not include source a (the entity's own source):\n%s", body)
	}
}

func TestPromotePickerEmptyWhenSoleSource(t *testing.T) {
	a := newMockSource("host:/only", entity.GlobalScope())
	e := a.addEntity(entity.KindCommand, "x", []byte("body"))
	srv := New([]source.Source{a}, nil, "", nil, nil, nil, nil, nil)

	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodGet,
		"/partials/promote-picker?id="+e.ID, nil))
	if rr.Code != 200 {
		t.Fatalf("status = %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "no other sources") {
		t.Errorf("expected empty-state message:\n%s", rr.Body.String())
	}
}

func TestPromoteClearReturnsEmpty(t *testing.T) {
	srv := New(nil, nil, "", nil, nil, nil, nil, nil)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/partials/promote-clear", nil))
	if rr.Code != 200 {
		t.Errorf("status = %d, want 200", rr.Code)
	}
	if rr.Body.Len() != 0 {
		t.Errorf("body should be empty, got %q", rr.Body.String())
	}
}

// ── modal v2: grouping, badges, rename ───────────────────────────────────────

func TestPromotePickerGroupsByTier(t *testing.T) {
	a := newMockSource("host:/a", entity.GlobalScope())      // own source, excluded
	b := newMockSource("host:/b", entity.GlobalScope())      // global tier
	c := newMockSource("host:/c", entity.ProjectScope("/c")) // project tier
	e := a.addEntity(entity.KindCommand, "x", []byte("body"))

	srv := New([]source.Source{a, b, c}, nil, "", nil, nil, nil, nil, nil)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/partials/promote-picker?id="+e.ID, nil))
	body := rr.Body.String()

	if !strings.Contains(body, "Global") || !strings.Contains(body, "Projects") {
		t.Errorf("picker should render group headings; body:\n%s", body)
	}
	// Global must come before Projects in document order.
	if strings.Index(body, "Global") > strings.Index(body, "Projects") {
		t.Errorf("Global heading should appear before Projects heading")
	}
}

func TestPromotePickerMarksReadOnlyTarget(t *testing.T) {
	a := newMockSource("host:/a", entity.GlobalScope())
	b := newMockSource("vol:b", entity.GlobalScope())
	b.readOnly = true
	e := a.addEntity(entity.KindCommand, "x", []byte("body"))

	srv := New([]source.Source{a, b}, nil, "", nil, nil, nil, nil, nil)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/partials/promote-picker?id="+e.ID, nil))
	body := rr.Body.String()

	if !strings.Contains(body, "read-only") {
		t.Errorf("read-only target missing badge:\n%s", body)
	}
	if !strings.Contains(body, "disabled") {
		t.Errorf("read-only target should be disabled:\n%s", body)
	}
	// A disabled button must NOT carry hx-post — clicking should be a no-op.
	// Find the button block for vol:b and ensure it lacks hx-post on that row.
	if strings.Contains(body, `hx-post="/api/promote?from=`+e.ID+`&to=vol:b"`) {
		t.Errorf("read-only target should not have hx-post:\n%s", body)
	}
}

func TestPromotePickerMarksExistingTarget(t *testing.T) {
	a := newMockSource("host:/a", entity.GlobalScope())
	b := newMockSource("host:/b", entity.GlobalScope())
	e := a.addEntity(entity.KindCommand, "x", []byte("from-a"))
	// Pre-populate b so the target shows as "exists/replaces".
	b.addEntity(entity.KindCommand, "x", []byte("orig-b"))

	srv := New([]source.Source{a, b}, nil, "", nil, nil, nil, nil, nil)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/partials/promote-picker?id="+e.ID, nil))
	body := rr.Body.String()

	if !strings.Contains(body, "replaces") {
		t.Errorf("conflicting target missing 'replaces' badge:\n%s", body)
	}
	// Empty target b' (different name) would still show "new"; here b has the
	// entity so 'new' must NOT appear for b.
	if strings.Count(body, "promote-flag new") > 0 {
		t.Errorf("'new' badge should not appear when only existing target is shown:\n%s", body)
	}
}

func TestPromotePickerMarksNewTarget(t *testing.T) {
	a := newMockSource("host:/a", entity.GlobalScope())
	b := newMockSource("host:/b", entity.GlobalScope())
	e := a.addEntity(entity.KindCommand, "fresh", []byte("body"))
	// b has no entity by that name → should be flagged as "new".

	srv := New([]source.Source{a, b}, nil, "", nil, nil, nil, nil, nil)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/partials/promote-picker?id="+e.ID, nil))
	body := rr.Body.String()

	if !strings.Contains(body, `promote-flag new`) {
		t.Errorf("clean target should show 'new' badge:\n%s", body)
	}
}

func TestPromotePickerSortsWritableFirst(t *testing.T) {
	a := newMockSource("host:/a", entity.GlobalScope())
	// Project scopes so each gets its project path rendered in the picker
	// (global hosts all collapse to the same "~/.claude" label).
	bRO := newMockSource("host:/aaa-readonly", entity.ProjectScope("/aaa-readonly"))
	bRO.readOnly = true
	bRW := newMockSource("host:/zzz-writable", entity.ProjectScope("/zzz-writable"))
	e := a.addEntity(entity.KindCommand, "x", []byte("body"))

	srv := New([]source.Source{a, bRO, bRW}, nil, "", nil, nil, nil, nil, nil)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/partials/promote-picker?id="+e.ID, nil))
	body := rr.Body.String()

	// Writable target ("/zzz-writable") should appear before read-only one
	// despite alphabetical order pointing the other way.
	wIdx := strings.Index(body, "/zzz-writable")
	roIdx := strings.Index(body, "/aaa-readonly")
	if wIdx == -1 || roIdx == -1 {
		t.Fatalf("targets missing from picker; w=%d ro=%d body:\n%s", wIdx, roIdx, body)
	}
	if wIdx > roIdx {
		t.Errorf("writable target should appear before read-only one (writable@%d, ro@%d)", wIdx, roIdx)
	}
}

func TestPromoteRename(t *testing.T) {
	a := newMockSource("host:/a", entity.GlobalScope())
	b := newMockSource("host:/b", entity.GlobalScope())
	e := a.addEntity(entity.KindCommand, "deploy", []byte("body"))

	srv := twoSourceServer(a, b)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost,
		"/api/promote?from="+e.ID+"&to="+b.ID()+"&name=deploy-prod", nil)
	srv.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d; body = %s", rr.Code, rr.Body.String())
	}
	if got := string(b.files["command:deploy-prod"]); got != "body" {
		t.Errorf("renamed file content = %q, want %q", got, "body")
	}
	if _, exists := b.files["command:deploy"]; exists {
		t.Errorf("original name should not be written under rename")
	}
	if !strings.Contains(rr.Body.String(), "as deploy-prod") {
		t.Errorf("response should mention the rename:\n%s", rr.Body.String())
	}
}

func TestPromoteRenameViaFormBody(t *testing.T) {
	a := newMockSource("host:/a", entity.GlobalScope())
	b := newMockSource("host:/b", entity.GlobalScope())
	e := a.addEntity(entity.KindCommand, "deploy", []byte("body"))

	srv := twoSourceServer(a, b)
	rr := httptest.NewRecorder()
	// hx-include sends the rename in the form body, not the URL.
	req := httptest.NewRequest(http.MethodPost,
		"/api/promote?from="+e.ID+"&to="+b.ID(),
		strings.NewReader("name=deploy-prod"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	srv.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d; body = %s", rr.Code, rr.Body.String())
	}
	if got := string(b.files["command:deploy-prod"]); got != "body" {
		t.Errorf("renamed file content = %q, want %q", got, "body")
	}
}

func TestPromoteRenameInvalidNameRejected(t *testing.T) {
	a := newMockSource("host:/a", entity.GlobalScope())
	b := newMockSource("host:/b", entity.GlobalScope())
	e := a.addEntity(entity.KindCommand, "deploy", []byte("body"))

	srv := twoSourceServer(a, b)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodPost,
		"/api/promote?from="+e.ID+"&to="+b.ID()+"&name=../etc/passwd", nil))

	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rr.Code)
	}
	if _, ok := b.files["command:../etc/passwd"]; ok {
		t.Errorf("invalid rename should not have been written")
	}
}

func TestPromoteRenamePreservedInConflictConfirm(t *testing.T) {
	a := newMockSource("host:/a", entity.GlobalScope())
	b := newMockSource("host:/b", entity.GlobalScope())
	e := a.addEntity(entity.KindCommand, "deploy", []byte("from-a"))
	// Target already has the renamed entity → conflict on first POST.
	b.addEntity(entity.KindCommand, "deploy-prod", []byte("orig-b"))

	srv := twoSourceServer(a, b)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodPost,
		"/api/promote?from="+e.ID+"&to="+b.ID()+"&name=deploy-prod", nil))

	if rr.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body = %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	// Confirm-overwrite button must round-trip the rename so the user doesn't
	// lose it.
	if !strings.Contains(body, "name=deploy-prod") {
		t.Errorf("conflict confirm should preserve renamed name in URL:\n%s", body)
	}
	if !strings.Contains(body, "overwrite=true") {
		t.Errorf("conflict confirm should include overwrite=true:\n%s", body)
	}
}

func TestPromoteRenameToExistingNameAtSameSourceRejected(t *testing.T) {
	// The same-source guard previously fired even when the user requested a
	// rename (which would actually be a useful "duplicate within source"
	// operation). Now the guard only triggers when there's no rename.
	// Verify the rejection still fires when both source==target AND no rename.
	a := newMockSource("host:/a", entity.GlobalScope())
	e := a.addEntity(entity.KindCommand, "x", []byte("body"))

	srv := New([]source.Source{a}, nil, "", nil, nil, nil, nil, nil)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodPost,
		"/api/promote?from="+e.ID+"&to="+a.ID(), nil))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rr.Code)
	}
}

func TestPromotePickerSeedsRenameInputWithEntityName(t *testing.T) {
	a := newMockSource("host:/a", entity.GlobalScope())
	b := newMockSource("host:/b", entity.GlobalScope())
	e := a.addEntity(entity.KindCommand, "myname", []byte("body"))

	srv := New([]source.Source{a, b}, nil, "", nil, nil, nil, nil, nil)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/partials/promote-picker?id="+e.ID, nil))
	body := rr.Body.String()

	if !strings.Contains(body, `id="promote-rename-input"`) {
		t.Errorf("modal missing rename input:\n%s", body)
	}
	if !strings.Contains(body, `value="myname"`) {
		t.Errorf("rename input should be seeded with entity name; body:\n%s", body)
	}
}
