package branding

import (
	"bytes"
	"image/png"
	"os"
	"testing"
)

func TestDisplayName(t *testing.T) {
	if Name != "voice input" {
		t.Fatalf("display name must be exactly voice input, got %q", Name)
	}
}

func TestIconPNG(t *testing.T) {
	image, err := png.Decode(bytes.NewReader(PNG()))
	if err != nil {
		t.Fatal(err)
	}
	if image.Bounds().Dx() != 32 || image.Bounds().Dy() != 32 {
		t.Fatal("tray icon dimensions")
	}
	_, _, _, alpha := image.At(14, 16).RGBA()
	if alpha == 0 {
		t.Fatal("waveform is invisible")
	}
}

func TestFrontendIconMatchesTray(t *testing.T) {
	data, err := os.ReadFile("../../frontend/public/app-icon.png")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, PNG()) {
		t.Fatal("frontend icon differs from tray; regenerate with go run ./tools/winresource -png frontend/public/app-icon.png")
	}
}
