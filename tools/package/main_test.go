package main

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"debug/buildinfo"
	"debug/pe"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strings"
	"testing"

	"github.com/tc-hib/winres"
	"github.com/tc-hib/winres/version"
	"voice_input/internal/buildmeta"
)

// Keep this list independent of the production whitelist.
var requiredDocuments = []string{
	"LICENSE", "README.md", "README.zh-CN.md", "docs/USER_GUIDE.md", "docs/RELEASE_NOTES.md",
	"docs/TEST_MATRIX.md", "docs/DEVELOPMENT.md", "docs/DICTATION_QUALITY_CASES.md",
	"docs/REAL_SERVICE_ACCEPTANCE.md", "docs/SECURITY_CHECK.md", "tests/target.html",
	"docs/images/settings.png", "docs/images/prompts.png", "docs/images/dialogue.png",
}

func writeFixture(t *testing.T, root, path, content string) {
	t.Helper()
	full := filepath.Join(root, path)
	if err := os.MkdirAll(filepath.Dir(full), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func fixtureResources(t *testing.T, rawVersion string, mutate func(*version.Info, *winres.AppManifest)) *winres.ResourceSet {
	t.Helper()
	release, err := buildmeta.Parse(rawVersion)
	if err != nil {
		t.Fatal(err)
	}
	info := version.Info{FileVersion: release.Windows, ProductVersion: release.Windows, Type: version.App}
	for _, key := range []string{version.FileVersion, version.ProductVersion} {
		if err := info.Set(0, key, rawVersion); err != nil {
			t.Fatal(err)
		}
	}
	manifest := winres.AppManifest{Identity: winres.AssemblyIdentity{Name: "voice_input", Version: release.Windows}, ExecutionLevel: winres.AsInvoker, Compatibility: winres.Win10AndAbove}
	if mutate != nil {
		mutate(&info, &manifest)
	}
	set := &winres.ResourceSet{}
	set.SetVersionInfo(info)
	set.SetManifest(manifest)
	return set
}

func compileFixture(t *testing.T, root, name, ldflags string, resources *winres.ResourceSet) string {
	t.Helper()
	object, err := os.Create(filepath.Join(root, "resources_windows_amd64.syso"))
	if err != nil {
		t.Fatal(err)
	}
	if err = resources.WriteObject(object, winres.ArchAMD64); err != nil {
		t.Fatal(err)
	}
	if err = object.Close(); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(root, name+".exe")
	command := exec.Command("go", "build", "-trimpath", "-buildvcs=false", "-ldflags="+ldflags, "-o", exe, ".")
	command.Dir = root
	command.Env = append(os.Environ(), "GOOS=windows", "GOARCH=amd64", "CGO_ENABLED=0", "GOTOOLCHAIN=local", "GOPROXY=off", "GOSUMDB=off", "GOWORK=off")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("fixture compile: %v %s", err, output)
	}
	return exe
}

func rewriteResources(t *testing.T, original, output string, set *winres.ResourceSet) {
	t.Helper()
	source, err := os.Open(original)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	destination, err := os.Create(output)
	if err != nil {
		t.Fatal(err)
	}
	defer destination.Close()
	if err := set.WriteToEXE(destination, source); err != nil {
		t.Fatal(err)
	}
}

func TestPortableArchivesAndExecutableValidation(t *testing.T) {
	root := t.TempDir()
	for _, path := range requiredDocuments {
		writeFixture(t, root, path, "fixture")
	}
	writeFixture(t, root, "go.mod", "module voice_input\n\ngo 1.25.0\n")
	writeFixture(t, root, "internal/buildmeta/version.go", "package buildmeta\nvar Version = \"0.2.0\"\n")
	writeFixture(t, root, "main.go", "package main\nimport (\"os\"; \"voice_input/internal/buildmeta\")\nfunc main(){ _, _ = os.Stdout.WriteString(buildmeta.Version) }\n")
	const stable = "12.34.56"
	const stamp = "-X=voice_input/internal/buildmeta.Version="
	var stableExe string
	for _, rawVersion := range []string{stable, stable + "-rc.1"} {
		t.Run(rawVersion, func(t *testing.T) {
			exe := compileFixture(t, root, rawVersion, "-H=windowsgui "+stamp+rawVersion, fixtureResources(t, rawVersion, nil))
			if rawVersion == stable {
				stableExe = exe
			}
			build, err := buildinfo.ReadFile(exe)
			if err != nil || len(build.Deps) != 0 {
				t.Fatal("fixture must have no third-party dependencies", err)
			}
			binaryContent, err := os.ReadFile(exe)
			if err != nil || bytes.Contains(binaryContent, []byte(root)) {
				t.Fatal("trimpath fixture contains its private build directory", err)
			}
			const private = "private-fixture-do-not-package-configuration-or-history"
			for _, path := range []string{
				"config.json", "bin/config.json", ".env", ".env.local", ".git/config",
				".claude/worktrees/local/notes.md", "reports/acceptance.json", "recordings/private.wav",
				"screenshots/local.png", "docs/MILESTONE_STATUS.md", "docs/acceptance/local.json",
				"docs/images/unapproved.png", "docs/images/nested/settings.png",
				"docs/WINDOWS_DEPLOYMENT_2000-01-01.md", "voice_input.md", "voice_input.md:Zone.Identifier",
			} {
				if strings.Contains(path, ":") && os.PathSeparator == '\\' {
					continue
				}
				writeFixture(t, root, path, private)
			}
			archive := filepath.Join(root, rawVersion+".zip")
			if err := pack(exe, archive, root, rawVersion); err != nil {
				t.Fatal(err)
			}
			checkArchive(t, archive, exe, rawVersion, private)
			if rawVersion == stable {
				tool := filepath.Join(root, "package-tool")
				command := exec.Command("go", "build", "-o", tool, ".")
				if output, err := command.CombinedOutput(); err != nil {
					t.Fatalf("compile package CLI: %v %s", err, output)
				}
				command = exec.Command(tool, "-exe", exe, "-version", rawVersion)
				command.Dir = root
				wantOutput := "bin/voice_input-" + rawVersion + "-windows-amd64.zip"
				if output, err := command.CombinedOutput(); err != nil || string(output) != wantOutput+"\n" {
					t.Fatalf("package CLI: %q, %v", output, err)
				}
				checkArchive(t, filepath.Join(root, filepath.FromSlash(wantOutput)), exe, rawVersion, private)
			}
		})
	}
	if stableExe == "" {
		t.Fatal("stable fixture unavailable")
	}
	for _, path := range []string{"LICENSE", "README.zh-CN.md", "docs/images/settings.png", "docs/images/prompts.png", "docs/images/dialogue.png"} {
		t.Run("missing-"+path, func(t *testing.T) {
			if err := os.Remove(filepath.Join(root, path)); err != nil {
				t.Fatal(err)
			}
			defer writeFixture(t, root, path, "fixture")
			output := filepath.Join(root, "missing-document.zip")
			if err := pack(stableExe, output, root, stable); err == nil {
				t.Fatal("accepted missing required file", path)
			}
			if _, err := os.Stat(output); !os.IsNotExist(err) {
				t.Fatal("incomplete package was not removed", err)
			}
		})
	}
	for _, test := range []struct {
		name   string
		mutate func(*version.Info, *winres.AppManifest)
	}{
		{"file-numeric-mismatch", func(v *version.Info, _ *winres.AppManifest) { v.FileVersion[2]++ }},
		{"product-numeric-mismatch", func(v *version.Info, _ *winres.AppManifest) { v.ProductVersion[2]++ }},
		{"file-string-mismatch", func(v *version.Info, _ *winres.AppManifest) { _ = v.Set(0, version.FileVersion, "0.2.0") }},
		{"product-string-mismatch", func(v *version.Info, _ *winres.AppManifest) { _ = v.Set(0, version.ProductVersion, "0.2.0") }},
		{"missing-version-strings", func(v *version.Info, _ *winres.AppManifest) {
			*v = version.Info{FileVersion: v.FileVersion, ProductVersion: v.ProductVersion}
		}},
		{"manifest-version-mismatch", func(_ *version.Info, m *winres.AppManifest) { m.Identity.Version[2]++ }},
		{"manifest-no-identity", func(_ *version.Info, m *winres.AppManifest) { m.Identity.Name = "" }},
		{"administrator", func(_ *version.Info, m *winres.AppManifest) { m.ExecutionLevel = winres.RequireAdministrator }},
		{"highest-available", func(_ *version.Info, m *winres.AppManifest) { m.ExecutionLevel = winres.HighestAvailable }},
		{"ui-access", func(_ *version.Info, m *winres.AppManifest) { m.UIAccess = true }},
		{"auto-elevate", func(_ *version.Info, m *winres.AppManifest) { m.AutoElevate = true }},
	} {
		t.Run(test.name, func(t *testing.T) {
			exe := filepath.Join(root, test.name+".exe")
			rewriteResources(t, stableExe, exe, fixtureResources(t, stable, test.mutate))
			assertPackRejected(t, exe, root, stable)
		})
	}
	for _, kind := range []winres.Identifier{winres.RT_VERSION, winres.RT_MANIFEST, nil} {
		t.Run("missing-resource-"+kindName(kind), func(t *testing.T) {
			set := &winres.ResourceSet{}
			fixtureResources(t, stable, nil).Walk(func(current, id winres.Identifier, lang uint16, data []byte) bool {
				if kind != nil && current != kind {
					if err := set.Set(current, id, lang, data); err != nil {
						t.Fatal(err)
					}
				}
				return true
			})
			exe := filepath.Join(root, "missing-resource.exe")
			rewriteResources(t, stableExe, exe, set)
			assertPackRejected(t, exe, root, stable)
		})
	}
	t.Run("wrong-architecture", func(t *testing.T) {
		data, err := os.ReadFile(stableExe)
		if err != nil {
			t.Fatal(err)
		}
		peOffset := binary.LittleEndian.Uint32(data[0x3c:])
		binary.LittleEndian.PutUint16(data[peOffset+4:], pe.IMAGE_FILE_MACHINE_I386)
		exe := filepath.Join(root, "wrong-architecture.exe")
		if err := os.WriteFile(exe, data, 0600); err != nil {
			t.Fatal(err)
		}
		assertPackRejected(t, exe, root, stable)
	})
	// Both release fixtures use -trimpath, which intentionally omits -ldflags
	// from Go buildinfo. Actual version resources remain the release boundary.
	assertPackRejected(t, stableExe, root, "0.2.0")
}

func kindName(kind winres.Identifier) string {
	if kind == winres.RT_VERSION {
		return "version"
	}
	if kind == winres.RT_MANIFEST {
		return "manifest"
	}
	return "all"
}

func assertPackRejected(t *testing.T, exe, root, rawVersion string) {
	t.Helper()
	archive := filepath.Join(root, "rejected.zip")
	if err := pack(exe, archive, root, rawVersion); err == nil {
		t.Fatal("invalid executable accepted", exe)
	}
	for _, path := range []string{archive, archive + ".sha256"} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatal("rejected executable produced an archive or checksum", path, err)
		}
	}
}

