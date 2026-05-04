package deps

import "testing"

func TestParseNeedString(t *testing.T) {
	cases := []struct {
		in   string
		want Need
		err  bool
	}{
		{in: "service:llama-server", want: Need{Kind: "service", Name: "llama-server"}},
		{in: "service:llama-server@windows", want: Need{Kind: "service", Name: "llama-server", Tier: "windows"}},
		{in: "bridge:producer-pal", want: Need{Kind: "bridge", Name: "producer-pal"}},
		{in: "service:llama-server@container:devbox-1", want: Need{Kind: "service", Name: "llama-server", Tier: "container:devbox-1"}},
		{in: "", err: true},
		{in: "service:", err: true},
		{in: "thing:foo", err: true},
		{in: "service:Foo", err: true},
		{in: "service:foo@unknown", err: true},
		{in: "service:foo@windows@extra", err: true},
	}
	for _, tc := range cases {
		got, err := ParseNeedString(tc.in)
		if tc.err {
			if err == nil {
				t.Errorf("%q: expected error, got %+v", tc.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("%q: unexpected error: %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%q: got %+v, want %+v", tc.in, got, tc.want)
		}
	}
}

func TestRuleValidate(t *testing.T) {
	cases := []struct {
		name string
		r    Rule
		err  bool
	}{
		{
			name: "valid mcp host",
			r:    Rule{Entity: "mcp:llama", Scope: "host", Needs: []Need{{Kind: "service", Name: "llama-server"}}},
		},
		{
			name: "valid skill any",
			r:    Rule{Entity: "skill:summarize-doc", Scope: "*", Needs: []Need{{Kind: "service", Name: "llama-server"}}},
		},
		{
			name: "valid project glob",
			r:    Rule{Entity: "mcp:llama", Scope: "project:*", Needs: []Need{{Kind: "bridge", Name: "llama-cpp"}}},
		},
		{
			name: "valid project absolute",
			r:    Rule{Entity: "mcp:llama", Scope: "project:/home/u/proj", Needs: []Need{{Kind: "bridge", Name: "x"}}},
		},
		{
			name: "bad entity kind",
			r:    Rule{Entity: "weird:thing", Scope: "host", Needs: []Need{{Kind: "service", Name: "x"}}},
			err:  true,
		},
		{
			name: "no needs",
			r:    Rule{Entity: "mcp:llama", Scope: "host"},
			err:  true,
		},
		{
			name: "relative project path",
			r:    Rule{Entity: "mcp:llama", Scope: "project:../foo", Needs: []Need{{Kind: "bridge", Name: "x"}}},
			err:  true,
		},
		{
			name: "unknown scope form",
			r:    Rule{Entity: "mcp:llama", Scope: "weird", Needs: []Need{{Kind: "bridge", Name: "x"}}},
			err:  true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.r.Validate()
			if tc.err && err == nil {
				t.Error("expected error")
			}
			if !tc.err && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}
