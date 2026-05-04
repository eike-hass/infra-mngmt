package containers

import "testing"

func TestValidateAccepts(t *testing.T) {
	f := File{Containers: []Container{
		{Name: "ident-browser"},
		{Name: "abc_123.thing"},
		{Name: "X"},
	}}
	if err := f.Validate(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateRejectsBadName(t *testing.T) {
	bad := []string{"", "-leading-dash", ".dot-start", "has space", "with/slash", "with;semi"}
	for _, n := range bad {
		f := File{Containers: []Container{{Name: n}}}
		if err := f.Validate(); err == nil {
			t.Errorf("expected error for name %q", n)
		}
	}
}

func TestValidateRejectsDuplicateCaseInsensitive(t *testing.T) {
	f := File{Containers: []Container{{Name: "ident-browser"}, {Name: "Ident-Browser"}}}
	if err := f.Validate(); err == nil {
		t.Error("expected duplicate-name error")
	}
}
