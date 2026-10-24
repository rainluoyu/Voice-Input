package audio

import (
	"encoding/binary"
	"errors"
	"math"
	"time"

	"voice_input/internal/i18n"
)

const SampleRate = 16000
const MaxWAVBytes = 10 << 20

var ErrUnavailable = errors.New("麦克风不可用：检查设备、隐私权限和独占占用")
var ErrQuiet = errors.New("录音没有内容或时间过短，请重新录音")

type Device struct {
	ID       string        `json:"id"`
	Name     string        `json:"name"`
	NameI18n *i18n.Message `json:"name_i18n,omitempty"`
}
type Data struct {
	WAV      []byte
	Duration time.Duration
	RMS      float64
}
type Format struct {
	Rate     int
	Channels int
	Bits     int
	Float    bool
}

func (f Format) Validate() error {
	if f.Rate < 8000 || f.Rate > 192000 || f.Channels < 1 || f.Channels > 8 || !(f.Bits == 16 || f.Bits == 24 || f.Bits == 32) || f.Float && f.Bits != 32 {
		return errors.New("麦克风音频格式暂不支持")
	}
	return nil
}
func Decode(data []byte, f Format) ([]float32, error) {
	if err := f.Validate(); err != nil {
		return nil, err
	}
	width := f.Bits / 8
	stride := width * f.Channels
	if len(data)%stride != 0 {
		return nil, errors.New("音频帧大小无效")
	}
	mono := make([]float32, len(data)/stride)
	for frame := range mono {
		var sum float64
		for ch := 0; ch < f.Channels; ch++ {
			p := data[frame*stride+ch*width:]
			var value float64
			switch {
			case f.Float:
				value = float64(math.Float32frombits(binary.LittleEndian.Uint32(p)))
			case f.Bits == 16:
				value = float64(int16(binary.LittleEndian.Uint16(p))) / 32768
			case f.Bits == 24:
				v := int32(p[0]) | int32(p[1])<<8 | int32(p[2])<<16
				value = float64(v<<8>>8) / 8388608
			case f.Bits == 32:
				value = float64(int32(binary.LittleEndian.Uint32(p))) / 2147483648
			}
			if math.IsNaN(value) || math.IsInf(value, 0) {
				return nil, errors.New("音频包含无效采样")
			}
			sum += max(-1, min(1, value))
		}
		mono[frame] = float32(sum / float64(f.Channels))
	}
	return mono, nil
}

// Resampler uses streaming linear interpolation. No native-rate recording is
// retained: memory is bounded by the 16 kHz mono output, not channel count.
type Resampler struct {
	rate       int
	index      int64
	next       float64
	previous   float32
	output     []float32
	maxSamples int
}

func NewResampler(rate, maxSeconds int) *Resampler {
	return &Resampler{rate: rate, maxSamples: maxSeconds * SampleRate}
}
func (r *Resampler) Append(samples []float32) error {
	for _, sample := range samples {
		position := float64(r.index)
		for r.next <= position {
			value := sample
			if r.index > 0 {
				fraction := r.next - (position - 1)
				value = r.previous + float32(fraction)*(sample-r.previous)
			}
			if len(r.output) >= r.maxSamples {
				return nil
			}
			r.output = append(r.output, value)
			r.next += float64(r.rate) / SampleRate
		}
		r.previous = sample
		r.index++
	}
	return nil
}
func (r *Resampler) Finish() (Data, error) { return Encode(r.output) }
func Encode(samples []float32) (Data, error) {
	if len(samples) < SampleRate*3/10 || len(samples)*2+44 > MaxWAVBytes {
		return Data{}, ErrQuiet
	}
	const frameSamples = SampleRate / 50
	const minActiveSamples = SampleRate * 8 / 100
	var energy float64
	activeSamples := 0
	for start := 0; start < len(samples); start += frameSamples {
		frame := samples[start:min(start+frameSamples, len(samples))]
		var frameEnergy float64
		for _, sample := range frame {
			if math.IsNaN(float64(sample)) || math.IsInf(float64(sample), 0) {
				return Data{}, errors.New("音频采样无效")
			}
			squared := float64(sample) * float64(sample)
			energy += squared
			frameEnergy += squared
		}
		if math.Sqrt(frameEnergy/float64(len(frame))) >= 0.003 {
			activeSamples += len(frame)
		}
	}
	rms := math.Sqrt(energy / float64(len(samples)))
	if activeSamples < minActiveSamples {
		return Data{}, ErrQuiet
	}
	return Data{WAV: WAV(samples), Duration: time.Duration(len(samples)) * time.Second / SampleRate, RMS: rms}, nil
}
func WAV(samples []float32) []byte {
	out := make([]byte, 44+len(samples)*2)
	copy(out[0:4], "RIFF")
	binary.LittleEndian.PutUint32(out[4:8], uint32(len(out)-8))
	copy(out[8:16], "WAVEfmt ")
	binary.LittleEndian.PutUint32(out[16:20], 16)
	binary.LittleEndian.PutUint16(out[20:22], 1)
	binary.LittleEndian.PutUint16(out[22:24], 1)
	binary.LittleEndian.PutUint32(out[24:28], SampleRate)
	binary.LittleEndian.PutUint32(out[28:32], SampleRate*2)
	binary.LittleEndian.PutUint16(out[32:34], 2)
	binary.LittleEndian.PutUint16(out[34:36], 16)
	copy(out[36:40], "data")
	binary.LittleEndian.PutUint32(out[40:44], uint32(len(samples)*2))
	for i, sample := range samples {
		value := int16(math.Round(max(-1, min(1, float64(sample))) * 32767))
		binary.LittleEndian.PutUint16(out[44+i*2:46+i*2], uint16(value))
	}
	return out
}

// ParseMixFormat reads the packed SDK WAVEFORMATEX/EXTENSIBLE layout, not Go
// struct padding. Only PCM and IEEE float GUIDs are accepted.
func ParseMixFormat(raw []byte) (Format, error) {
	if len(raw) < 18 {
		return Format{}, ErrUnavailable
	}
	tag := binary.LittleEndian.Uint16(raw[0:2])
	f := Format{Channels: int(binary.LittleEndian.Uint16(raw[2:4])), Rate: int(binary.LittleEndian.Uint32(raw[4:8])), Bits: int(binary.LittleEndian.Uint16(raw[14:16]))}
	if tag == 0xfffe {
		if len(raw) < 40 || binary.LittleEndian.Uint16(raw[16:18]) < 22 {
			return f, ErrUnavailable
		}
		tail := []byte{0, 0, 0x10, 0, 0x80, 0, 0, 0xaa, 0, 0x38, 0x9b, 0x71}
		for i, b := range tail {
			if raw[28+i] != b {
				return f, errors.New("麦克风压缩格式不受支持")
			}
		}
		subtype := binary.LittleEndian.Uint32(raw[24:28])
		if subtype != 1 && subtype != 3 {
			return f, errors.New("麦克风压缩格式不受支持")
		}
		tag = uint16(subtype)
	}
	if tag != 1 && tag != 3 {
		return f, errors.New("仅支持 PCM 或 IEEE float 麦克风")
	}
	f.Float = tag == 3
	if int(binary.LittleEndian.Uint16(raw[12:14])) != f.Channels*f.Bits/8 {
		return f, ErrUnavailable
	}
	return f, f.Validate()
}
