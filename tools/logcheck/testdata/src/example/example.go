package example

import (
	"context"
	"fmt"
	"log/slog"
	"net/mail"
)

func f(log *slog.Logger, ctx context.Context, user string, body []byte, m *mail.Message) {
	log.Info("constant ok", "user", user)
	log.Info(fmt.Sprintf("hello %s", user)) // want `log message must be a constant`
	msg := "x"
	log.InfoContext(ctx, msg)     // want `log message must be a constant`
	log.Info("body", "raw", body) // want `do not log raw bytes`
	log.Info("mail", "m", m)      // want `do not log net/mail.Message`
	_ = slog.Any("raw", body)     // want `do not log raw bytes`
	log.Log(ctx, slog.LevelWarn, "ok", "n", len(body))
	slog.Error("pkg func " + user)            // want `log message must be a constant`
	log.Info(fmt.Sprintf("allowed %s", user)) //logcheck:allow migration message built from constants
	const c = "typed constant"
	log.Warn(c)
}
