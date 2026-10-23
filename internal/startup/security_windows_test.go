//go:build windows

package startup

import (
	"strings"
	"testing"
)

func TestTaskSecurityAcceptsSchedulerMetadataOnly(t *testing.T) {
	const sid = "S-1-5-21-100-200-300-1001"
	expected := taskSDDL(sid)
	registered := strings.Replace(expected, "D:P", "D:PAI", 1)
	for _, actual := range []string{
		expected,
		registered,
		strings.Replace(registered, ";;FRFX;;;", ";;0x1200a9;;;", 1),
	} {
		matching, err := sameTaskSecurity(actual, expected)
		if err != nil || !matching {
			t.Fatalf("equivalent scheduler ACL rejected: %s: %v", actual, err)
		}
	}
	for _, actual := range []string{
		strings.Replace(expected, "D:P", "D:", 1),
		strings.Replace(expected, "D:P", "D:PAR", 1),
		strings.Replace(expected, "O:BA", "O:SY", 1),
		strings.Replace(expected, "G:BA", "G:SY", 1),
		strings.Replace(expected, ";;FRFX;;;", ";;FA;;;", 1),
		expected + "(A;;FA;;;WD)",
		expected + "(A;;FRFX;;;" + sid + ")",
		strings.Replace(expected, "(A;;FA;;;SY)", "(D;;FA;;;SY)", 1),
		strings.Replace(expected, "(A;;FRFX;;;", "(A;ID;FRFX;;;", 1),
		"O:BAG:BAD:P",
		"invalid SDDL",
	} {
		matching, _ := sameTaskSecurity(actual, expected)
		if matching {
			t.Fatalf("modified scheduler ACL accepted: %s", actual)
		}
	}
}
