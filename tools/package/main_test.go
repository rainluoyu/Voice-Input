package main

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tc-hib/winres"
)

func TestPortableArchiveWhitelistsFilesAndChecksums(t *testing.T) {
	root := t.TempDir()
	documents := []string{
		"README.md", "README.zh-CN.md", "docs/USER_GUIDE.md", "docs/RELEASE_NOTES.md",
		"docs/TEST_MATRIX.md", "docs/DEVELOPMENT.md", "docs/DICTATION_QUALITY_CASES.md",
		"docs/REAL_SERVICE_ACCEPTANCE.md", "docs/SECURITY_CHECK.md", "tests/target.html",
	}
	write := func(path, content string) {
		t.Helper()
		full := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(full), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range documents {
		write(path, "fixture")
	}
	write("go.mod", "module fixture\n\ngo 1.25.0\n")
	write("main.go", "package main\nfunc main(){}\n")
	resources := &winres.ResourceSet{}
	resources.SetManifest(winres.AppManifest{ExecutionLevel: winres.AsInvoker, Compatibility: winres.Win10AndAbove})
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
	exe := filepath.Join(root, "fixture.exe")
	command := exec.Command("go", "build", "-o", exe, ".")
	command.Dir = root
	command.Env = append(os.Environ(), "GOOS=windows", "GOARCH=amd64", "CGO_ENABLED=0")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("fixture compile: %v %s", err, output)
	}
	const private = "private-fixture-do-not-package-configuration-or-history"
	for _, path := range []string{
		"config.json", "bin/config.json", ".env", ".env.local", ".git/config",
		".claude/worktrees/local/notes.md", "reports/acceptance.json", "recordings/private.wav",
		"screenshots/local.png", "docs/MILESTONE_STATUS.md", "docs/acceptance/local.json",
		"docs/WINDOWS_DEPLOYMENT_2000-01-01.md", "voice_input.md", "voice_input.md:Zone.Identifier",
	} {
		// Colons name alternate streams on Windows; that case is covered on Unix.
		if strings.Contains(path, ":") && os.PathSeparator == '\\' {
			continue
		}
		write(path, private)
	}
	archive := filepath.Join(root, "package.zip")
	if err := pack(exe, archive, root); err != nil {
		t.Fatal(err)
	}
	reader, err := zip.OpenReader(archive)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	expected := map[string]bool{
		"voice_input/voice_input.exe": false, "voice_input/BUILDINFO.json": false,
		"voice_input/licenses/Go-LICENSE": false,
	}
	for _, path := range documents {
		expected["voice_input/"+path] = false
	}
	binary, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	binarySum := sha256.Sum256(binary)
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
		if file.Name == "voice_input/BUILDINFO.json" {
			var info struct {
				SHA256 string `json:"sha256"`
			}
			if err := json.Unmarshal(data, &info); err != nil || info.SHA256 != hex.EncodeToString(binarySum[:]) {
				t.Fatal("wrong executable checksum", err)
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
	if err != nil || string(checksum) != hex.EncodeToString(sum)+"  package.zip\n" {
		t.Fatal("wrong archive checksum", err)
	}
	if err := os.Remove(filepath.Join(root, "README.zh-CN.md")); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(root, "missing-document.zip")
	if err := pack(exe, missing, root); err == nil {
		t.Fatal("package accepted missing translated README")
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatal("incomplete package was not removed", err)
	}
}

func TestPackRejectsNonExecutable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "not.exe")
	if err := os.WriteFile(path, []byte("not a PE"), 0600); err != nil {
		t.Fatal(err)
	}
	if pack(path, path+".zip", ".") == nil {
		t.Fatal("invalid PE accepted")
	}
}
