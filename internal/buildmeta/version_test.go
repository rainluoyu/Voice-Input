package buildmeta

import (
	"os"
	"testing"
)

func TestLinkedVersion(t *testing.T) {
	if expected := os.Getenv("VOICE_INPUT_TEST_VERSION"); expected != "" && Version != expected {
		t.Fatalf("linked Version = %q, want %q", Version, expected)
	}
	if _, err := Parse(Version); err != nil {
		t.Fatal(err)
	}
	t.Logf("linked Version = %s", Version)
}

func TestParse(t *testing.T) {
	for _, value := range []string{"0.0.0", "0.2.0", "12.34.56", "65535.65535.65535", "1.2.3-alpha", "1.2.3-rc.1", "1.2.3-0", "1.2.3-001a", "1.2.3-alpha-beta.123456789012345678901234567890", "1.2.3--"} {
		t.Run(value, func(t *testing.T) {
			parsed, err := Parse(value)
			if err != nil || parsed.Canonical != value || parsed.Windows[3] != 0 {
				t.Fatalf("Parse(%q) = %+v, %v", value, parsed, err)
			}
		})
	}
	parsed, err := Parse("12.34.56-rc.1")
	if err != nil || parsed.Windows != [4]uint16{12, 34, 56, 0} {
		t.Fatalf("wrong Windows version: %+v, %v", parsed, err)
	}
}

func TestParseRejectsInvalidVersions(t *testing.T) {
	for _, value := range []string{"", "v1.2.3", "1.2", "1.2.3.4", "01.2.3", "1.02.3", "1.2.03", "65536.0.0", "0.65536.0", "0.0.65536", "99999999999999999999999.0.0", "+1.2.3", "-1.2.3", "1.2.3+build", "1.2.3-rc+build", "1.2.3-", "1.2.3-01", "1.2.3-alpha.01", "1.2.3-.alpha", "1.2.3-alpha.", "1.2.3-a..b", "1.2.3-a_b", "1.2.3-候选", "1.2.3-rc/1", " 1.2.3", "1.2.3\n", "1.2.3-rc 1", "1.2.3-rc\x00"} {
		t.Run(value, func(t *testing.T) {
			if parsed, err := Parse(value); err == nil || parsed != (ParsedVersion{}) {
				t.Fatalf("accepted %q: %+v, %v", value, parsed, err)
			}
		})
	}
}