func checkArchive(t *testing.T, archive, exe, rawVersion, private string) {
	t.Helper()
	reader, err := zip.OpenReader(archive)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	expected := map[string]bool{
		"voice_input/voice_input.exe": false, "voice_input/BUILDINFO.json": false,
		"voice_input/licenses/Go-LICENSE": false,
	}
	for _, path := range requiredDocuments {
		expected["voice_input/"+path] = false
	}
	content, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	binarySum := sha256.Sum256(content)
	for _, file := range reader.File {
		if seen, allowed := expected[file.Name]; !allowed || seen || strings.Contains(file.Name, "..") || filepath.IsAbs(file.Name) {
			t.Fatalf("unexpected, duplicate or unsafe packaged file: %s", file.Name)
		}
		expected[file.Name] = true
		stream, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, readErr := io.ReadAll(stream)
		closeErr := stream.Close()
		if readErr != nil || closeErr != nil {
			t.Fatal("cannot read packaged file", readErr, closeErr)
		}
		if bytes.Contains(data, []byte(private)) {
			t.Fatal("private fixture leaked into package", file.Name)
		}
		if file.Name == "voice_input/voice_input.exe" && !bytes.Equal(data, content) {
			t.Fatal("packaged executable differs from validated input")
		}
		if file.Name == "voice_input/BUILDINFO.json" {
			var info struct {
				Project           string `json:"project"`
				Version           string `json:"version"`
				SHA256            string `json:"sha256"`
				Platform          string `json:"platform"`
				DesktopAcceptance string `json:"desktop_acceptance"`
				Signed            *bool  `json:"signed"`
			}
			if err := json.Unmarshal(data, &info); err != nil || info.SHA256 != hex.EncodeToString(binarySum[:]) || info.Version != rawVersion || info.Project != "voice_input" || info.Platform != "windows/amd64" || info.DesktopAcceptance != "pending" || info.Signed == nil || *info.Signed {
				t.Fatal("wrong build metadata", info, err)
			}
		}
	}
	for name, found := range expected {
		if !found {
			t.Fatal("required packaged file missing", name)
		}
	}
	sum, err := sha256File(archive)
	if err != nil {
		t.Fatal(err)
	}
	checksum, err := os.ReadFile(archive + ".sha256")
	if err != nil || string(checksum) != hex.EncodeToString(sum)+"  "+filepath.Base(archive)+"\n" {
		t.Fatal("wrong archive checksum", err)
	}
}

