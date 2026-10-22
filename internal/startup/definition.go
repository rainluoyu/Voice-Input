package startup

import (
	"bytes"
	"encoding/binary"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strings"
	"unicode/utf16"
)

func normalRun(exe string) runValue {
	units := utf16.Encode([]rune(`"` + exe + `"` + "\x00"))
	data := make([]byte, len(units)*2)
	for i, unit := range units {
		binary.LittleEndian.PutUint16(data[i*2:], unit)
	}
	return runValue{exists: true, kind: 1, data: data} // REG_SZ, including its NUL
}

func taskName(sid string) string { return "voice_input_startup_" + sid }

// GetTask/DeleteTask report HRESULT_FROM_WIN32(ERROR_FILE_NOT_FOUND). In an
// Automation invocation it may be carried by EXCEPINFO rather than the outer
// DISP_E_EXCEPTION. Access denied, bad XML and a stopped service are not absence.
func taskMissing(err error) bool {
	var code interface{ Code() uintptr }
	if !errors.As(err, &code) {
		return false
	}
	hr := uint32(code.Code())
	if hr == 0x80020009 {
		var sub interface{ SubError() error }
		var info interface{ SCODE() uint32 }
		if !errors.As(err, &sub) || !errors.As(sub.SubError(), &info) {
			return false
		}
		hr = info.SCODE()
	}
	return hr == 0x80070002
}

// Only this in-memory definition can be registered, including during rollback.
// The helper never consumes caller-supplied XML, paths, arguments or task names.
func taskXML(exe, sid string) string {
	escape := func(s string) string {
		var b bytes.Buffer
		_ = xml.EscapeText(&b, []byte(s))
		return b.String()
	}
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-16"?>
<Task version="1.2" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
  <RegistrationInfo>
    <Description>voice_input managed administrator startup</Description>
    <URI>\%s</URI>
  </RegistrationInfo>
  <Triggers><LogonTrigger><Enabled>true</Enabled><UserId>%s</UserId></LogonTrigger></Triggers>
  <Principals><Principal id="Author"><UserId>%s</UserId><LogonType>InteractiveToken</LogonType><RunLevel>HighestAvailable</RunLevel></Principal></Principals>
  <Settings>
    <MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>
    <DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>
    <StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>
    <AllowHardTerminate>true</AllowHardTerminate>
    <StartWhenAvailable>false</StartWhenAvailable>
    <RunOnlyIfNetworkAvailable>false</RunOnlyIfNetworkAvailable>
    <IdleSettings><StopOnIdleEnd>true</StopOnIdleEnd><RestartOnIdle>false</RestartOnIdle></IdleSettings>
    <AllowStartOnDemand>true</AllowStartOnDemand>
    <Enabled>true</Enabled>
    <Hidden>false</Hidden>
    <RunOnlyIfIdle>false</RunOnlyIfIdle>
    <WakeToRun>false</WakeToRun>
    <ExecutionTimeLimit>PT0S</ExecutionTimeLimit>
    <Priority>7</Priority>
  </Settings>
  <Actions Context="Author"><Exec><Command>%s</Command></Exec></Actions>
</Task>`, escape(taskName(sid)), escape(sid), escape(sid), escape(exe))
}

// Do not grant the filtered user token permission to edit an elevated task.
// A fixed owner/group/DACL also makes deleting and recreating our task reversible;
// externally changed security descriptors must not be silently discarded.
func taskSDDL(sid string) string {
	return "O:BAG:BAD:P(A;;FA;;;SY)(A;;FA;;;BA)(A;;FRFX;;;" + sid + ")"
}

// Task Scheduler itself normalizes the expected XML via ITaskDefinition.XmlText
// before this comparison. Ignore only namespace spelling, attribute order and
// formatting, not commands, arguments, triggers, settings or metadata.
func sameTaskXML(actual, expected string) bool {
	a, err := xmlTokens(actual)
	if err != nil {
		return false
	}
	b, err := xmlTokens(expected)
	return err == nil && reflect.DeepEqual(a, b)
}

func xmlTokens(text string) ([]xml.Token, error) {
	d := xml.NewDecoder(strings.NewReader(text))
	// COM returns a Go UTF-8 string even though XmlText declares UTF-16.
	d.CharsetReader = func(charset string, input io.Reader) (io.Reader, error) {
		if strings.EqualFold(charset, "utf-16") {
			return input, nil
		}
		return nil, fmt.Errorf("unexpected task XML encoding %q", charset)
	}
	var tokens []xml.Token
	for {
		token, err := d.Token()
		if err == io.EOF {
			return tokens, nil
		}
		if err != nil {
			return nil, err
		}
		switch value := token.(type) {
		case xml.Comment:
			continue
		case xml.ProcInst:
			if value.Target == "xml" {
				continue
			}
			return nil, fmt.Errorf("unexpected task XML instruction")
		case xml.Directive:
			return nil, fmt.Errorf("unexpected task XML directive")
		case xml.CharData:
			if strings.TrimSpace(string(value)) == "" {
				continue
			}
		case xml.StartElement:
			attrs := make([]xml.Attr, 0, len(value.Attr))
			for _, attr := range value.Attr {
				if attr.Name.Space != "xmlns" && !(attr.Name.Space == "" && attr.Name.Local == "xmlns") {
					attrs = append(attrs, attr)
				}
			}
			sort.Slice(attrs, func(i, j int) bool {
				if attrs[i].Name.Space != attrs[j].Name.Space {
					return attrs[i].Name.Space < attrs[j].Name.Space
				}
				return attrs[i].Name.Local < attrs[j].Name.Local
			})
			value.Attr = attrs
			token = value
		}
		tokens = append(tokens, xml.CopyToken(token))
	}
}
