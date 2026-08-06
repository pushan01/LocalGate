package notification

import (
	"testing"

	notify "localgate/internal/notification"
)

func TestValidateMessage(t *testing.T) {
	if err := validateMessage(notify.Message{Title: "Ready", Body: "Done"}); err != nil {
		t.Fatalf("valid message rejected: %v", err)
	}
	if err := validateMessage(notify.Message{Title: "Ready"}); err == nil {
		t.Fatal("expected missing content error")
	}
	if err := validateMessage(notify.Message{Title: "Ready", Body: "Done", Markdown: "Done"}); err == nil {
		t.Fatal("expected mutually exclusive content error")
	}
}

func TestToolDoesNotExposeLevelArgument(t *testing.T) {
	schema := Tool{}.Schema().(map[string]any)
	properties := schema["properties"].(map[string]any)
	if _, ok := properties["level"]; ok {
		t.Fatal("level must be fixed and absent from the tool schema")
	}
}
