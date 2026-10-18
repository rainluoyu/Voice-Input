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
)

func main() {
	binary := flag.String("exe", "bin/voice_input.exe", "Windows executable")
	output := flag.String("out", "bin/voice_input-0.2.0-windows-amd64.zip", "portable archive")
	flag.Parse()
	if err := pack(*binary, *output, "."); err != nil {
		log.Fatal(err)
	}
	fmt.Println(*output)
}
func pack(binary, output, root string) error {
	file, err := pe.Open(binary)
	if err != nil {
		return err
	}
	if file.Machine != pe.IMAGE_FILE_MACHINE_AMD64 {
		file.Close()
		return fmt.Errorf("not a Windows x64 executable")
	}
	section := file.Section(".rsrc")
	if section == nil {
		file.Close()
		return fmt.Errorf("Windows resources/manifest missing")
	}
	resourceData, err := section.Data()
	file.Close()
	if err != nil || !bytes.Contains(resourceData, []byte(`level="asInvoker"`)) || bytes.Contains(resourceData, []byte("requireAdministrator")) {
		return fmt.Errorf("unsafe or invalid Windows manifest")
	}
	build, err := buildinfo.ReadFile(binary)
	if err != nil {
		return err
	}
	content, err := os.ReadFile(binary)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(content)
	metadata, _ := json.MarshalIndent(map[string]any{"project": "voice_input", "version": "0.2.0", "go": build.GoVersion, "sha256": hex.EncodeToString(sum[:]), "platform": "windows/amd64", "desktop_acceptance": "pending", "signed": false}, "", "  ")
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
	for _, path := range []string{"README.md", "README.zh-CN.md", "docs/USER_GUIDE.md", "docs/RELEASE_NOTES.md", "docs/TEST_MATRIX.md", "docs/DEVELOPMENT.md", "docs/DICTATION_QUALITY_CASES.md", "docs/REAL_SERVICE_ACCEPTANCE.md", "docs/SECURITY_CHECK.md", "tests/target.html"} {
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
