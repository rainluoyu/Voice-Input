package audio

import (
	"encoding/binary"
	"errors"
	"math"
	"testing"
	"time"
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
func TestEncodeActivity(t *testing.T) {
	activity := func(length int, level float32, spans ...[2]int) []float32 {
		samples := make([]float32, length)
		for _, span := range spans {
			for i := span[0]; i < span[1]; i++ {
				samples[i] = level
			}
		}
		return samples
	}
	for _, tt := range []struct {
		name    string
		samples []float32
		quiet   bool
	}{
		{"empty", nil, true},
		{"299ms", activity(4784, .1, [2]int{0, 4784}), true},
		{"300ms_silence", make([]float32, 4800), true},
		{"300ms_normal", tone(SampleRate, 1)[:4800], false},
		{"near_silence", activity(16000, math.Nextafter32(.003, 0), [2]int{0, 16000}), true},
		{"frame_rms_threshold", activity(4800, .003, [2]int{0, 4800}), false},
		{"single_strong_impulse", activity(16000, 1, [2]int{160, 161}), true},
		{"cross_frame_impulse", activity(16000, 1, [2]int{319, 321}), true},
		{"dispersed_60ms", activity(16000, .1, [2]int{0, 320}, [2]int{3200, 3520}, [2]int{6400, 6720}), true},
		{"dispersed_80ms", activity(16000, .003, [2]int{0, 320}, [2]int{3200, 3520}, [2]int{6400, 6720}, [2]int{9600, 9920}), false},
		{"1279_active_samples_with_tail", activity(5119, .003, [2]int{0, 960}, [2]int{4800, 5119}), true},
		{"1280_active_samples", activity(4800, .003, [2]int{0, 1280}), false},
		{"1281_active_samples_with_tail", activity(4801, .003, [2]int{0, 1280}, [2]int{4800, 4801}), false},
		{"long_silence_with_short_phrase", activity(160000, .01, [2]int{80000, 81600}), false},
		{"normal", tone(SampleRate, 1), false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			data, err := Encode(tt.samples)
			if tt.quiet {
				if !errors.Is(err, ErrQuiet) || len(data.WAV) != 0 {
					t.Fatalf("quiet recording produced %d WAV bytes, error %v", len(data.WAV), err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var energy float64
			for _, sample := range tt.samples {
				energy += float64(sample) * float64(sample)
			}
			wantRMS := math.Sqrt(energy / float64(len(tt.samples)))
			if len(data.WAV) != 44+2*len(tt.samples) || data.Duration != time.Duration(len(tt.samples))*time.Second/SampleRate || math.Abs(data.RMS-wantRMS) > 1e-12 {
				t.Fatalf("audio metadata changed: duration %v, RMS %g (want %g), WAV bytes %d", data.Duration, data.RMS, wantRMS, len(data.WAV))
			}
			if tt.name == "long_silence_with_short_phrase" && data.RMS >= .003 {
				t.Fatal("fixture does not exercise a phrase diluted by silence")
			}
		})
	}
}

func TestEncodeRejectsNonFiniteSamples(t *testing.T) {
	for _, tt := range []struct {
		name  string
		value float32
	}{
		{"NaN", float32(math.NaN())},
		{"positive_infinity", float32(math.Inf(1))},
		{"negative_infinity", float32(math.Inf(-1))},
	} {
		for _, position := range []struct {
			name  string
			index int
		}{{"first", 0}, {"after_activity_threshold", 1280}, {"tail", 4800}} {
			t.Run(tt.name+"_"+position.name, func(t *testing.T) {
				samples := tone(SampleRate, 1)[:4801]
				samples[position.index] = tt.value
				if data, err := Encode(samples); err == nil || errors.Is(err, ErrQuiet) || len(data.WAV) != 0 {
					t.Fatalf("invalid sample produced %d WAV bytes, error %v", len(data.WAV), err)
				}
			})
		}
	}
}

func TestEncodeSizeLimit(t *testing.T) {
	maxSamples := (MaxWAVBytes - 44) / 2
	samples := make([]float32, maxSamples+1)
	copy(samples, tone(SampleRate, 1))
	data, err := Encode(samples[:maxSamples])
	if err != nil || len(data.WAV) != MaxWAVBytes {
		t.Fatalf("maximum WAV size rejected: %d bytes, error %v", len(data.WAV), err)
	}
	if _, err := Encode(samples); !errors.Is(err, ErrQuiet) {
		t.Fatalf("oversized recording accepted: %v", err)
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
