package sinks

import (
	"context"
	"errors"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/smithy-go"
	smithyhttp "github.com/aws/smithy-go/transport/http"

	"github.com/radiobros/emailbridge/internal/egress"
)

func loadAWSConfig(ctx context.Context, region, endpoint, accessKey, secretKey string, dial *egress.Dialer) (aws.Config, error) {
	opts := []func(*config.LoadOptions) error{
		config.WithRegion(region),
	}
	if accessKey != "" {
		opts = append(opts, config.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(accessKey, secretKey, ""),
		))
	}
	if dial != nil {
		opts = append(opts, config.WithHTTPClient(dial.HTTPClient(30*time.Second)))
	}
	cfg, err := config.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return aws.Config{}, safeErr("sinks.aws", err)
	}
	if endpoint != "" {
		// Egress is enforced by the HTTP client dialer when Dial is set.
		// When Dial is nil (tests without policy), skip host pre-check.
		if dial != nil {
			if host, err := hostFromEndpoint(endpoint); err == nil {
				if err := dial.CheckURLHost(ctx, host, egress.HTTP); err != nil {
					return aws.Config{}, err
				}
			}
		}
		cfg.BaseEndpoint = aws.String(endpoint)
	}
	return cfg, nil
}

func hostFromEndpoint(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	host := raw
	if strings.Contains(raw, "://") {
		u, err := url.Parse(raw)
		if err != nil {
			return "", err
		}
		host = u.Host
	}
	if h, _, err := net.SplitHostPort(host); err == nil {
		return h, nil
	}
	return host, nil
}

func awsErrorCode(err error) string {
	var api smithy.APIError
	if errors.As(err, &api) {
		return api.ErrorCode()
	}
	return ""
}

func awsHTTPStatus(err error) int {
	var re *smithyhttp.ResponseError
	if errors.As(err, &re) {
		return re.HTTPStatusCode()
	}
	return 0
}
