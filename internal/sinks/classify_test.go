package sinks

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"testing"

	"github.com/aws/smithy-go"
	smithyhttp "github.com/aws/smithy-go/transport/http"

	"github.com/radiobros/emailbridge/internal/egress"
	"github.com/radiobros/emailbridge/internal/errs"
)

func awsHTTPErr(status int, msg string) error {
	return &smithyhttp.ResponseError{
		Response: &smithyhttp.Response{Response: &http.Response{StatusCode: status}},
		Err:      errors.New(msg),
	}
}

func TestClassifyAWSTable(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		err  error
		want errs.Kind
	}{
		{"nil", nil, errs.KindInternal},
		{"blocked", egress.ErrBlocked, errs.KindForbidden},
		{"deadline", context.DeadlineExceeded, errs.KindTemporary},
		{"timeout msg", errors.New("i/o timeout"), errs.KindTemporary},
		{"throttle msg", errors.New("Throttling: Rate exceeded"), errs.KindRateLimited},
		{"access denied code", &smithy.GenericAPIError{Code: "AccessDenied", Message: "no"}, errs.KindAuth},
		{"not found code", &smithy.GenericAPIError{Code: "AWS.SimpleQueueService.NonExistentQueue", Message: "x"}, errs.KindNotFound},
		{"invalid param", &smithy.GenericAPIError{Code: "InvalidParameterValue", Message: "x"}, errs.KindValidation},
		{"throttling code", &smithy.GenericAPIError{Code: "Throttling", Message: "x"}, errs.KindRateLimited},
		{"unavailable", &smithy.GenericAPIError{Code: "ServiceUnavailable", Message: "x"}, errs.KindTemporary},
		{"http 403", awsHTTPErr(403, "forbid"), errs.KindAuth},
		{"http 404", awsHTTPErr(404, "gone"), errs.KindNotFound},
		{"http 429", awsHTTPErr(429, "slow"), errs.KindRateLimited},
		{"http 503", awsHTTPErr(503, "down"), errs.KindTemporary},
		{"http 400", awsHTTPErr(400, "bad"), errs.KindPermanent},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := classifyAWS(tc.err); got != tc.want {
				t.Fatalf("got %s want %s", got, tc.want)
			}
		})
	}
}

func TestNATSClassifyTable(t *testing.T) {
	t.Parallel()
	s := &natsSink{}
	cases := []struct {
		err  error
		want errs.Kind
	}{
		{errors.New("Authorization Violation"), errs.KindAuth},
		{errors.New("Permissions Violation"), errs.KindAuth},
		{errors.New("nats: timeout"), errs.KindTemporary},
		{errors.New("connection refused"), errs.KindTemporary},
		{errors.New("invalid subject"), errs.KindValidation},
		{context.DeadlineExceeded, errs.KindTemporary},
	}
	for _, tc := range cases {
		if got := s.Classify(tc.err); got != tc.want {
			t.Fatalf("%v: got %s want %s", tc.err, got, tc.want)
		}
	}
}

func TestKafkaClassifyTable(t *testing.T) {
	t.Parallel()
	s := &kafkaSink{}
	cases := []struct {
		err  error
		want errs.Kind
	}{
		{errors.New("SASL authentication failed"), errs.KindAuth},
		{errors.New("UNKNOWN_TOPIC_OR_PARTITION"), errs.KindNotFound},
		{errors.New("broker not available"), errs.KindTemporary},
		{errors.New("Throttling quota exceeded"), errs.KindRateLimited},
		{fmt.Errorf("dial: %w", &net.OpError{Op: "dial", Err: errors.New("refused")}), errs.KindTemporary},
	}
	for _, tc := range cases {
		if got := s.Classify(tc.err); got != tc.want {
			t.Fatalf("%v: got %s want %s", tc.err, got, tc.want)
		}
	}
}

func TestPubSubClassifyTable(t *testing.T) {
	t.Parallel()
	s := &pubsubSink{}
	cases := []struct {
		err  error
		want errs.Kind
	}{
		{errors.New("rpc error: code = PermissionDenied"), errs.KindAuth},
		{errors.New("rpc error: code = NotFound"), errs.KindNotFound},
		{errors.New("rpc error: code = Unavailable"), errs.KindTemporary},
		{errors.New("rpc error: code = ResourceExhausted"), errs.KindRateLimited},
		{errors.New("rpc error: code = InvalidArgument"), errs.KindValidation},
	}
	for _, tc := range cases {
		if got := s.Classify(tc.err); got != tc.want {
			t.Fatalf("%v: got %s want %s", tc.err, got, tc.want)
		}
	}
}

func TestSQSSNSClassifyDelegates(t *testing.T) {
	t.Parallel()
	err := &smithy.GenericAPIError{Code: "AccessDenied", Message: "no"}
	if (&sqsSink{}).Classify(err) != errs.KindAuth {
		t.Fatal("sqs")
	}
	if (&snsSink{}).Classify(err) != errs.KindAuth {
		t.Fatal("sns")
	}
}
