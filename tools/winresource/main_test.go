package main

import (
	"bytes"
	"testing"
	"unicode/utf16"

	"voice_input/internal/branding"

	"github.com/tc-hib/winres"
)

func TestProductDisplayName(t *testing.T) {
	set, err := resources()
	if err != nil {
		t.Fatal(err)
	}
	var name []byte
	for _, u := range utf16.Encode([]rune(branding.Name)) {
		name = append(name, byte(u), byte(u>>8))
	}
	found := false
	set.Walk(func(kind, id winres.Identifier, language uint16, data []byte) bool {
		if kind == winres.RT_VERSION {
			found = bytes.Count(data, name) >= 2
		}
		return true
	})
	if !found {
		t.Fatal("product name and file description missing")
	}
}

func TestManifestHasNoElevation(t *testing.T) {
	set, err := resources()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	set.Walk(func(kind, id winres.Identifier, language uint16, data []byte) bool {
		if kind == winres.RT_MANIFEST {
			found = true
			for _, text := range []string{`level="asInvoker"`, `uiAccess="false"`, `permonitorv2`, `voice_input`, branding.Name} {
				if !bytes.Contains(data, []byte(text)) {
					t.Errorf("missing %s", text)
				}
			}
			if bytes.Contains(data, []byte("requireAdministrator")) {
				t.Fatal("elevation enabled")
			}
		}
		return true
	})
	if !found {
		t.Fatal("manifest missing")
	}
}
