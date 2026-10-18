package audio

import (
	"encoding/json"
	"strings"
	"testing"

	"voice_input/internal/i18n"
)

func TestDeviceNameOrigin(t *testing.T) {
	message := i18n.M("popup.device.microphone")
	for _, test := range []struct {
		name   string
		device Device
		marked bool
	}{
		{"real-name-matches-placeholder", Device{ID: "physical", Name: "麦克风"}, false},
		{"generated-placeholder", Device{ID: "unavailable", Name: "麦克风", NameI18n: &message}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			data, err := json.Marshal(test.device)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(data), "name_i18n") != test.marked {
				t.Fatal("device-name provenance lost", string(data))
			}
			var decoded Device
			if err := json.Unmarshal(data, &decoded); err != nil || decoded.Name != test.device.Name {
				t.Fatal("real device name changed", decoded, err)
			}
		})
	}
}
