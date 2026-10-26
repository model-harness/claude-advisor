package bedrock

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/smithy-go"

	"github.com/model-harness/claude-advisor/engine/internal/protocol"
)

type staticCreds struct{ err error }

func (s staticCreds) Retrieve(context.Context) (aws.Credentials, error) {
	return aws.Credentials{AccessKeyID: "AKID", SecretAccessKey: "secret", Source: "test"}, s.err
}

func TestClassify(t *testing.T) {
	for name, tc := range map[string]struct {
		err  error
		want string
	}{
		"credential error":  {credentialError{&smithy.GenericAPIError{Code: "UnauthorizedException"}}, protocol.ErrCredentials},
		"no region":         {fmt.Errorf("load: %w", ErrNoRegion), protocol.ErrCredentials},
		"deadline":          {fmt.Errorf("call: %w", context.DeadlineExceeded), protocol.ErrTimeout},
		"access denied":     {&smithy.GenericAPIError{Code: "AccessDeniedException"}, protocol.ErrAccessDenied},
		"expired token":     {&smithy.GenericAPIError{Code: "ExpiredTokenException"}, protocol.ErrAccessDenied},
		"throttled":         {&smithy.GenericAPIError{Code: "ThrottlingException"}, protocol.ErrThrottled},
		"validation":        {&smithy.GenericAPIError{Code: "ValidationException"}, protocol.ErrAPI},
		"sso text fallback": {errors.New("failed to refresh cached SSO token"), protocol.ErrCredentials},
		"sso substring":     {errors.New("processor overloaded"), protocol.ErrAPI},
		"other":             {errors.New("connection reset"), protocol.ErrAPI},
	} {
		if got := Classify(tc.err); got != tc.want {
			t.Errorf("%s: Classify = %q, want %q", name, got, tc.want)
		}
	}
}

func TestReady(t *testing.T) {
	ctx := context.Background()
	t.Setenv("AWS_BEARER_TOKEN_BEDROCK", "")
	if _, err := Ready(ctx, aws.Config{}); !errors.Is(err, ErrNoRegion) {
		t.Errorf("no region: got %v", err)
	}
	failing := aws.Config{Region: "eu-west-1", Credentials: staticCreds{errors.New("sso expired")}}
	if _, err := Ready(ctx, failing); Classify(err) != protocol.ErrCredentials {
		t.Errorf("failing credentials: got %v (%s)", err, Classify(err))
	}
	expired, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := Ready(expired, failing); Classify(err) == protocol.ErrCredentials {
		t.Errorf("a cancelled credential fetch was classed as credentials: %v", err)
	}
	ok := aws.Config{Region: "eu-west-1", Credentials: staticCreds{}}
	if src, err := Ready(ctx, ok); err != nil || src != "test" {
		t.Errorf("good credentials: %q, %v", src, err)
	}
	t.Setenv("AWS_BEARER_TOKEN_BEDROCK", "token")
	if src, err := Ready(ctx, failing); err != nil || src != "AWS_BEARER_TOKEN_BEDROCK" {
		t.Errorf("bearer token: %q, %v", src, err)
	}
}

func TestSanitize(t *testing.T) {
	in := "User: arn:aws:iam::111122223333:user/example-user is not authorized on arn:aws:bedrock:eu-west-1:111122223333:inference-profile/global.anthropic.claude-opus-5, account 111122223333"
	out := Sanitize(in)
	if strings.Contains(out, "111122223333") || strings.Contains(out, "example-user") || strings.Contains(out, "inference-profile") {
		t.Errorf("Sanitize left identifiers: %q", out)
	}
	if !strings.Contains(out, "is not authorized") {
		t.Errorf("Sanitize dropped the message: %q", out)
	}
}
