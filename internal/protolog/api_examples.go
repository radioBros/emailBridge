package protolog

// OpenAPI examples of the protocol-log operations (W70). Documentation only.

const exampleAccountID = "acc_01m5cvnpb031g57sqct6mzhq94"

func exampleProtocolLogList() map[string]any {
	conn := exampleAccountID + "/main"
	return map[string]any{
		"data": []any{
			map[string]any{"id": "log_01m5dckp30mdexhj4qp140dym4", "time": 1792833262318000000, "dir": "C", "proto": "imap",
				"connId": conn, "line": "A0007 LOGIN reception@example.com ****", "bytes": 42, "accountId": exampleAccountID},
			map[string]any{"id": "log_01m5deqe20xh52jw94xna6cv58", "time": 1792833262360000000, "dir": "S", "proto": "imap",
				"connId": conn, "line": "A0007 OK [CAPABILITY IMAP4rev1 IDLE MOVE UIDPLUS] Logged in", "bytes": 58,
				"accountId": exampleAccountID},
		},
		"nextCursor": "eyJ0IjoxNzkyODMzMjYyMzYwMDAwMDAwLCJpZCI6ImxvZ18wMW01ZGVxZTIweGg1Mmp3OTR4bmE2Y3Y1OCJ9",
	}
}

const exampleProtocolLogNDJSON = `{"id":"log_01m5dckp30mdexhj4qp140dym4","time":1792833262318000000,"dir":"C","proto":"imap","connId":"` +
	exampleAccountID + `/main","line":"A0012 UID FETCH 48213 (FLAGS)","bytes":30,"accountId":"` + exampleAccountID + `"}
{"id":"log_01m5deqe20xh52jw94xna6cv58","time":1792833262360000000,"dir":"S","proto":"imap","connId":"` +
	exampleAccountID + `/main","line":"* 1 FETCH (UID 48213 FLAGS (\\Seen))","bytes":35,"accountId":"` + exampleAccountID + `"}
`
