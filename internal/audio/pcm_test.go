package audio

import (
	"encoding/binary"
	"math"
	"testing"
)

func tone(rate, seconds int) []float32 {
	data := make([]float32, rate*seconds)
	for i := range data {
		data[i] = float32(.2 * math.Sin(float64(i)*2*math.Pi*300/float64(rate)))
	}
	return data
}
func TestWAVAndQuiet(t *testing.T) {
	data, err := Encode(tone(SampleRate, 1))
	if err != nil {
		t.Fatal(err)
	}
	if string(data.WAV[:4]) != "RIFF" || string(data.WAV[8:12]) != "WAVE" || binary.LittleEndian.Uint32(data.WAV[24:28]) != SampleRate || len(data.WAV) != 32044 {
		t.Fatal("invalid wav header")
	}
	for _, samples := range [][]float32{nil, make([]float32, SampleRate), {float32(math.NaN())}} {
		if _, err := Encode(samples); err == nil {
			t.Fatal("invalid audio accepted")
		}
	}
}
func TestStreamingResample(t *testing.T) {
	source := tone(48000, 1)
	all := NewResampler(48000, 2)
	_ = all.Append(source)
	chunks := NewResampler(48000, 2)
	for start := 0; start < len(source); start += 137 {
		_ = chunks.Append(source[start:min(start+137, len(source))])
	}
	if len(all.output) != 16000 || len(chunks.output) != 16000 {
		t.Fatal("resample length")
	}
	for i := range all.output {
		if all.output[i] != chunks.output[i] {
			t.Fatal("chunk boundary changed output")
		}
	}
	if _, err := chunks.Finish(); err != nil {
		t.Fatal(err)
	}
}
func TestDecodeNativeFormats(t *testing.T) {
	fixtures := []struct {
		data []byte
		f    Format
		want float32
	}{
		{[]byte{0, 0x40, 0, 0x40}, Format{48000, 2, 16, false}, .5},
		{[]byte{0, 0, 0x40}, Format{48000, 1, 24, false}, .5},
		{[]byte{0, 0, 0, 0x40}, Format{48000, 1, 32, false}, .5},
		{[]byte{0, 0, 0, 0x3f}, Format{48000, 1, 32, true}, .5},
	}
	for _, tt := range fixtures {
		got, err := Decode(tt.data, tt.f)
		if err != nil || len(got) != 1 || got[0] != tt.want {
			t.Fatalf("decode: %v %v", got, err)
		}
	}
	if _, err := Decode([]byte{1}, Format{48000, 1, 16, false}); err == nil {
		t.Fatal("partial frame accepted")
	}
}

func TestPackedMixFormat(t *testing.T) {
	raw := make([]byte, 40)
	binary.LittleEndian.PutUint16(raw[0:2], 0xfffe)
	binary.LittleEndian.PutUint16(raw[2:4], 2)
	binary.LittleEndian.PutUint32(raw[4:8], 48000)
	binary.LittleEndian.PutUint16(raw[12:14], 8)
	binary.LittleEndian.PutUint16(raw[14:16], 32)
	binary.LittleEndian.PutUint16(raw[16:18], 22)
	binary.LittleEndian.PutUint32(raw[24:28], 3)
	copy(raw[28:], []byte{0, 0, 0x10, 0, 0x80, 0, 0, 0xaa, 0, 0x38, 0x9b, 0x71})
	format, err := ParseMixFormat(raw)
	if err != nil || !format.Float || format.Rate != 48000 || format.Channels != 2 {
		t.Fatal("valid float mix rejected")
	}
	binary.LittleEndian.PutUint32(raw[24:28], 0x10001)
	if _, err = ParseMixFormat(raw); err == nil {
		t.Fatal("unknown GUID subtype truncated into PCM")
	}
	binary.LittleEndian.PutUint16(raw[0:2], 1)
	binary.LittleEndian.PutUint16(raw[12:14], 4)
	binary.LittleEndian.PutUint16(raw[14:16], 16)
	if _, err = ParseMixFormat(raw[:18]); err != nil {
		t.Fatal("PCM16 packed layout rejected")
	}
	if _, err = ParseMixFormat(raw[:16]); err == nil {
		t.Fatal("short format accepted")
	}
}
