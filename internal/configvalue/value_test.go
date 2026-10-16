package configvalue

import "testing"

func TestTypedConfigValues(t *testing.T) {
	values := map[string]interface{}{
		"name": 123, "enabled": "yes", "count": "7", "ratio": "1.5",
		"tags": []interface{}{"one", 2}, "nested": map[string]interface{}{"ok": true},
	}
	if got := String(values, "name", ""); got != "123" {
		t.Fatalf("String = %q", got)
	}
	if !Bool(values, "enabled", false) {
		t.Fatal("Bool did not parse yes")
	}
	if got := Int(values, "count", 0); got != 7 {
		t.Fatalf("Int = %d", got)
	}
	if got := Float(values, "ratio", 0); got != 1.5 {
		t.Fatalf("Float = %v", got)
	}
	if got := StringSlice(values, "tags", nil); len(got) != 2 || got[1] != "2" {
		t.Fatalf("StringSlice = %#v", got)
	}
	if got := Map(values, "nested", nil); got["ok"] != true {
		t.Fatalf("Map = %#v", got)
	}
}
