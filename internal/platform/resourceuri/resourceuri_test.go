package resourceuri

import "testing"

func TestParseResourceURI(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want URI
		err  bool
	}{
		{name: "github repo", in: "github:repo:example-org/example-repo", want: URI{Provider: "github", Kind: "repo", Identifier: "example-org/example-repo"}},
		{name: "identifier contains colon", in: "aws:role:arn:aws:iam::123:role/x", want: URI{Provider: "aws", Kind: "role", Identifier: "arn:aws:iam::123:role/x"}},
		{name: "missing provider", in: ":repo:x", err: true},
		{name: "missing kind", in: "github::x", err: true},
		{name: "missing identifier", in: "github:repo:", err: true},
		{name: "missing second colon", in: "github:repo", err: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Parse(tc.in)
			if tc.err {
				if err == nil {
					t.Fatalf("Parse(%q) succeeded with %+v", tc.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("Parse(%q): %v", tc.in, err)
			}
			if got != tc.want {
				t.Fatalf("Parse(%q) = %+v, want %+v", tc.in, got, tc.want)
			}
			if got.String() != tc.in {
				t.Fatalf("String() = %q, want %q", got.String(), tc.in)
			}
		})
	}
}
