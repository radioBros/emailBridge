package throttle

import (
	"testing"
	"time"
)

func rome() *time.Location {
	loc, err := time.LoadLocation("Europe/Rome")
	if err != nil {
		panic(err)
	}
	return loc
}

func TestWindowWeekdayEdges(t *testing.T) {
	w := &Window{TimeZone: "Europe/Rome", Days: []int{1, 2, 3, 4, 5}, Start: "09:00", End: "18:00"}
	loc := rome()
	// Monday 2026-10-26 08:59 Rome — closed
	monEarly := time.Date(2026, 10, 26, 8, 59, 0, 0, loc)
	if w.Contains(monEarly) {
		t.Fatal("before open")
	}
	monOpen := time.Date(2026, 10, 26, 9, 0, 0, 0, loc)
	if !w.Contains(monOpen) {
		t.Fatal("at open")
	}
	monLate := time.Date(2026, 10, 26, 17, 59, 0, 0, loc)
	if !w.Contains(monLate) {
		t.Fatal("before close")
	}
	monClosed := time.Date(2026, 10, 26, 18, 0, 0, 0, loc)
	if w.Contains(monClosed) {
		t.Fatal("at end exclusive")
	}
	sat := time.Date(2026, 10, 31, 12, 0, 0, 0, loc) // Saturday
	if w.Contains(sat) {
		t.Fatal("saturday")
	}
	next := w.NextOpen(monEarly)
	want := monOpen.UTC()
	if !next.Equal(want) {
		t.Fatalf("next=%v want %v", next, want)
	}
	nextSat := w.NextOpen(sat)
	// next Monday 09:00
	wantMon := time.Date(2026, 11, 2, 9, 0, 0, 0, loc).UTC()
	if !nextSat.Equal(wantMon) {
		t.Fatalf("sat next=%v want %v", nextSat, wantMon)
	}
}

func TestWindowOvernight(t *testing.T) {
	w := &Window{TimeZone: "UTC", Days: nil, Start: "22:00", End: "06:00"}
	if !w.Contains(time.Date(2026, 1, 1, 23, 0, 0, 0, time.UTC)) {
		t.Fatal("23:00")
	}
	if !w.Contains(time.Date(2026, 1, 2, 5, 59, 0, 0, time.UTC)) {
		t.Fatal("05:59")
	}
	if w.Contains(time.Date(2026, 1, 2, 6, 0, 0, 0, time.UTC)) {
		t.Fatal("06:00")
	}
	if w.Contains(time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC)) {
		t.Fatal("noon")
	}
}

func TestWindowDSTSpringForward(t *testing.T) {
	// Europe/Rome springs forward 2026-03-29 02:00 → 03:00
	w := &Window{TimeZone: "Europe/Rome", Days: []int{7}, Start: "01:30", End: "03:30"}
	loc := rome()
	// Sunday before change still in CET
	before := time.Date(2026, 3, 29, 1, 45, 0, 0, loc)
	if !w.Contains(before) {
		t.Fatal("01:45 should be in window")
	}
	// 02:30 does not exist; 03:00 is CEST
	after := time.Date(2026, 3, 29, 3, 0, 0, 0, loc)
	if !w.Contains(after) {
		t.Fatal("03:00 after spring")
	}
	closed := time.Date(2026, 3, 29, 3, 30, 0, 0, loc)
	if w.Contains(closed) {
		t.Fatal("03:30 exclusive")
	}
}

func TestWindowDSTFallBack(t *testing.T) {
	// Europe/Rome falls back 2026-10-25 03:00 → 02:00
	w := &Window{TimeZone: "Europe/Rome", Days: []int{7}, Start: "02:15", End: "02:45"}
	loc := rome()
	// Ambiguous hour: both offsets — Contains uses local wall clock
	t1 := time.Date(2026, 10, 25, 2, 30, 0, 0, loc)
	if !w.Contains(t1) {
		t.Fatal("02:30 should be inside")
	}
	next := w.NextOpen(time.Date(2026, 10, 25, 1, 0, 0, 0, loc))
	want := time.Date(2026, 10, 25, 2, 15, 0, 0, loc).UTC()
	if !next.Equal(want) {
		t.Fatalf("next=%v want %v", next, want)
	}
}

func TestParseWindow(t *testing.T) {
	w, err := ParseWindow(`{"timeZone":"UTC","days":[1],"start":"09:00","end":"17:00"}`)
	if err != nil || w == nil || w.Start != "09:00" {
		t.Fatalf("%v %v", w, err)
	}
	w, err = ParseWindow("{}")
	if err != nil || w != nil {
		t.Fatalf("%v %v", w, err)
	}
}
