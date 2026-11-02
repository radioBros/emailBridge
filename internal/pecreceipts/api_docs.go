package pecreceipts

import "github.com/danielgtaylor/huma/v2"

// OpenAPI examples of the PEC receipt operations (W70). Documentation only: the lint validates every example
// against the generated schema.

const (
	exampleReceiptID  = "rcp_01m5d20y80n4ps0apz3e6baqq3"
	exampleReceipt2ID = "rcp_01m5dsa5x0v78ag81yce7hjpr3"
	exampleOrgID      = "org_01m5caqpk0kast63yc13wpy2d9"
	exampleAccountID  = "acc_01m5cvnpb031g57sqct6mzhq94"
	exampleEvidence   = "evidence/" + exampleOrgID + "/" + exampleAccountID + "/2026/10/" + exampleReceiptID
)

func jsonContent(v any) map[string]*huma.MediaType {
	return map[string]*huma.MediaType{"application/json": {Example: v}}
}

func exampleDeliveryReceipt() map[string]any {
	return map[string]any{
		"id": exampleReceiptID, "accountId": exampleAccountID, "messageId": "msg_01m5cnaee070z834j1neyr6a58",
		"outboundId": "out_01m5cqe6d02twj9xw9b9vyh5b4", "type": "avvenuta-consegna", "receiptKind": "completa",
		"error":                   "nessuno",
		"transportId":             "opec210312.20261027093006.123456.789.1.53@pec.example.it",
		"originalMessageIdHeader": "<opec210312.20261027093007.654321.111.1.53@pec.example.it>",
		"refMessageIdHeader":      "<01m5cqe6d02twj9xw9b9vyh5b4@mail.example.com>",
		"sender":                  "reception@pec.example.com",
		"recipients":              []any{map[string]any{"address": "studio.rossi@pec.example.it", "kind": "certificato"}},
		"deliveredTo":             "studio.rossi@pec.example.it", "provider": "pec.example.it",
		"at": "2026-10-27T08:30:07Z",
		"signature": map[string]any{"valid": true, "trusted": true,
			"signer":   "CN=posta-certificata@pec.example.it, O=Example PEC S.p.A., C=IT",
			"issuer":   "CN=Example PEC CA, O=Example PEC S.p.A., C=IT",
			"notAfter": "2027-06-30T23:59:59Z"},
		"sha256":    "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08",
		"evidence":  map[string]any{"key": exampleEvidence + ".eml", "lockedUntil": "2036-10-27T08:30:09Z", "tsa": true},
		"createdAt": "2026-10-27T08:30:09Z",
	}
}

func exampleAcceptanceReceipt() map[string]any {
	return map[string]any{
		"id": exampleReceipt2ID, "accountId": exampleAccountID, "messageId": "msg_01m5dq6dy0bpw4pexpdp4cjcn0",
		"outboundId": nil, "type": "accettazione", "error": "nessuno",
		"transportId":        "opec210312.20261026161204.998877.66.1.53@pec.example.com",
		"refMessageIdHeader": "<4f2a9c1e.20261026161200@pc-reception.example.com>",
		"sender":             "reception@pec.example.com",
		"recipients": []any{
			map[string]any{"address": "ufficio.protocollo@pec.example.it", "kind": "certificato"},
			map[string]any{"address": "info@example.org", "kind": "esterno"},
		},
		"provider": "pec.example.com", "at": "2026-10-26T16:12:04Z",
		"signature": map[string]any{"valid": true, "trusted": nil,
			"signer": "CN=posta-certificata@pec.example.com, O=Example Mail S.p.A., C=IT",
			"issuer": "CN=Example Mail CA, O=Example Mail S.p.A., C=IT", "notAfter": "2027-03-31T23:59:59Z"},
		"sha256":    "2c26b46b68ffc68ff99b453c1d30413413422d706483bfa0f98a5e886266e7ae",
		"createdAt": "2026-10-26T16:12:06Z",
	}
}

func exampleReceiptList() map[string]any {
	return map[string]any{
		"data":       []any{exampleDeliveryReceipt(), exampleAcceptanceReceipt()},
		"nextCursor": "eyJpZCI6InJjcF8wMW01ZHNhNXgwdjc4YWc4MXljZTdoanByMyIsImZpZWxkIjoiYXQiLCJkZXNjIjp0cnVlfQ",
	}
}

func exampleEvidenceView() map[string]any {
	return map[string]any{
		"receiptId": exampleReceiptID, "key": exampleEvidence + ".eml",
		"sha256": "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08",
		"verification": map[string]any{"intact": true, "hashMatches": true, "locked": true,
			"lockedUntil": "2036-10-27T08:30:09Z", "tsa": map[string]any{"valid": true, "at": "2026-10-27T08:30:10Z"}},
		"manifestKey": exampleEvidence + ".json",
	}
}

// exampleRawReceipt is the start of a signed PEC receipt (message/rfc822).
const exampleRawReceipt = "Return-Path: <posta-certificata@pec.example.it>\r\n" +
	"Message-ID: <opec210312.20261027093007.654321.111.1.53@pec.example.it>\r\n" +
	"X-Ricevuta: avvenuta-consegna\r\n" +
	"X-Riferimento-Message-ID: <01m5cqe6d02twj9xw9b9vyh5b4@mail.example.com>\r\n" +
	"X-Trasporto: posta-certificata\r\n" +
	"Date: Tue, 27 Oct 2026 09:30:07 +0100\r\n" +
	"Subject: CONSEGNA: Your appointment on 4 November\r\n" +
	"From: \"Per conto di: studio.rossi@pec.example.it\" <posta-certificata@pec.example.it>\r\n" +
	"To: reception@pec.example.com\r\n" +
	"MIME-Version: 1.0\r\n" +
	"Content-Type: multipart/signed; protocol=\"application/pkcs7-signature\"; micalg=sha-256; boundary=\"----B1\"\r\n" +
	"\r\n" +
	"------B1\r\n…"
