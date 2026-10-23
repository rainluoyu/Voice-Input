package main

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"debug/buildinfo"
	"debug/pe"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/tc-hib/winres"
	"voice_input/internal/buildmeta"
)

func main() {
	binary := flag.String("exe", "bin/voice_input.exe", "Windows executable")
	output := flag.String("out", "", "portable archive (default bin/voice_input-<version>-windows-amd64.zip)")
	rawVersion := flag.String("version", buildmeta.Version, "bare release SemVer")
	flag.Parse()
	release, err := buildmeta.Parse(*rawVersion)
	if err != nil {
		log.Fatal(err)
	}
	if *output == "" {
		*output = "bin/voice_input-" + release.Canonical + "-windows-amd64.zip"
	}
	if err := pack(*binary, *output, ".", release.Canonical); err != nil {
		log.Fatal(err)
	}
	fmt.Println(*output)
}
func pack(binary, output, root, rawVersion string) error {
	release, err := buildmeta.Parse(rawVersion)
	if err != nil {
		return err
	}
	content, err := os.ReadFile(binary)
	if err != nil {
		return err
	}
	file, err := pe.NewFile(bytes.NewReader(content))
	if err != nil {
		return err
	}
	_, isPE64 := file.OptionalHeader.(*pe.OptionalHeader64)
	if file.Machine != pe.IMAGE_FILE_MACHINE_AMD64 || !isPE64 || file.Characteristics&pe.IMAGE_FILE_EXECUTABLE_IMAGE == 0 || file.Characteristics&pe.IMAGE_FILE_DLL != 0 {
		return fmt.Errorf("not a Windows x64 executable")
	}
	build, err := buildinfo.Read(bytes.NewReader(content))
	if err != nil {
		return err
	}
	if err := validateBuild(build, release); err != nil {
		return err
	}
	resources, err := winres.LoadFromEXE(bytes.NewReader(content))
	if err != nil {
		return fmt.Errorf("Windows resources missing or invalid: %w", err)
	}
	if err := validateResources(resources, release); err != nil {
		return err
	}
	sum := sha256.Sum256(content)
	metadata, _ := json.MarshalIndent(map[string]any{"project": "voice_input", "version": release.Canonical, "go": build.GoVersion, "sha256": hex.EncodeToString(sum[:]), "platform": "windows/amd64", "desktop_acceptance": "pending", "signed": false}, "", "  ")
	out, err := os.Create(output)
	if err != nil {
		return err
	}
	archive := zip.NewWriter(out)
	successful := false
	defer func() {
		_ = archive.Close()
		_ = out.Close()
		if !successful {
			_ = os.Remove(output)
		}
	}()
	add := func(name string, data []byte) error {
		writer, err := archive.Create(name)
		if err != nil {
			return err
		}
		_, err = writer.Write(data)
		return err
	}
	if err = add("voice_input/voice_input.exe", content); err != nil {
		return err
	}
	for _, path := range []string{"LICENSE", "README.md", "README.zh-CN.md", "docs/images/en/settings.png", "docs/images/en/general.png", "docs/images/en/prompts.png", "docs/images/en/dialogue.png", "docs/images/zh-CN/settings.png", "docs/images/zh-CN/general.png", "docs/images/zh-CN/prompts.png", "docs/images/zh-CN/dialogue.png", "docs/USER_GUIDE.md", "docs/RELEASE_NOTES.md", "docs/TEST_MATRIX.md", "docs/DEVELOPMENT.md", "docs/DICTATION_QUALITY_CASES.md", "docs/REAL_SERVICE_ACCEPTANCE.md", "docs/SECURITY_CHECK.md", "tests/target.html"} {
		data, err := os.ReadFile(filepath.Join(root, path))
		if err != nil {
			return err
		}
		if err = add("voice_input/"+filepath.ToSlash(path), data); err != nil {
			return err
		}
	}
	if err = add("voice_input/BUILDINFO.json", metadata); err != nil {
		return err
	}
	goLicense, err := os.ReadFile(filepath.Join(runtime.GOROOT(), "LICENSE"))
	if err != nil {
		return err
	}
	if err = add("voice_input/licenses/Go-LICENSE", goLicense); err != nil {
		return err
	}
	// Include the licenses of shipped Go dependencies, not credentials/config or
	// development directories. Module directories come from the local Go cache.
	for _, dependency := range build.Deps {
		cmd := exec.Command("go", "list", "-m", "-json", dependency.Path)
		cmd.Dir = root
		info, err := cmd.Output()
		if err != nil {
			return fmt.Errorf("cannot resolve dependency license: %s", dependency.Path)
		}
		var module struct{ Dir string }
		if json.Unmarshal(info, &module) != nil || module.Dir == "" {
			return fmt.Errorf("module directory unavailable")
		}
		if dependency.Path == "github.com/wailsapp/wails/v3" {
			for _, relative := range []string{"internal/webview2/webviewloader/LICENSE", "internal/go-common-file-dialog/LICENSE"} {
				license, err := os.ReadFile(filepath.Join(module.Dir, filepath.FromSlash(relative)))
				if err != nil {
					return err
				}
				name := "voice_input/licenses/" + strings.ReplaceAll(dependency.Path, "/", "_") + "/" + relative
				if err = add(name, license); err != nil {
					return err
				}
			}
		}
		entries, err := os.ReadDir(module.Dir)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			name := strings.ToUpper(entry.Name())
			if entry.IsDir() || !(strings.HasPrefix(name, "LICENSE") || strings.HasPrefix(name, "COPYING") || strings.HasPrefix(name, "NOTICE")) {
				continue
			}
			source, err := os.Open(filepath.Join(module.Dir, entry.Name()))
			if err != nil {
				return err
			}
			data, readErr := io.ReadAll(io.LimitReader(source, 1<<20))
			source.Close()
			if readErr != nil {
				return readErr
			}
			path := "voice_input/licenses/" + strings.ReplaceAll(dependency.Path, "/", "_") + "/" + entry.Name()
			if err = add(path, data); err != nil {
				return err
			}
		}
	}
	if err = archive.Close(); err != nil {
		return err
	}
	if err = out.Close(); err != nil {
		return err
	}
	successful = true
	checksum, err := sha256File(output)
	if err != nil {
		return err
	}
	return os.WriteFile(output+".sha256", []byte(fmt.Sprintf("%x  %s\n", checksum, filepath.Base(output))), 0644)
}
func sha256File(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err = io.Copy(hash, file); err != nil {
		return nil, err
	}
	return hash.Sum(nil), nil
}