func TestValidateBuildSettingsAndStamp(t *testing.T) {
	release, err := buildmeta.Parse("12.34.56-rc.1")
	if err != nil {
		t.Fatal(err)
	}
	const canonical = "-X=voice_input/internal/buildmeta.Version=12.34.56-rc.1"
	valid := []debug.BuildSetting{{Key: "GOOS", Value: "windows"}, {Key: "GOARCH", Value: "amd64"}, {Key: "CGO_ENABLED", Value: "0"}, {Key: "-ldflags", Value: "-H=windowsgui " + canonical}}
	if err := validateBuild(&debug.BuildInfo{Settings: valid}, release); err != nil {
		t.Fatal(err)
	}
	for _, flags := range []string{"", "-H=windowsgui", canonical + " " + canonical, canonical + " -X=voice_input/internal/buildmeta.Version=0.2.0", canonical + " -X 'voice_input/internal/buildmeta.Version=0.2.0'", "-X voice_input/internal/buildmeta.Version=12.34.56-rc.1", "-X=voice_input/internal/buildmeta.Version=v12.34.56-rc.1", "-X=other.Version=12.34.56-rc.1"} {
		settings := append([]debug.BuildSetting(nil), valid...)
		settings[3].Value = flags
		if err := validateBuild(&debug.BuildInfo{Settings: settings}, release); err == nil {
			t.Fatal("accepted invalid linker flags", flags)
		}
	}
	for i, setting := range valid {
		missing := append([]debug.BuildSetting(nil), valid[:i]...)
		missing = append(missing, valid[i+1:]...)
		if err := validateBuild(&debug.BuildInfo{Settings: missing}, release); (err == nil) != (setting.Key == "-ldflags") {
			t.Fatal("wrong handling of omitted setting", setting.Key, err)
		}
		duplicate := append(append([]debug.BuildSetting(nil), valid...), setting)
		if err := validateBuild(&debug.BuildInfo{Settings: duplicate}, release); err == nil {
			t.Fatal("accepted duplicate setting", setting.Key)
		}
		wrong := append([]debug.BuildSetting(nil), valid...)
		wrong[i].Value = "wrong"
		if err := validateBuild(&debug.BuildInfo{Settings: wrong}, release); err == nil {
			t.Fatal("accepted wrong setting", setting.Key)
		}
	}
}

