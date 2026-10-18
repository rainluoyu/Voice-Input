package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/tc-hib/winres"
	"github.com/tc-hib/winres/version"
	"voice_input/internal/branding"
	"voice_input/internal/buildmeta"
)

func resources(release buildmeta.ParsedVersion) (*winres.ResourceSet, error) {
	set := &winres.ResourceSet{}
	set.SetManifest(winres.AppManifest{Identity: winres.AssemblyIdentity{Name: "voice_input", Version: release.Windows}, Description: branding.Name, Compatibility: winres.Win10AndAbove, ExecutionLevel: winres.AsInvoker, UIAccess: false, AutoElevate: false, DPIAwareness: winres.DPIPerMonitorV2, LongPathAware: true, UseCommonControlsV6: true})
	icon, err := winres.NewIconFromResizedImage(branding.Icon(), []int{16, 32, 48, 64, 128, 256})
	if err != nil {
		return nil, err
	}
	if err = set.SetIcon(winres.ID(1), icon); err != nil {
		return nil, err
	}
	vi := version.Info{FileVersion: release.Windows, ProductVersion: release.Windows, Type: version.App}
	vi.Flags.Prerelease = strings.Contains(release.Canonical, "-")
	for key, value := range map[string]string{version.ProductName: branding.Name, version.FileDescription: branding.Name, version.FileVersion: release.Canonical, version.ProductVersion: release.Canonical, version.OriginalFilename: "voice_input.exe"} {
		if err = vi.Set(0, key, value); err != nil {
			return nil, err
		}
	}
	set.SetVersionInfo(vi)
	return set, nil
}
func main() {
	path := flag.String("out", "cmd/voice_input/resources_windows_amd64.syso", "output object file")
	pngPath := flag.String("png", "", "also export the shared app PNG to this path")
	rawVersion := flag.String("version", buildmeta.Version, "bare release SemVer")
	printVersion := flag.Bool("print-version", false, "validate and print only the release version")
	flag.Parse()
	release, err := buildmeta.Parse(*rawVersion)
	if err != nil {
		log.Fatal(err)
	}
	if *printVersion {
		fmt.Println(release.Canonical)
		return
	}
	if *pngPath != "" {
		if err := os.MkdirAll(filepath.Dir(*pngPath), 0755); err != nil {
			log.Fatal(err)
		}
		if err := os.WriteFile(*pngPath, branding.PNG(), 0644); err != nil {
			log.Fatal(err)
		}
	}
	set, err := resources(release)
	if err != nil {
		log.Fatal(err)
	}
	out, err := os.Create(*path)
	if err != nil {
		log.Fatal(err)
	}
	if err = set.WriteObject(out, winres.ArchAMD64); err != nil {
		_ = out.Close()
		log.Fatal(err)
	}
	if err = out.Close(); err != nil {
		log.Fatal(err)
	}
}
