//go:build windows && amd64

package audio

import (
	"context"
	"math"
	"runtime"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"voice_input/internal/i18n"

	"github.com/go-ole/go-ole"
	"github.com/moutend/go-wca/pkg/wca"
	"golang.org/x/sys/windows"
)

var captureMu sync.Mutex
var devicesMu sync.Mutex
var initialize = windows.NewLazySystemDLL("ole32.dll").NewProc("CoInitializeEx")

func apartment() error {
	hr, _, _ := initialize.Call(0, 0)
	if int32(hr) < 0 {
		return ErrUnavailable
	}
	return nil
}
func enumerator() (*wca.IMMDeviceEnumerator, error) {
	var e *wca.IMMDeviceEnumerator
	err := wca.CoCreateInstance(wca.CLSID_MMDeviceEnumerator, 0, ole.CLSCTX_INPROC_SERVER, wca.IID_IMMDeviceEnumerator, &e)
	if err != nil || e == nil {
		return nil, ErrUnavailable
	}
	return e, nil
}
func deviceID(d *wca.IMMDevice) (string, error) {
	var value *uint16
	hr, _, _ := syscall.SyscallN(d.VTable().GetId, uintptr(unsafe.Pointer(d)), uintptr(unsafe.Pointer(&value)))
	if int32(hr) < 0 || value == nil {
		return "", ErrUnavailable
	}
	defer ole.CoTaskMemFree(uintptr(unsafe.Pointer(value)))
	return windows.UTF16PtrToString(value), nil
}
func deviceName(d *wca.IMMDevice) (string, *i18n.Message) {
	fallback := i18n.M("popup.device.microphone")
	var store *wca.IPropertyStore
	if d.OpenPropertyStore(wca.STGM_READ, &store) != nil || store == nil {
		return fallback.Text(i18n.ZhCN), &fallback
	}
	defer store.Release()
	var value wca.PROPVARIANT
	if store.GetValue(&wca.PKEY_Device_FriendlyName, &value) != nil || value.VT != 31 || value.Val == 0 {
		return fallback.Text(i18n.ZhCN), &fallback
	}
	return value.String(), nil // go-wca frees the LPWSTR allocated by the property store
}
func Devices() ([]Device, error) {
	if !devicesMu.TryLock() {
		return nil, ErrUnavailable
	}
	defer devicesMu.Unlock()
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := apartment(); err != nil {
		return nil, err
	}
	defer ole.CoUninitialize()
	e, err := enumerator()
	if err != nil {
		return nil, err
	}
	defer e.Release()
	var collection *wca.IMMDeviceCollection
	if e.EnumAudioEndpoints(wca.ECapture, wca.DEVICE_STATE_ACTIVE, &collection) != nil || collection == nil {
		return nil, ErrUnavailable
	}
	defer collection.Release()
	var count uint32
	if collection.GetCount(&count) != nil || count > 256 {
		return nil, ErrUnavailable
	}
	defaultName := i18n.M("popup.device.default")
	result := []Device{{ID: "default", Name: defaultName.Text(i18n.ZhCN), NameI18n: &defaultName}}
	for i := uint32(0); i < count; i++ {
		var d *wca.IMMDevice
		if collection.Item(i, &d) != nil || d == nil {
			continue
		}
		id, err := deviceID(d)
		if err == nil {
			name, message := deviceName(d)
			result = append(result, Device{ID: id, Name: name, NameI18n: message})
		}
		d.Release()
	}
	if len(result) == 1 {
		return result, ErrUnavailable
	}
	return result, nil
}
func findDevice(e *wca.IMMDeviceEnumerator, id string) (*wca.IMMDevice, error) {
	if id == "default" {
		var d *wca.IMMDevice
		if e.GetDefaultAudioEndpoint(wca.ECapture, wca.EConsole, &d) != nil || d == nil {
			return nil, ErrUnavailable
		}
		return d, nil
	}
	// go-wca v0.3.0 GetDevice is unimplemented; select from active endpoints.
	var collection *wca.IMMDeviceCollection
	if e.EnumAudioEndpoints(wca.ECapture, wca.DEVICE_STATE_ACTIVE, &collection) != nil || collection == nil {
		return nil, ErrUnavailable
	}
	defer collection.Release()
	var count uint32
	if collection.GetCount(&count) != nil || count > 256 {
		return nil, ErrUnavailable
	}
	for i := uint32(0); i < count; i++ {
		var d *wca.IMMDevice
		if collection.Item(i, &d) != nil || d == nil {
			continue
		}
		actual, err := deviceID(d)
		if err == nil && actual == id {
			return d, nil
		}
		d.Release()
	}
	return nil, ErrUnavailable
}
func activate(d *wca.IMMDevice) (*wca.IAudioClient, error) {
	var client *wca.IAudioClient
	// v0.3.0 Activate passes &ctx instead of ctx. Use the SDK signature here;
	// keep all remaining Core Audio interfaces in the mature binding.
	hr, _, _ := syscall.SyscallN(d.VTable().Activate, uintptr(unsafe.Pointer(d)), uintptr(unsafe.Pointer(wca.IID_IAudioClient)), ole.CLSCTX_INPROC_SERVER, 0, uintptr(unsafe.Pointer(&client)))
	if int32(hr) < 0 || client == nil {
		return nil, ErrUnavailable
	}
	return client, nil
}
func playbackVolume(e *wca.IMMDeviceEnumerator) (*wca.IAudioEndpointVolume, error) {
	var device *wca.IMMDevice
	if e.GetDefaultAudioEndpoint(wca.ERender, wca.EConsole, &device) != nil || device == nil {
		return nil, i18n.Error("audio.mute_failed")
	}
	defer device.Release()
	var volume *wca.IAudioEndpointVolume
	// As with IAudioClient, bypass go-wca v0.3.0's Activate context ABI bug.
	hr, _, _ := syscall.SyscallN(device.VTable().Activate, uintptr(unsafe.Pointer(device)), uintptr(unsafe.Pointer(wca.IID_IAudioEndpointVolume)), ole.CLSCTX_INPROC_SERVER, 0, uintptr(unsafe.Pointer(&volume)))
	if int32(hr) < 0 || volume == nil {
		return nil, i18n.Error("audio.mute_failed")
	}
	return volume, nil
}
func endpointMute(volume *wca.IAudioEndpointVolume) (bool, error) {
	// Win32 BOOL is 32 bits; go-wca's GetMute(*bool) would overwrite Go memory.
	var muted uint32
	hr, _, _ := syscall.SyscallN(volume.VTable().GetMute, uintptr(unsafe.Pointer(volume)), uintptr(unsafe.Pointer(&muted)))
	if int32(hr) < 0 {
		return false, i18n.Error("audio.mute_failed")
	}
	return muted != 0, nil
}
func mixFormat(format *wca.WAVEFORMATEX) (Format, error) {
	size := 18
	if format.WFormatTag == 0xfffe {
		if format.CbSize < 22 {
			return Format{}, ErrUnavailable
		}
		size = 40
	}
	return ParseMixFormat(unsafe.Slice((*byte)(unsafe.Pointer(format)), size))
}
func (n *Native) Start(ctx context.Context, id string, maxSeconds int, level func(float64)) error {
	return n.start(ctx, 10*time.Second, func(captureCtx context.Context, ready chan<- error) (Data, error) {
		return n.capture(captureCtx, id, maxSeconds, level, ready)
	})
}
func (n *Native) capture(ctx context.Context, id string, maxSeconds int, level func(float64), ready chan<- error) (data Data, err error) {
	if !captureMu.TryLock() {
		ready <- ErrUnavailable
		return Data{}, ErrUnavailable
	}
	defer captureMu.Unlock()
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	announced := false
	defer func() {
		if !announced {
			ready <- err
		}
	}()
	if err = ctx.Err(); err != nil {
		return
	}
	if err = apartment(); err != nil {
		return
	}
	defer ole.CoUninitialize()
	e, err := enumerator()
	if err != nil {
		return Data{}, err
	}
	defer e.Release()
	device, err := findDevice(e, id)
	if err != nil {
		return Data{}, err
	}
	defer device.Release()
	client, err := activate(device)
	if err != nil {
		return Data{}, err
	}
	defer client.Release()
	var format *wca.WAVEFORMATEX
	if client.GetMixFormat(&format) != nil || format == nil {
		return Data{}, ErrUnavailable
	}
	defer ole.CoTaskMemFree(uintptr(unsafe.Pointer(format)))
	decodedFormat, err := mixFormat(format)
	if err != nil {
		return Data{}, err
	}
	if client.Initialize(wca.AUDCLNT_SHAREMODE_SHARED, 0, wca.REFERENCE_TIME(1000000), 0, format, nil) != nil {
		return Data{}, ErrUnavailable
	}
	var capture *wca.IAudioCaptureClient
	if client.GetService(wca.IID_IAudioCaptureClient, &capture) != nil || capture == nil {
		return Data{}, ErrUnavailable
	}
	defer capture.Release()
	if ctx.Err() != nil {
		return Data{}, ctx.Err()
	}
	if n.mutePlayback {
		volume, volumeErr := playbackVolume(e)
		if volumeErr != nil {
			return Data{}, volumeErr
		}
		defer volume.Release()
		var mute muteState
		defer func() {
			if restoreErr := mute.restore(); restoreErr != nil {
				data, err = Data{}, restoreErr
			}
		}()
		if err = mute.mute(func() (bool, error) { return endpointMute(volume) }, func(muted bool) error {
			return volume.SetMute(muted, nil)
		}); err != nil {
			return Data{}, err
		}
	}
	if ctx.Err() != nil {
		return Data{}, ctx.Err()
	}
	if client.Start() != nil {
		return Data{}, ErrUnavailable
	}
	defer client.Stop()
	announced = true
	ready <- nil
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.NewTimer(time.Duration(maxSeconds) * time.Second)
	defer deadline.Stop()
	resampler := NewResampler(decodedFormat.Rate, maxSeconds)
	lastLevel := time.Time{}
	for {
		select {
		case <-ctx.Done():
			return Data{}, ctx.Err()
		case <-n.stop:
			return resampler.Finish()
		case <-deadline.C:
			return resampler.Finish()
		case <-ticker.C:
			for {
				if ctx.Err() != nil {
					return Data{}, ctx.Err()
				}
				select {
				case <-n.stop:
					return resampler.Finish()
				case <-deadline.C:
					return resampler.Finish()
				default:
				}
				var packet uint32
				if capture.GetNextPacketSize(&packet) != nil {
					return Data{}, ErrUnavailable
				}
				if packet == 0 {
					break
				}
				if packet > uint32(decodedFormat.Rate*2) {
					return Data{}, ErrUnavailable
				}
				var buffer *byte
				var frames, flags uint32
				if capture.GetBuffer(&buffer, &frames, &flags, nil, nil) != nil {
					return Data{}, ErrUnavailable
				}
				if frames == 0 {
					_ = capture.ReleaseBuffer(0)
					break
				}
				if frames > uint32(decodedFormat.Rate*2) {
					_ = capture.ReleaseBuffer(frames)
					return Data{}, ErrUnavailable
				}
				var mono []float32
				if flags&wca.AUDCLNT_BUFFERFLAGS_SILENT != 0 {
					mono = make([]float32, frames)
				} else if buffer != nil {
					raw := unsafe.Slice(buffer, int(frames)*decodedFormat.Channels*decodedFormat.Bits/8)
					mono, err = Decode(raw, decodedFormat)
				} else {
					err = ErrUnavailable
				}
				released := capture.ReleaseBuffer(frames)
				if err != nil || released != nil {
					return Data{}, ErrUnavailable
				}
				if err = resampler.Append(mono); err != nil {
					return Data{}, err
				}
				if level != nil && time.Since(lastLevel) > 75*time.Millisecond {
					var energy float64
					for _, s := range mono {
						energy += float64(s) * float64(s)
					}
					if len(mono) > 0 {
						level(math.Sqrt(energy / float64(len(mono))))
					}
					lastLevel = time.Now()
				}
			}
		}
	}
}
