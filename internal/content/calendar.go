package content

import (
	"bytes"
	"fmt"
	"strings"
	"time"

	"github.com/emersion/go-ical"
)

// CalendarEvent is one VEVENT in the S3 `calendarEvents` shape (P4.2 §3).
type CalendarEvent struct {
	Method    string           `json:"method,omitempty" doc:"iTIP method of the calendar: REQUEST, REPLY, CANCEL, PUBLISH …"`
	UID       string           `json:"uid" doc:"Event UID"`
	Sequence  int              `json:"sequence" doc:"Revision number (SEQUENCE)"`
	Summary   string           `json:"summary,omitempty" doc:"Event title"`
	Status    string           `json:"status,omitempty" doc:"TENTATIVE, CONFIRMED or CANCELLED"`
	DTStart   *CalendarTime    `json:"dtStart,omitempty" doc:"Start"`
	DTEnd     *CalendarTime    `json:"dtEnd,omitempty" doc:"End"`
	RRule     string           `json:"rrule,omitempty" doc:"Recurrence rule (RFC 5545 RRULE)"`
	Organizer *CalendarPerson  `json:"organizer,omitempty" doc:"Organizer"`
	Attendees []CalendarPerson `json:"attendees,omitempty" doc:"Attendees"`
}

// CalendarTime is a DTSTART/DTEND value with timezone metadata.
type CalendarTime struct {
	DateTime time.Time `json:"dateTime" doc:"Date and time (UTC)"`
	TimeZone string    `json:"timeZone,omitempty" doc:"Original TZID"`
	AllDay   bool      `json:"allDay,omitempty" doc:"All-day event (a DATE value)"`
}

// CalendarPerson is an ORGANIZER or ATTENDEE.
type CalendarPerson struct {
	Address  string `json:"address" doc:"E-mail address"`
	Name     string `json:"name,omitempty" doc:"Common name (CN)"`
	Role     string `json:"role,omitempty" doc:"Role, e.g. REQ-PARTICIPANT"`
	PartStat string `json:"partStat,omitempty" doc:"Participation status, e.g. ACCEPTED or NEEDS-ACTION"`
	RSVP     bool   `json:"rsvp,omitempty" doc:"The organizer asks for a reply (RSVP=TRUE)"`
}

// ParseCalendar parses a text/calendar body into CalendarEvent values.
// go-ical can panic on some malformed inputs; those are returned as errors.
func ParseCalendar(ics []byte) (out []CalendarEvent, err error) {
	defer func() {
		if rec := recover(); rec != nil {
			out = nil
			err = fmt.Errorf("calendar: parse panic: %v", rec)
		}
	}()
	if len(bytes.TrimSpace(ics)) == 0 {
		return []CalendarEvent{}, nil
	}
	cal, err := ical.NewDecoder(bytes.NewReader(ics)).Decode()
	if err != nil {
		return nil, err
	}
	method, _ := cal.Props.Text(ical.PropMethod)
	events := cal.Events()
	out = make([]CalendarEvent, 0, len(events))
	for _, ev := range events {
		ce, err := mapEvent(ev, method)
		if err != nil {
			return nil, err
		}
		out = append(out, ce)
	}
	return out, nil
}

func mapEvent(ev ical.Event, method string) (CalendarEvent, error) {
	uid, _ := ev.Props.Text(ical.PropUID)
	summary, _ := ev.Props.Text(ical.PropSummary)
	status, err := ev.Status()
	if err != nil {
		status = ""
	}
	seq := 0
	if p := ev.Props.Get(ical.PropSequence); p != nil {
		if n, err := p.Int(); err == nil {
			seq = n
		}
	}
	rrule := ""
	if p := ev.Props.Get(ical.PropRecurrenceRule); p != nil {
		rrule = p.Value
	}

	start, err := calendarTime(ev.Props.Get(ical.PropDateTimeStart))
	if err != nil {
		return CalendarEvent{}, err
	}
	endProp := ev.Props.Get(ical.PropDateTimeEnd)
	var end *CalendarTime
	if endProp != nil {
		end, err = calendarTime(endProp)
		if err != nil {
			return CalendarEvent{}, err
		}
	} else if start != nil {
		if t, err := ev.DateTimeEnd(time.UTC); err == nil && !t.IsZero() {
			end = &CalendarTime{DateTime: t.UTC(), TimeZone: start.TimeZone, AllDay: start.AllDay}
		}
	}

	var attendees []CalendarPerson
	for _, p := range ev.Props.Values(ical.PropAttendee) {
		attendees = append(attendees, mapPerson(p))
	}
	if attendees == nil {
		attendees = []CalendarPerson{}
	}

	var organizer *CalendarPerson
	if p := ev.Props.Get(ical.PropOrganizer); p != nil {
		o := mapPerson(*p)
		organizer = &o
	}

	return CalendarEvent{
		Method:    method,
		UID:       uid,
		Sequence:  seq,
		Summary:   summary,
		Status:    string(status),
		DTStart:   start,
		DTEnd:     end,
		RRule:     rrule,
		Organizer: organizer,
		Attendees: attendees,
	}, nil
}

func calendarTime(prop *ical.Prop) (*CalendarTime, error) {
	if prop == nil {
		return nil, nil
	}
	tzid := prop.Params.Get(ical.ParamTimezoneID)
	loc := time.UTC
	tzName := "UTC"
	if tzid != "" {
		tzName = tzid
		if l, err := time.LoadLocation(tzid); err == nil {
			loc = l
		}
	}
	t, err := prop.DateTime(loc)
	if err != nil && tzid != "" {
		// Invalid TZID: parse in UTC without the TZID param.
		cp := *prop
		cp.Params = cloneParams(prop.Params)
		cp.Params.Del(ical.ParamTimezoneID)
		t, err = cp.DateTime(time.UTC)
		tzName = "UTC"
	}
	if err != nil {
		return nil, err
	}
	allDay := prop.ValueType() == ical.ValueDate || len(prop.Value) == len("20060102")
	if allDay {
		tzName = "UTC"
		t = time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
	} else if strings.HasSuffix(prop.Value, "Z") {
		tzName = "UTC"
		t = t.UTC()
	} else {
		t = t.UTC()
	}
	return &CalendarTime{DateTime: t, TimeZone: tzName, AllDay: allDay}, nil
}

func cloneParams(in ical.Params) ical.Params {
	out := make(ical.Params, len(in))
	for k, v := range in {
		out[k] = append([]string(nil), v...)
	}
	return out
}

func mapPerson(p ical.Prop) CalendarPerson {
	addr := strings.TrimSpace(p.Value)
	addr = strings.TrimPrefix(strings.TrimPrefix(addr, "mailto:"), "MAILTO:")
	cp := CalendarPerson{
		Address:  addr,
		Name:     p.Params.Get(ical.ParamCommonName),
		Role:     p.Params.Get(ical.ParamRole),
		PartStat: p.Params.Get(ical.ParamParticipationStatus),
	}
	if strings.EqualFold(p.Params.Get(ical.ParamRSVP), "TRUE") {
		cp.RSVP = true
	}
	return cp
}