func TestValidateManifestRejectsCoercedDefaults(t *testing.T) {
	release, err := buildmeta.Parse("12.34.56")
	if err != nil {
		t.Fatal(err)
	}
	var manifest []byte
	fixtureResources(t, release.Canonical, nil).Walk(func(kind, id winres.Identifier, language uint16, data []byte) bool {
		if kind == winres.RT_MANIFEST {
			manifest = data
		}
		return true
	})
	if err := validateManifest(manifest, release); err != nil {
		t.Fatal(err)
	}
	for _, replacement := range [][2]string{
		{`version="12.34.56.0"`, `version="12.34.56.0.1"`},
		{`level="asInvoker"`, `level="unknown"`},
		{`level="asInvoker"`, ""},
		{`uiAccess="false"`, `uiAccess="1"`},
		{`uiAccess="false"`, ""},
		{`<assembly `, `<not-assembly `},
	} {
		changed := bytes.ReplaceAll(manifest, []byte(replacement[0]), []byte(replacement[1]))
		if bytes.Equal(changed, manifest) {
			t.Fatal("fixture replacement did not apply", replacement)
		}
		if err := validateManifest(changed, release); err == nil {
			t.Fatal("accepted missing or malformed manifest field", replacement)
		}
	}
}

func TestPackRejectsNonExecutable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "not.exe")
	if err := os.WriteFile(path, []byte("not a PE"), 0600); err != nil {
		t.Fatal(err)
	}
	if pack(path, path+".zip", ".", buildmeta.Version) == nil {
		t.Fatal("invalid PE accepted")
	}
	if pack(path, path+".zip", ".", "v1.2.3") == nil {
		t.Fatal("invalid requested version accepted")
	}
}
