package startup

import (
	"encoding/binary"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"unicode/utf16"
)

func TestNormalRunIsQuotedREGSZAbsoluteExecutable(t *testing.T) {
	exe := `C:\Program Files\输入 & voice_input.exe`
	value := normalRun(exe)
	if !value.exists || value.kind != 1 || len(value.data)%2 != 0 {
		t.Fatalf("not REG_SZ: %#v", value)
	}
	units := make([]uint16, len(value.data)/2)
	for i := range units {
		units[i] = binary.LittleEndian.Uint16(value.data[i*2:])
	}
	if got := string(utf16.Decode(units)); got != `"`+exe+`"`+"\x00" {
		t.Fatalf("Run data = %q", got)
	}
}

func TestTaskDefinitionHasOnlyCurrentExecutableAndSID(t *testing.T) {
	exe := `C:\Program Files\a & b\输入.exe`
	sid := "S-1-5-21-100-200-300-1001"
	text := taskXML(exe, sid)
	if _, err := xmlTokens(text); err != nil {
		t.Fatal(err)
	}
	var task struct {
		Registration struct {
			URI                string
			SecurityDescriptor string
		} `xml:"RegistrationInfo"`
		Principals []struct {
			UserID    string `xml:"UserId"`
			LogonType string
			RunLevel  string
		} `xml:"Principals>Principal"`
		Triggers []struct {
			UserID string `xml:"UserId"`
		} `xml:"Triggers>LogonTrigger"`
		Actions []struct {
			Command          string
			Arguments        string
			WorkingDirectory string
		} `xml:"Actions>Exec"`
	}
	decoder := xml.NewDecoder(strings.NewReader(text))
	decoder.CharsetReader = func(_ string, inputReader io.Reader) (io.Reader, error) { return inputReader, nil }
	if err := decoder.Decode(&task); err != nil {
		t.Fatal(err)
	}
	if task.Registration.URI != `\`+taskName(sid) || !strings.Contains(taskName(sid), sid) {
		t.Fatal("task name is not bound to SID")
	}
	if task.Registration.SecurityDescriptor != taskSDDL(sid) {
		t.Fatal("fixed XML must include the ACL serialized by task registration")
	}
	if len(task.Principals) != 1 || task.Principals[0].UserID != sid || task.Principals[0].LogonType != "InteractiveToken" || task.Principals[0].RunLevel != "HighestAvailable" {
		t.Fatalf("unsafe principal: %#v", task.Principals)
	}
	if len(task.Triggers) != 1 || task.Triggers[0].UserID != sid {
		t.Fatalf("unsafe trigger: %#v", task.Triggers)
	}
	if len(task.Actions) != 1 || task.Actions[0].Command != exe || task.Actions[0].Arguments != "" || task.Actions[0].WorkingDirectory != "" {
		t.Fatalf("unsafe action: %#v", task.Actions)
	}
}

func TestTaskXMLComparisonRejectsExternalChanges(t *testing.T) {
	expected := taskXML(`C:\voice_input.exe`, "S-1-5-21-1")
	if !sameTaskXML(expected, expected) {
		t.Fatal("fixed XML does not match itself")
	}
	formatted := strings.ReplaceAll(expected, "\n", "\r\n\t")
	if !sameTaskXML(formatted, expected) {
		t.Fatal("formatting-only XML change was rejected")
	}
	changes := []string{
		strings.Replace(expected, `C:\voice_input.exe`, `C:\other.exe`, 1),
		strings.Replace(expected, "</Command>", "</Command><Arguments>--arbitrary</Arguments>", 1),
		strings.Replace(expected, "InteractiveToken", "Password", 1),
		strings.Replace(expected, "HighestAvailable", "LeastPrivilege", 1),
		strings.Replace(expected, "<Enabled>true</Enabled>", "<Enabled>false</Enabled>", 1),
		strings.Replace(expected, "<UserId>S-1-5-21-1</UserId>", "<UserId>S-1-5-21-2</UserId>", 1),
		strings.Replace(expected, "<Priority>7</Priority>", "<Priority>6</Priority>", 1),
		strings.Replace(expected, "</Triggers>", "<BootTrigger/></Triggers>", 1),
		strings.Replace(expected, "</Actions>", "<Exec><Command>evil.exe</Command></Exec></Actions>", 1),
		strings.Replace(expected, "managed administrator", "externally modified", 1),
		strings.Replace(expected, "/2004/02/mit/task", "/different-schema", 1),
		expected + "<Extra/>",
		"<!DOCTYPE Task [<!ENTITY path 'evil.exe'>]>" + expected,
		"<?arbitrary instruction?>" + expected,
		"<Task>",
	}
	for i, changed := range changes {
		if sameTaskXML(changed, expected) {
			t.Fatalf("accepted externally modified task #%d", i)
		}
	}
}

func TestTaskXMLAcceptsOnlyEquivalentUserIdentities(t *testing.T) {
	const sid = "S-1-5-21-100-200-300-1001"
	const account = `HOST\User`
	expected := taskXML(`C:\voice_input.exe`, sid)
	resolve := func(name string) (string, error) {
		switch strings.ToLower(name) {
		case strings.ToLower(account):
			return sid, nil
		case `host\other`:
			return "S-1-5-21-100-200-300-1002", nil
		default:
			return "", errors.New("unknown account")
		}
	}
	for _, actual := range []string{
		expected,
		strings.Replace(expected, "<UserId>"+sid+"</UserId>", "<UserId>"+account+"</UserId>", 1),
		strings.ReplaceAll(expected, "<UserId>"+sid+"</UserId>", "<UserId>host\\user</UserId>"),
	} {
		if !sameTaskXMLForUser(actual, expected, sid, resolve) {
			t.Fatal("equivalent registered identity was rejected")
		}
	}
	for _, actual := range []string{
		strings.Replace(expected, "<UserId>"+sid+"</UserId>", `<UserId>HOST\Other</UserId>`, 1),
		strings.Replace(expected, "<UserId>"+sid+"</UserId>", `<UserId>Unknown</UserId>`, 1),
		strings.Replace(expected, "<UserId>"+sid+"</UserId>", `<UserId xmlns="other">HOST\User</UserId>`, 1),
		strings.Replace(expected, `C:\voice_input.exe`, account, 1),
		strings.Replace(expected, "</RegistrationInfo>", "<UserId>"+account+"</UserId></RegistrationInfo>", 1),
		strings.Replace(expected, "</Actions>", "<Exec><Command>evil.exe</Command></Exec></Actions>", 1),
	} {
		if sameTaskXMLForUser(actual, expected, sid, resolve) {
			t.Fatal("identity normalization accepted a different or modified task")
		}
	}
}

type fakeHRESULT struct {
	code uintptr
	sub  error
}

func (e fakeHRESULT) Error() string   { return "HRESULT" }
func (e fakeHRESULT) Code() uintptr   { return e.code }
func (e fakeHRESULT) SubError() error { return e.sub }

type fakeEXCEPINFO uint32

func (e fakeEXCEPINFO) Error() string { return "EXCEPINFO" }
func (e fakeEXCEPINFO) SCODE() uint32 { return uint32(e) }

func TestOnlyPreciseTaskMissingHRESULTCountsAsAbsent(t *testing.T) {
	for _, err := range []error{
		fakeHRESULT{code: 0x80070002},
		fmt.Errorf("wrapped: %w", fakeHRESULT{code: 0x80070002}),
		fakeHRESULT{code: 0x80020009, sub: fakeEXCEPINFO(0x80070002)},
	} {
		if !taskMissing(err) {
			t.Fatalf("missing task not recognized: %#v", err)
		}
	}
	for _, err := range []error{
		nil,
		errors.New("The system cannot find the file specified"),
		fakeHRESULT{code: 0x80070005}, // E_ACCESSDENIED
		fakeHRESULT{code: 0x80070003}, // ERROR_PATH_NOT_FOUND, not a missing task
		fakeHRESULT{code: 0x80041315}, // SCHED_E_SERVICE_NOT_RUNNING
		fakeHRESULT{code: 0x80041318}, // SCHED_E_INVALIDVALUE
		fakeHRESULT{code: 0x80020009},
		fakeHRESULT{code: 0x80020009, sub: fakeEXCEPINFO(0x80070005)},
	} {
		if taskMissing(err) {
			t.Fatalf("non-absence treated as missing: %#v", err)
		}
	}
}
