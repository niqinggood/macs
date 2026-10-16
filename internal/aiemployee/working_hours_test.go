package aiemployee

import (
	"testing"
	"time"
)

func TestIsWithinWorkingHoursAtSupportsOvernightWindows(t *testing.T) {
	tests := []struct {
		name  string
		now   string
		hours string
		want  bool
	}{
		{name: "before midnight", now: "23:59", hours: "23:57-00:03", want: true},
		{name: "after midnight", now: "00:01", hours: "23:59-00:03", want: true},
		{name: "outside overnight window", now: "00:04", hours: "23:59-00:03", want: false},
		{name: "ordinary daytime window", now: "09:30", hours: "09:00-12:00", want: true},
		{name: "multiple windows", now: "14:30", hours: "09:00-12:00,14:00-18:00", want: true},
		{name: "invalid window is ignored", now: "00:00", hours: "bad-00:30", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			now, err := time.Parse("15:04", tt.now)
			if err != nil {
				t.Fatal(err)
			}
			if got := IsWithinWorkingHoursAt(tt.hours, now); got != tt.want {
				t.Fatalf("isWithinWorkingHoursAt(%q, %s) = %v, want %v", tt.hours, tt.now, got, tt.want)
			}
		})
	}
}
