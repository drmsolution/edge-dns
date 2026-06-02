package sync

import (
	"testing"
)

func TestEncodeDecode(t *testing.T) {
	event := &ConfigEvent{
		UserID: "user_abc123",
		Action: ActionClearCache,
	}

	data, err := event.Encode()
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	got, err := DecodeEvent(string(data))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	if got.UserID != event.UserID {
		t.Errorf("UserID = %q, want %q", got.UserID, event.UserID)
	}
	if got.Action != event.Action {
		t.Errorf("Action = %q, want %q", got.Action, event.Action)
	}
}

func TestDecodeInvalidJSON(t *testing.T) {
	_, err := DecodeEvent("{bad json}")
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestDecodeMissingUserID(t *testing.T) {
	_, err := DecodeEvent(`{"action": "clear_cache"}`)
	if err == nil {
		t.Fatal("expected error for missing user_id")
	}
}

func TestDecodeUnknownAction(t *testing.T) {
	_, err := DecodeEvent(`{"user_id": "test", "action": "nuke_everything"}`)
	if err == nil {
		t.Fatal("expected error for unknown action")
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		event ConfigEvent
		valid bool
	}{
		{ConfigEvent{UserID: "u1", Action: ActionClearCache}, true},
		{ConfigEvent{UserID: "u1", Action: ActionUpdateRule}, true},
		{ConfigEvent{UserID: "", Action: ActionClearCache}, false},
		{ConfigEvent{UserID: "u1", Action: "invalid"}, false},
	}

	for _, tc := range tests {
		err := tc.event.Validate()
		if tc.valid && err != nil {
			t.Errorf("expected valid: %+v, got: %v", tc.event, err)
		}
		if !tc.valid && err == nil {
			t.Errorf("expected invalid: %+v", tc.event)
		}
	}
}

func TestChannelConstant(t *testing.T) {
	if ConfigChannel != "dns:config:events" {
		t.Errorf("ConfigChannel = %q, want %q", ConfigChannel, "dns:config:events")
	}
}

func TestEncodeOutput(t *testing.T) {
	event := &ConfigEvent{
		UserID: "user_xyz",
		Action: ActionClearCache,
	}
	data, err := event.Encode()
	if err != nil {
		t.Fatal(err)
	}
	expected := `{"user_id":"user_xyz","action":"clear_cache"}`
	if string(data) != expected {
		t.Errorf("encode = %s, want %s", data, expected)
	}
}
