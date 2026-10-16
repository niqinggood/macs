package portablepath

import "testing"

func TestNormalizeAndIsAbs(t *testing.T) {
	tests := []struct {
		input string
		want  string
		abs   bool
	}{
		{input: ` "C:\\work\\openkitty\\..\\openkitty\\main.go" `, want: "C:/work/openkitty/main.go", abs: true},
		{input: `/srv/openkitty/./main.go`, want: "/srv/openkitty/main.go", abs: true},
		{input: `\\\\server\\share\\folder`, want: "//server/share/folder", abs: true},
		{input: `docs/../README.md`, want: "README.md", abs: false},
	}
	for _, test := range tests {
		if got := Normalize(test.input); got != test.want {
			t.Errorf("Normalize(%q) = %q, want %q", test.input, got, test.want)
		}
		if got := IsAbs(test.input); got != test.abs {
			t.Errorf("IsAbs(%q) = %v, want %v", test.input, got, test.abs)
		}
	}
}

func TestRelativeTo(t *testing.T) {
	if got, ok := RelativeTo(`C:\\Work\\OpenKitty`, `c:\\work\\openkitty\\docs\\README.md`); !ok || got != "docs/README.md" {
		t.Fatalf("Windows RelativeTo() = %q, %v", got, ok)
	}
	if got, ok := RelativeTo("/srv/openkitty", "/srv/openkitty/docs/readme.md"); !ok || got != "docs/readme.md" {
		t.Fatalf("Unix RelativeTo() = %q, %v", got, ok)
	}
	if got, ok := RelativeTo("/srv/openkitty", "/srv/other/file"); ok || got != "" {
		t.Fatalf("outside RelativeTo() = %q, %v", got, ok)
	}
}
