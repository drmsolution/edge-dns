package sync

import (
	"encoding/json"
	"fmt"
)

type ConfigEvent struct {
	UserID string `json:"user_id"`
	Action string `json:"action"`
}

const (
	ActionClearCache = "clear_cache"
	ActionUpdateRule = "update_rule"
	ConfigChannel    = "dns:config:events"
)

func (e *ConfigEvent) Validate() error {
	if e.UserID == "" {
		return fmt.Errorf("user_id is required")
	}
	switch e.Action {
	case ActionClearCache, ActionUpdateRule:
		return nil
	default:
		return fmt.Errorf("unknown action: %q", e.Action)
	}
}

func (e *ConfigEvent) Encode() ([]byte, error) {
	data, err := json.Marshal(e)
	if err != nil {
		return nil, fmt.Errorf("encode event: %w", err)
	}
	return data, nil
}

func DecodeEvent(payload string) (*ConfigEvent, error) {
	var e ConfigEvent
	if err := json.Unmarshal([]byte(payload), &e); err != nil {
		return nil, fmt.Errorf("decode event: %w", err)
	}
	if err := e.Validate(); err != nil {
		return nil, fmt.Errorf("invalid event: %w", err)
	}
	return &e, nil
}
