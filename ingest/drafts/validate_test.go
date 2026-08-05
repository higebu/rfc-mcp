package drafts

import "testing"

func TestValidateDraftName(t *testing.T) {
	valid := []string{
		"draft-ietf-quic-transport",
		"draft-ietf-bess-mup-safi",
		"draft-example-protocol",
		"draft-kille-x.400-88", // dots occur in real (older) draft names
		"draft-foo_bar",        // underscores occur in a few historical names
		"draft-0abc",
		"draft-x",
	}
	for _, name := range valid {
		if err := validateDraftName(name); err != nil {
			t.Errorf("validateDraftName(%q) = %v, want nil", name, err)
		}
	}

	invalid := []string{
		"",
		"draft-",
		"rfc9000",
		"Draft-Foo",                  // uppercase never appears in Datatracker names
		"draft-Foo",                  //
		"draft-foo bar",              // whitespace
		"draft-foo/../../etc/passwd", // path separator / traversal
		"../draft-foo",
		"draft-..",     // "." after "draft-" fails the [a-z0-9] first-char rule
		"-draft-foo",   //
		"draft-foo%2e", // percent-encoding must not smuggle bytes past the check
	}
	for _, name := range invalid {
		if err := validateDraftName(name); err == nil {
			t.Errorf("validateDraftName(%q) = nil, want an error", name)
		}
	}
}

func TestNormalizeDraftRev(t *testing.T) {
	for in, want := range map[string]string{"3": "03", "0": "00", "34": "34", "00": "00"} {
		got, err := normalizeDraftRev(in)
		if err != nil {
			t.Errorf("normalizeDraftRev(%q) = %v, want nil", in, err)
			continue
		}
		if got != want {
			t.Errorf("normalizeDraftRev(%q) = %q, want %q", in, got, want)
		}
	}

	for _, in := range []string{"", "abc", "3a", "123", "-1", "1.", "0/", "latest"} {
		if _, err := normalizeDraftRev(in); err == nil {
			t.Errorf("normalizeDraftRev(%q) = nil error, want an error", in)
		}
	}
}
