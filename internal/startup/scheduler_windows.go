//go:build windows

package startup

import (
	"errors"
	"runtime"

	"github.com/go-ole/go-ole"
	"github.com/go-ole/go-ole/oleutil"
	"golang.org/x/sys/windows"
)

type dispatch = ole.IDispatch

func withScheduler(work func(service, root *dispatch) error) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	err := ole.CoInitializeEx(0, ole.COINIT_APARTMENTTHREADED)
	initialized := true
	if err != nil {
		var code *ole.OleError
		if !errors.As(err, &code) {
			return err
		}
		switch uint32(code.Code()) {
		case 1: // S_FALSE still increments COM's initialization count.
		case 0x80010106: // RPC_E_CHANGED_MODE: use the caller's existing apartment.
			// Schedule.Service supports either model. We did not initialize COM
			// successfully in this case and must not uninitialize the caller's COM.
			initialized = false
		default:
			return err
		}
	}
	if initialized {
		defer ole.CoUninitialize()
	}
	unknown, err := oleutil.CreateObject("Schedule.Service")
	if err != nil {
		return err
	}
	defer unknown.Release()
	service, err := unknown.QueryInterface(ole.IID_IDispatch)
	if err != nil {
		return err
	}
	defer service.Release()
	if err := callVoid(service, "Connect"); err != nil {
		return err
	}
	root, err := callObject(service, "GetFolder", `\`)
	if err != nil {
		return err
	}
	defer root.Release()
	return work(service, root)
}

func callObject(object *dispatch, method string, args ...interface{}) (*dispatch, error) {
	value, err := oleutil.CallMethod(object, method, args...)
	if value != nil {
		defer value.Clear()
	}
	if err != nil {
		return nil, err
	}
	if value == nil || value.VT != ole.VT_DISPATCH || value.ToIDispatch() == nil {
		return nil, errors.New("Task Scheduler returned no dispatch object")
	}
	result := value.ToIDispatch()
	result.AddRef() // Transfer a reference before clearing the owning VARIANT.
	return result, nil
}

func callVoid(object *dispatch, method string, args ...interface{}) error {
	value, err := oleutil.CallMethod(object, method, args...)
	if value != nil {
		defer value.Clear()
	}
	return err
}

func propertyString(object *dispatch, name string) (string, error) {
	value, err := oleutil.GetProperty(object, name)
	if value != nil {
		defer value.Clear()
	}
	return variantString(value, err)
}

func variantString(value *ole.VARIANT, err error) (string, error) {
	if err != nil {
		return "", err
	}
	if value == nil || value.VT != ole.VT_BSTR {
		return "", errors.New("Task Scheduler returned a non-string value")
	}
	return value.ToString(), nil
}

func (b *windowsBackend) expectedTaskXML(service *dispatch) (string, error) {
	definition, err := callObject(service, "NewTask", int32(0))
	if err != nil {
		return "", err
	}
	defer definition.Release()
	value, err := oleutil.PutProperty(definition, "XmlText", taskXML(b.exe, b.sid))
	if value != nil {
		defer value.Clear()
	}
	if err != nil {
		return "", err
	}
	return propertyString(definition, "XmlText")
}

func (b *windowsBackend) inspectTask(service, root *dispatch) (bool, error) {
	task, err := callObject(root, "GetTask", taskName(b.sid))
	if taskMissing(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer task.Release()
	actual, err := propertyString(task, "Xml")
	if err != nil {
		return false, err
	}
	expected, err := b.expectedTaskXML(service)
	if err != nil {
		return false, err
	}
	if !sameTaskXMLForUser(actual, expected, b.sid, func(account string) (string, error) {
		sid, _, _, err := windows.LookupSID("", account)
		if err != nil {
			return "", err
		}
		return sid.String(), nil
	}) {
		return false, errors.New("startup task definition was modified externally; refusing to replace it")
	}
	// OWNER_SECURITY_INFORMATION | GROUP_SECURITY_INFORMATION |
	// DACL_SECURITY_INFORMATION. The fixed descriptor excludes audit rules.
	value, err := oleutil.CallMethod(task, "GetSecurityDescriptor", int32(7))
	if value != nil {
		defer value.Clear()
	}
	sddl, err := variantString(value, err)
	if err != nil {
		return false, err
	}
	matching, err := sameTaskSecurity(sddl, taskSDDL(b.sid))
	if err != nil {
		return false, err
	}
	if !matching {
		return false, errors.New("startup task security was modified externally; refusing to replace it")
	}
	return true, nil
}

// changeTask is called only after the helper (or the already elevated caller)
// has checked both elevation and the original SID. No XML snapshot is replayed.
func (b *windowsBackend) changeTask(enabled bool) error {
	return withScheduler(func(service, root *dispatch) error {
		present, err := b.inspectTask(service, root)
		if err != nil || present == enabled {
			return err
		}
		if !enabled {
			err := callVoid(root, "DeleteTask", taskName(b.sid), int32(0))
			if taskMissing(err) {
				return nil
			}
			return err
		}
		expected, err := b.expectedTaskXML(service)
		if err != nil {
			return err
		}
		// TASK_CREATE, never CREATE_OR_UPDATE: a racing external registration
		// must fail rather than overwrite an uninspected task. Preserve our fixed
		// ACL with TASK_DONT_ADD_PRINCIPAL_ACE. TASK_LOGON_INTERACTIVE_TOKEN=3.
		task, err := callObject(root, "RegisterTask", taskName(b.sid), expected,
			int32(2|0x10), b.sid, nil, int32(3), taskSDDL(b.sid))
		if task != nil {
			task.Release()
		}
		return err
	})
}
