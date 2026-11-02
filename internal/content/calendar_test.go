package content

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseCalendar_meeting(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "calendar", "meeting.ics"))
	if err != nil {
		t.Fatal(err)
	}
	evs, err := ParseCalendar(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 1 {
		t.Fatalf("len=%d", len(evs))
	}
	e := evs[0]
	if e.Method != "REQUEST" || e.UID != "meet-001@example.com" || e.Sequence != 2 {
		t.Fatalf("meta: %+v", e)
	}
	if e.Summary != "Planning sync" || e.Status != "CONFIRMED" {
		t.Fatalf("summary/status: %+v", e)
	}
	if e.RRule == "" || !strings.Contains(e.RRule, "FREQ=WEEKLY") {
		t.Fatalf("rrule: %q", e.RRule)
	}
	if e.DTStart == nil || e.DTEnd == nil || e.DTStart.TimeZone != "Europe/Rome" {
		t.Fatalf("times: %+v %+v", e.DTStart, e.DTEnd)
	}
	if e.Organizer == nil || e.Organizer.Address != "alice@example.com" || e.Organizer.Name != "Alice Example" {
		t.Fatalf("organizer: %+v", e.Organizer)
	}
	if len(e.Attendees) != 2 || e.Attendees[0].Address != "bob@example.com" || !e.Attendees[0].RSVP {
		t.Fatalf("attendees: %+v", e.Attendees)
	}
	if e.Attendees[0].PartStat != "NEEDS-ACTION" || e.Attendees[1].PartStat != "ACCEPTED" {
		t.Fatalf("partstat: %+v", e.Attendees)
	}
	// timed event should not be all-day
	if e.DTStart.AllDay {
		t.Fatal("expected timed start")
	}
	if e.DTStart.DateTime.Location() != time.UTC {
		t.Fatalf("want UTC instant, got %v", e.DTStart.DateTime.Location())
	}
}

func TestParseCalendar_allDay(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "calendar", "allday.ics"))
	if err != nil {
		t.Fatal(err)
	}
	evs, err := ParseCalendar(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 1 || !evs[0].DTStart.AllDay || evs[0].Method != "PUBLISH" {
		t.Fatalf("%+v", evs)
	}
	if evs[0].DTStart.DateTime != time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC) {
		t.Fatalf("start %v", evs[0].DTStart.DateTime)
	}
}

func TestParseCalendar_emptyAndBad(t *testing.T) {
	evs, err := ParseCalendar(nil)
	if err != nil || len(evs) != 0 {
		t.Fatalf("%v %v", evs, err)
	}
	if _, err := ParseCalendar([]byte("not ical")); err == nil {
		t.Fatal("expected error")
	}
}
