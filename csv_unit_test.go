package main

import (
	"reflect"
	"testing"
)

func TestEncodeLabels(t *testing.T) {
	tests := []struct {
		name   string
		input  map[string][]string
		want   string
	}{
		{"nil", nil, ""},
		{"empty", map[string][]string{}, ""},
		{"single", map[string][]string{"room": {"kitchen"}}, "room:kitchen"},
		{"multi-value", map[string][]string{"room": {"bar", "office"}}, "room:bar|room:office"},
		{"multi-key", map[string][]string{"floor": {"basement"}, "room": {"office"}}, "floor:basement|room:office"},
		{"sorted values", map[string][]string{"room": {"z-room", "a-room"}}, "room:a-room|room:z-room"},
		{"value with space", map[string][]string{"Location": {"Master Bedroom"}}, "Location:Master Bedroom"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := encodeLabels(tt.input)
			if got != tt.want {
				t.Errorf("encodeLabels(%v) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestDecodeLabels(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  map[string][]string
	}{
		{"empty", "", nil},
		{"whitespace only", "   ", nil},
		{"single", "room:kitchen", map[string][]string{"room": {"kitchen"}}},
		{"multi-value same key", "room:bar|room:office", map[string][]string{"room": {"bar", "office"}}},
		{"multi-key", "floor:basement|room:office", map[string][]string{"floor": {"basement"}, "room": {"office"}}},
		{"leading/trailing space", "  room:kitchen  ", map[string][]string{"room": {"kitchen"}}},
		{"value with space", "Location:Master Bedroom", map[string][]string{"Location": {"Master Bedroom"}}},
		{"multi with spaces", "Location:Master Bedroom|Location:Alex Room", map[string][]string{"Location": {"Master Bedroom", "Alex Room"}}},
		{"skips token without colon", "badtoken|room:kitchen", map[string][]string{"room": {"kitchen"}}},
		{"skips token with leading colon", ":bad|room:kitchen", map[string][]string{"room": {"kitchen"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := decodeLabels(tt.input)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("decodeLabels(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestEncodeDecodeRoundtrip(t *testing.T) {
	original := map[string][]string{
		"room":  {"bar", "office"},
		"floor": {"basement"},
		"ip":    {"192.168.1.1"},
	}
	encoded := encodeLabels(original)
	decoded := decodeLabels(encoded)

	for k, wantVals := range original {
		gotVals := decoded[k]
		if len(gotVals) != len(wantVals) {
			t.Errorf("key %q: got %v, want %v", k, gotVals, wantVals)
			continue
		}
		// both are sorted by encodeLabels, so direct compare works
		for i, v := range wantVals {
			if gotVals[i] != v {
				t.Errorf("key %q[%d]: got %q, want %q", k, i, gotVals[i], v)
			}
		}
	}
}

func TestCsvColIndex(t *testing.T) {
	header := []string{"plugin_id", "device_id", " local_name ", "labels"}
	col := csvColIndex(header)

	if col["plugin_id"] != 0 {
		t.Errorf("plugin_id: want 0, got %d", col["plugin_id"])
	}
	if col["local_name"] != 2 {
		t.Errorf("local_name (trimmed): want 2, got %d", col["local_name"])
	}
}

func TestCsvCol(t *testing.T) {
	col := map[string]int{"plugin_id": 0, "device_id": 1, "local_name": 2}
	row := []string{"my-plugin", "my-device", "  My Device  "}

	if got := csvCol(row, col, "plugin_id"); got != "my-plugin" {
		t.Errorf("got %q", got)
	}
	if got := csvCol(row, col, "local_name"); got != "My Device" {
		t.Errorf("expected trimmed, got %q", got)
	}
	if got := csvCol(row, col, "missing"); got != "" {
		t.Errorf("missing col should return empty, got %q", got)
	}
}
