//go:build windows

package startup

import (
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestShellExecuteInfoABI(t *testing.T) {
	var info shellExecuteInfo
	wantSize, wantProcessOffset := uintptr(60), uintptr(56)
	if unsafe.Sizeof(uintptr(0)) == 8 {
		wantSize, wantProcessOffset = 112, 104
	}
	if unsafe.Sizeof(info) != wantSize || unsafe.Offsetof(info.process) != wantProcessOffset {
		t.Fatalf("SHELLEXECUTEINFOW size=%d process offset=%d", unsafe.Sizeof(info), unsafe.Offsetof(info.process))
	}
}

func TestTaskSecurityDescriptorRoundTrip(t *testing.T) {
	sddl := taskSDDL("S-1-5-21-100-200-300-1001")
	sd, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		t.Fatal(err)
	}
	roundTrip, err := windows.SecurityDescriptorFromString(sd.String())
	if err != nil || roundTrip.String() != sd.String() {
		t.Fatalf("invalid canonical task descriptor: %v", err)
	}
}
