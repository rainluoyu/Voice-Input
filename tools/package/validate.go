package main

import (
	"encoding/xml"
	"fmt"
	"runtime/debug"
	"strings"

	"github.com/tc-hib/winres"
	"github.com/tc-hib/winres/version"
	"voice_input/internal/buildmeta"
)

func validateBuild(build *debug.BuildInfo, release buildmeta.ParsedVersion) error {
	wanted := map[string]string{"GOOS": "windows", "GOARCH": "amd64", "CGO_ENABLED": "0"}
	seen := map[string]bool{}
	ldflags := ""
	for _, setting := range build.Settings {
		if expected, ok := wanted[setting.Key]; ok {
			if seen[setting.Key] || setting.Value != expected {
				return fmt.Errorf("invalid Go build setting %s: want %s", setting.Key, expected)
			}
			seen[setting.Key] = true
		}
		if setting.Key == "-ldflags" {
			if seen[setting.Key] {
				return fmt.Errorf("duplicate Go linker flags")
			}
			seen[setting.Key] = true
			ldflags = setting.Value
		}
	}
	for key := range wanted {
		if !seen[key] {
			return fmt.Errorf("missing Go build setting %s", key)
		}
	}
	// Go omits -ldflags entirely with -trimpath. The actual version resources
	// remain mandatory; only check the linker stamp when buildinfo records it.
	if !seen["-ldflags"] {
		return nil
	}
	// Release flags need no quoting or escaping. Reject these forms rather than
	// risk overlooking an overriding -X hidden in a quoted linker argument.
	if strings.ContainsAny(ldflags, "\"'\\") {
		return fmt.Errorf("release linker flags must be unquoted and unescaped")
	}
	const stamp = "voice_input/internal/buildmeta.Version="
	count := 0
	fields := strings.Fields(ldflags)
	for i := 0; i < len(fields); i++ {
		field := fields[i]
		value := strings.TrimPrefix(field, "-X=")
		if field == "-X" && i+1 < len(fields) {
			i++
			value = fields[i]
		} else if !strings.HasPrefix(field, "-X=") {
			continue
		}
		if strings.HasPrefix(value, stamp) {
			count++
			if field != "-X="+stamp+release.Canonical {
				return fmt.Errorf("noncanonical or mismatched version stamp: want -X=%s%s", stamp, release.Canonical)
			}
		}
	}
	if count != 1 {
		return fmt.Errorf("want exactly one canonical version stamp, found %d", count)
	}
	return nil
}

func validateResources(set *winres.ResourceSet, release buildmeta.ParsedVersion) error {
	versions, manifests := 0, 0
	var invalid error
	set.Walk(func(kind, id winres.Identifier, language uint16, data []byte) bool {
		switch kind {
		case winres.RT_VERSION:
			versions++
			info, err := version.FromBytes(data)
			if err != nil {
				invalid = fmt.Errorf("invalid RT_VERSION: %w", err)
				return false
			}
			if info.FileVersion != release.Windows || info.ProductVersion != release.Windows {
				invalid = fmt.Errorf("RT_VERSION numeric version mismatch")
				return false
			}
			tables := info.Table()
			if len(tables) == 0 {
				invalid = fmt.Errorf("RT_VERSION strings missing")
				return false
			}
			for _, table := range tables {
				if table == nil || (*table)[version.FileVersion] != release.Canonical || (*table)[version.ProductVersion] != release.Canonical {
					invalid = fmt.Errorf("RT_VERSION string version mismatch")
					return false
				}
			}
		case winres.RT_MANIFEST:
			manifests++
			invalid = validateManifest(data, release)
		}
		return invalid == nil
	})
	if invalid != nil {
		return invalid
	}
	if versions == 0 || manifests == 0 {
		return fmt.Errorf("required RT_VERSION or RT_MANIFEST missing")
	}
	return nil
}

func validateManifest(data []byte, release buildmeta.ParsedVersion) error {
	manifest, err := winres.AppManifestFromXML(data)
	if err != nil {
		return fmt.Errorf("invalid RT_MANIFEST: %w", err)
	}
	if manifest.Identity.Name != "voice_input" || manifest.Identity.Version != release.Windows || manifest.ExecutionLevel != winres.AsInvoker || manifest.UIAccess || manifest.AutoElevate {
		return fmt.Errorf("unsafe or mismatched RT_MANIFEST")
	}
	// The library tolerates absent/invalid fields. Require an explicit, exact
	// identity version and non-elevated execution level instead of its defaults.
	var explicit struct {
		XMLName  xml.Name `xml:"assembly"`
		Identity []struct {
			Version string `xml:"version,attr"`
		} `xml:"assemblyIdentity"`
		Execution []struct {
			Level    string `xml:"level,attr"`
			UIAccess string `xml:"uiAccess,attr"`
		} `xml:"trustInfo>security>requestedPrivileges>requestedExecutionLevel"`
		AutoElevate []string `xml:"application>windowsSettings>autoElevate"`
	}
	if err := xml.Unmarshal(data, &explicit); err != nil {
		return fmt.Errorf("invalid RT_MANIFEST: %w", err)
	}
	v := release.Windows
	if len(explicit.Identity) != 1 || explicit.Identity[0].Version != fmt.Sprintf("%d.%d.%d.%d", v[0], v[1], v[2], v[3]) || len(explicit.Execution) != 1 || explicit.Execution[0].Level != "asInvoker" || explicit.Execution[0].UIAccess != "false" {
		return fmt.Errorf("RT_MANIFEST requires exact version and explicit asInvoker/uiAccess=false")
	}
	for _, value := range explicit.AutoElevate {
		if strings.TrimSpace(value) != "false" {
			return fmt.Errorf("RT_MANIFEST auto elevation is not disabled")
		}
	}
	return nil
}
