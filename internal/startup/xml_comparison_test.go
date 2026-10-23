package startup

import (
	"fmt"
	"strings"
	"testing"
)

func TestTaskXMLMatchesRegisteredWindowsSerialization(t *testing.T) {
	const sid = "S-1-5-21-100-200-300-1001"
	expected := taskXML(`C:\voice_input.exe`, sid)
	// Captured registration shape with synthetic identity/path: Windows sorts
	// schema-all fields, serializes the ACL, and drops default-valued settings.
	registered := fmt.Sprintf(`<Task version="1.2" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
  <RegistrationInfo><SecurityDescriptor>%s</SecurityDescriptor><Description>voice_input managed administrator startup</Description><URI>\%s</URI></RegistrationInfo>
  <Principals><Principal id="Author"><UserId>%s</UserId><LogonType>InteractiveToken</LogonType><RunLevel>HighestAvailable</RunLevel></Principal></Principals>
  <Settings><DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries><StopIfGoingOnBatteries>false</StopIfGoingOnBatteries><ExecutionTimeLimit>PT0S</ExecutionTimeLimit><MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy><IdleSettings><StopOnIdleEnd>true</StopOnIdleEnd><RestartOnIdle>false</RestartOnIdle></IdleSettings></Settings>
  <Triggers><LogonTrigger><UserId>HOST\User</UserId></LogonTrigger></Triggers>
  <Actions Context="Author"><Exec><Command>C:\voice_input.exe</Command></Exec></Actions>
</Task>`, taskSDDL(sid), taskName(sid), sid)
	resolve := func(name string) (string, error) {
		if name == `HOST\User` {
			return sid, nil
		}
		return "", fmt.Errorf("unexpected user")
	}
	if !sameTaskXMLForUser(registered, expected, sid, resolve) {
		t.Fatal("registered task serialization was rejected")
	}
	for _, changed := range []string{
		strings.Replace(registered, "</Settings>", "<Enabled>false</Enabled></Settings>", 1),
		strings.Replace(registered, "</Settings>", "<Priority>8</Priority></Settings>", 1),
		strings.Replace(registered, "</Settings>", "<Unknown>false</Unknown></Settings>", 1),
		strings.Replace(registered, "</Settings>", "<Hidden>true</Hidden></Settings>", 1),
		strings.Replace(registered, "</LogonTrigger>", "<Enabled>false</Enabled></LogonTrigger>", 1),
		strings.Replace(registered, "</LogonTrigger>", "<Enabled>true</Enabled><Enabled>true</Enabled></LogonTrigger>", 1),
		strings.Replace(registered, "</Settings>", "<Priority>7</Priority><Priority>7</Priority></Settings>", 1),
		strings.Replace(registered, "</Actions>", "<Exec><Command>evil.exe</Command></Exec></Actions>", 1),
	} {
		if sameTaskXMLForUser(changed, expected, sid, resolve) {
			t.Fatal("normalization accepted altered settings, duplicate defaults or actions")
		}
	}
}
