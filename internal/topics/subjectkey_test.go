package topics

import "testing"

func TestSubjectKey(t *testing.T) {
	tests := []struct {
		label, want string
	}{
		{"2024 US Presidential Election", "presidential election"},
		{"US Presidential Elections", "presidential election"},
		{"2026 US Midterm Elections", "midterm election"},
		{"2026 Midterm Elections", "midterm election"},
		{"Brazilian Presidential Election", "brazilian presidential election"},
		{"Elections", "election"},
		{"Congress", "congress"},
		{"Russia-Ukraine War", "russia-ukraine war"},
		{"U.S. Government Shutdown", "government shutdown"},
		{"The Fall of Rome", "fall rome"},
		{"Trump - Says", "trump says"}, // four runes: kept
		{"Bus", "bus"},
		{"Donald Trump", "donald trump"},
		{"Día de los Muertos", "día de los muerto"},
		{"ÉLECTIONS", "élection"},
		{"Over 2000 Flights", "over flight"},
		{"Election2024", "election2024"},
		{"", ""},
		{"  2026 ", ""},
	}
	for _, tt := range tests {
		if got := SubjectKey(tt.label); got != tt.want {
			t.Errorf("SubjectKey(%q) = %q, want %q", tt.label, got, tt.want)
		}
	}
}

func TestSubjectKeyKeepsSubjectsApart(t *testing.T) {
	if a, b := SubjectKey("2024 US Presidential Election"), SubjectKey("Brazilian Presidential Election"); a == b {
		t.Errorf("US and Brazilian elections share key %q", a)
	}
}
