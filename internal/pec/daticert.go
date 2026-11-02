// Package pec recognises, parses, verifies and builds PEC (Posta Elettronica Certificata) system messages:
// transport and anomaly envelopes and receipts (S17). It knows nothing about accounts or storage; sync links
// receipts to outbound messages and stores them.
package pec

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"golang.org/x/text/encoding/charmap"
)

// MaxDaticert bounds the certification document (S17 §3).
const MaxDaticert = 256 << 10

// Recipient is one addressee of a certified message.
type Recipient struct {
	Address string `json:"address" doc:"Recipient address" example:"studio.rossi@pec.example.it"`
	Kind    string `json:"kind" doc:"certificato: a PEC mailbox; esterno: an ordinary mailbox" example:"certificato"` // certificato (PEC) | esterno (ordinary mail)
}

// Certification is the parsed daticert.xml (S17 §3).
type Certification struct {
	Type              string      `json:"type"`
	Error             string      `json:"error,omitempty"`
	Sender            string      `json:"sender,omitempty"`
	Recipients        []Recipient `json:"recipients,omitempty"`
	ReplyTo           string      `json:"replyTo,omitempty"`
	Subject           string      `json:"subject,omitempty"`
	Provider          string      `json:"provider,omitempty"`
	At                time.Time   `json:"at"`
	TransportID       string      `json:"transportId,omitempty"`
	OriginalMessageID string      `json:"originalMessageId,omitempty"`
	ReceiptKind       string      `json:"receiptKind,omitempty"`
	DeliveredTo       string      `json:"deliveredTo,omitempty"`
	ErrorDetail       string      `json:"errorDetail,omitempty"`
}

type xmlDaticert struct {
	XMLName      xml.Name `xml:"postacert"`
	Tipo         string   `xml:"tipo,attr"`
	Errore       string   `xml:"errore,attr"`
	Intestazione struct {
		Mittente    string `xml:"mittente"`
		Destinatari []struct {
			Tipo  string `xml:"tipo,attr"`
			Value string `xml:",chardata"`
		} `xml:"destinatari"`
		Risposte string `xml:"risposte"`
		Oggetto  string `xml:"oggetto"`
	} `xml:"intestazione"`
	Dati struct {
		Gestore string `xml:"gestore-emittente"`
		Data    struct {
			Zona   string `xml:"zona,attr"`
			Giorno string `xml:"giorno"`
			Ora    string `xml:"ora"`
		} `xml:"data"`
		Identificativo string `xml:"identificativo"`
		MsgID          string `xml:"msgid"`
		Ricevuta       struct {
			Tipo string `xml:"tipo,attr"`
		} `xml:"ricevuta"`
		Consegna     string `xml:"consegna"`
		ErroreEsteso string `xml:"errore-esteso"`
	} `xml:"dati"`
}

// rome is the zone of a daticert date without a zone attribute (S17 §3).
var rome = func() *time.Location {
	if l, err := time.LoadLocation("Europe/Rome"); err == nil {
		return l
	}
	return time.FixedZone("CET", 3600)
}()

// ParseDaticert parses a daticert.xml document. Go's XML decoder never resolves external entities or reads a DTD,
// so XXE is not possible; the size is capped before parsing.
func ParseDaticert(b []byte) (*Certification, error) {
	if len(b) > MaxDaticert {
		return nil, fmt.Errorf("pec: daticert too large (%d bytes)", len(b))
	}
	if bytes.Contains(bytes.ToUpper(b), []byte("<!ENTITY")) {
		return nil, errors.New("pec: daticert declares entities")
	}
	dec := xml.NewDecoder(bytes.NewReader(b))
	dec.Strict = false
	dec.CharsetReader = charsetReader
	var x xmlDaticert
	if err := dec.Decode(&x); err != nil {
		return nil, fmt.Errorf("pec: daticert: %w", err)
	}
	c := &Certification{
		Type:              lower(x.Tipo),
		Error:             lower(x.Errore),
		Sender:            clean(x.Intestazione.Mittente),
		ReplyTo:           clean(x.Intestazione.Risposte),
		Subject:           clean(x.Intestazione.Oggetto),
		Provider:          clean(x.Dati.Gestore),
		TransportID:       clean(x.Dati.Identificativo),
		OriginalMessageID: clean(x.Dati.MsgID),
		ReceiptKind:       lower(x.Dati.Ricevuta.Tipo),
		DeliveredTo:       clean(x.Dati.Consegna),
		ErrorDetail:       capText(clean(x.Dati.ErroreEsteso), 1024),
	}
	for _, d := range x.Intestazione.Destinatari {
		if a := clean(d.Value); a != "" {
			c.Recipients = append(c.Recipients, Recipient{Address: a, Kind: lower(d.Tipo)})
		}
	}
	if c.Type == "" {
		return nil, errors.New("pec: daticert has no type")
	}
	c.At = parseDate(x.Dati.Data.Giorno, x.Dati.Data.Ora, x.Dati.Data.Zona)
	return c, nil
}

// parseDate reads giorno dd/mm/yyyy, ora hh:mm:ss and zona ±hhmm; a missing zone is Europe/Rome.
func parseDate(day, hour, zone string) time.Time {
	day, hour, zone = strings.TrimSpace(day), strings.TrimSpace(hour), strings.TrimSpace(zone)
	if day == "" {
		return time.Time{}
	}
	if hour == "" {
		hour = "00:00:00"
	}
	loc := rome
	if z, err := time.Parse("-0700", zone); err == nil && zone != "" {
		loc = z.Location()
	}
	for _, layout := range []string{"02/01/2006 15:04:05", "2/1/2006 15:04:05", "02/01/2006 15:04"} {
		if t, err := time.ParseInLocation(layout, day+" "+hour, loc); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

func charsetReader(label string, in io.Reader) (io.Reader, error) {
	switch strings.ToLower(strings.TrimSpace(label)) {
	case "", "utf-8", "utf8", "us-ascii":
		return in, nil
	case "iso-8859-1", "latin1", "iso8859-1":
		return charmap.ISO8859_1.NewDecoder().Reader(in), nil
	case "iso-8859-15", "latin9":
		return charmap.ISO8859_15.NewDecoder().Reader(in), nil
	case "windows-1252", "cp1252":
		return charmap.Windows1252.NewDecoder().Reader(in), nil
	}
	return nil, fmt.Errorf("pec: unsupported charset %q", label)
}

func clean(s string) string { return strings.Join(strings.Fields(s), " ") }
func lower(s string) string { return strings.ToLower(clean(s)) }

func capText(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && !utf8Start(s[cut]) {
		cut--
	}
	return s[:cut]
}

func utf8Start(b byte) bool { return b&0xC0 != 0x80 }
