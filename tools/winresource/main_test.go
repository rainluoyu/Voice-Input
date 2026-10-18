package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"unicode/utf16"

	"voice_input/internal/branding"
	"voice_input/internal/buildmeta"

	"github.com/tc-hib/winres"
	"github.com/tc-hib/winres/version"
)

func TestProductDisplayName(t *testing.T) {
	release, err := buildmeta.Parse(buildmeta.Version)
	if err != nil {
		t.Fatal(err)
	}
	set, err := resources(release)
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
	release, err := buildmeta.Parse(buildmeta.Version)
	if err != nil {
		t.Fatal(err)
	}
	set, err := resources(release)
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

func TestResourceVersions(t *testing.T) {
	for _, value := range []string{"12.34.56", "12.34.56-rc.1"} {
		t.Run(value, func(t *testing.T) {
			release, err := buildmeta.Parse(value)
			if err != nil {
				t.Fatal(err)
			}
			set, err := resources(release)
			if err != nil {
				t.Fatal(err)
			}
			versions, manifests := 0, 0
			set.Walk(func(kind, id winres.Identifier, language uint16, data []byte) bool {
				switch kind {
				case winres.RT_VERSION:
					versions++
					info, err := version.FromBytes(data)
					if err != nil {
						t.Fatal(err)
					}
					if info.FileVersion != release.Windows || info.ProductVersion != release.Windows {
						t.Fatal("wrong numeric versions", info)
					}
					for _, table := range info.Table() {
						if (*table)[version.FileVersion] != value || (*table)[version.ProductVersion] != value {
							t.Fatal("wrong string versions", table)
						}
					}
				case winres.RT_MANIFEST:
					manifests++
					manifest, err := winres.AppManifestFromXML(data)
					if err != nil || manifest.Identity.Name != "voice_input" || manifest.Identity.Version != release.Windows {
						t.Fatal("wrong manifest version", manifest, err)
					}
				}
				return true
			})
			if versions == 0 || manifests == 0 {
				t.Fatal("missing version/manifest resource")
			}
		})
	}
}

func TestPrintVersionHasNoSideEffects(t *testing.T) {
	dir := t.TempDir()
	executable := filepath.Join(dir, "winresource")
	command := exec.Command("go", "build", "-o", executable, ".")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("compile tool: %v %s", err, output)
	}
	object := filepath.Join(dir, "must-not-exist.syso")
	png := filepath.Join(dir, "must-not-exist.png")
	for _, value := range []string{buildmeta.Version, "12.34.56-rc.1"} {
		command := exec.Command(executable, "-version", value, "-print-version", "-out", object, "-png", png)
		output, err := command.CombinedOutput()
		if err != nil || string(output) != value+"\n" {
			t.Fatalf("print version: %q, %v", output, err)
		}
	}
	command = exec.Command(executable, "-print-version", "-out", object)
	if output, err := command.CombinedOutput(); err != nil || string(output) != buildmeta.Version+"\n" {
		t.Fatalf("default version: %q, %v", output, err)
	}
	command = exec.Command(executable, "-version", "v1.2.3", "-print-version", "-out", object)
	if err := command.Run(); err == nil {
		t.Fatal("invalid print version accepted")
	}
	for _, path := range []string{object, png} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatal("print-version generated output", path, err)
		}
	}
}
