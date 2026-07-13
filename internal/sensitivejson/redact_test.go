package sensitivejson

import (
	"strings"
	"testing"
)

func TestRedactJSONRejectsTrailingContent(t *testing.T) {
	if _, err := RedactJSON([]byte(`{"password":"one"} {"password":"two"}`)); err == nil {
		t.Fatal("expected trailing JSON to fail")
	}
}

func TestRedactRecursesWithoutMutatingInput(t *testing.T) {
	input := map[string]any{"ordinary": "kept", "nested": []any{map[string]any{"AdminPassword": "secret"}}}
	redacted := Redact(input).(map[string]any)
	if redacted["nested"].([]any)[0].(map[string]any)["AdminPassword"] != Redacted {
		t.Fatalf("redacted=%#v", redacted)
	}
	if input["nested"].([]any)[0].(map[string]any)["AdminPassword"] != "secret" {
		t.Fatalf("input mutated=%#v", input)
	}
	if strings.Contains(redacted["ordinary"].(string), Redacted) {
		t.Fatalf("ordinary value changed=%#v", redacted)
	}
}
