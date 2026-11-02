package sinks

import (
	"context"
	"errors"
	"net"
	"strings"

	"github.com/radiobros/emailbridge/internal/egress"
	"github.com/radiobros/emailbridge/internal/errs"
)

// kindFromCommon maps shared transport / context errors. Empty string means unknown.
func kindFromCommon(err error) errs.Kind {
	if err == nil {
		return ""
	}
	if errors.Is(err, egress.ErrBlocked) {
		return errs.KindForbidden
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return errs.KindTemporary
	}
	var ne net.Error
	if errors.As(err, &ne) {
		return errs.KindTemporary
	}
	var op *net.OpError
	if errors.As(err, &op) {
		return errs.KindTemporary
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "timeout"), strings.Contains(msg, "temporary"),
		strings.Contains(msg, "connection refused"), strings.Contains(msg, "connection reset"),
		strings.Contains(msg, "i/o timeout"), strings.Contains(msg, "broken pipe"),
		strings.Contains(msg, "no such host"), strings.Contains(msg, "network is unreachable"):
		return errs.KindTemporary
	case strings.Contains(msg, "throttl"), strings.Contains(msg, "rate exceeded"),
		strings.Contains(msg, "too many requests"), strings.Contains(msg, "slow down"):
		return errs.KindRateLimited
	}
	return ""
}

func classifyAWS(err error) errs.Kind {
	if err == nil {
		return errs.KindInternal
	}
	if k := kindFromCommon(err); k != "" {
		return k
	}
	code := awsErrorCode(err)
	status := awsHTTPStatus(err)
	switch strings.ToLower(code) {
	case "accessdenied", "accessdeniedexception", "unrecognizedclientexception",
		"invalidclienttokenid", "signaturedoesnotmatch", "expiredtoken",
		"authfailure", "unauthorizedaccess", "notauthorized":
		return errs.KindAuth
	case "invalidparameter", "invalidparametervalue", "invalidaddress",
		"invalidaction", "validationerror", "malformedquerystring",
		"missingparameter", "invalidattribute name", "invalidattributename":
		return errs.KindValidation
	case "notfound", "queuenamedoesnotexist", "aws.simplequeueservice.nonexistentqueue",
		"notfoundexception", "resourcenotfoundexception", "topicnotfound":
		return errs.KindNotFound
	case "throttling", "throttlingexception", "provisionedthroughputexceededexception",
		"requestlimitexceeded", "toomanyrequests":
		return errs.KindRateLimited
	case "serviceunavailable", "internalerror", "internalfailure",
		"requesttimeout", "priorrequestnotcomplete":
		return errs.KindTemporary
	}
	if status == 401 || status == 403 {
		return errs.KindAuth
	}
	if status == 404 {
		return errs.KindNotFound
	}
	if status == 429 || status == 503 || status == 500 || status == 502 || status == 504 {
		if status == 429 {
			return errs.KindRateLimited
		}
		return errs.KindTemporary
	}
	if status >= 400 && status < 500 {
		return errs.KindPermanent
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "access denied"), strings.Contains(msg, "not authorized"),
		strings.Contains(msg, "security token"):
		return errs.KindAuth
	case strings.Contains(msg, "does not exist"), strings.Contains(msg, "not found"):
		return errs.KindNotFound
	default:
		return errs.KindTemporary
	}
}
