// Package bedrock calls an advisor model through the Bedrock Converse API,
// using the AWS SDK's default credential chain.
package bedrock

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"github.com/aws/smithy-go"

	"github.com/model-harness/claude-advisor/engine/internal/protocol"
)

// Region picks the request's region, then AWS_REGION, then
// AWS_DEFAULT_REGION. Empty means the SDK's shared config decides.
func Region(requested string) string {
	for _, r := range []string{requested, os.Getenv("AWS_REGION"), os.Getenv("AWS_DEFAULT_REGION")} {
		if r = strings.TrimSpace(r); r != "" {
			return r
		}
	}
	return ""
}

// Load builds an AWS config for region.
func Load(ctx context.Context, region string) (aws.Config, error) {
	var opts []func(*config.LoadOptions) error
	if region != "" {
		opts = append(opts, config.WithRegion(region))
	}
	return config.LoadDefaultConfig(ctx, opts...)
}

// ErrNoRegion means neither the request, the environment nor the shared AWS
// config names a region.
var ErrNoRegion = errors.New("no AWS region: set AWS_REGION")

// credentialError marks a failure to obtain credentials, whatever service
// (SSO, STS, IMDS) reported it.
type credentialError struct{ err error }

func (e credentialError) Error() string { return "AWS credentials: " + e.err.Error() }
func (e credentialError) Unwrap() error { return e.err }

// UsesBearerToken reports whether the SDK will authenticate Bedrock calls
// with a Bedrock API key instead of SigV4 credentials.
func UsesBearerToken() bool { return strings.TrimSpace(os.Getenv("AWS_BEARER_TOKEN_BEDROCK")) != "" }

// Ready checks that cfg has a region and usable credentials, so a credential
// failure is reported as one before any model call.
func Ready(ctx context.Context, cfg aws.Config) (source string, err error) {
	if cfg.Region == "" {
		return "", ErrNoRegion
	}
	if UsesBearerToken() {
		return "AWS_BEARER_TOKEN_BEDROCK", nil
	}
	creds, err := cfg.Credentials.Retrieve(ctx)
	if err != nil {
		// Out of time is not a credential problem; report it as a timeout.
		if ctx.Err() != nil {
			return "", fmt.Errorf("AWS credentials: %w", ctx.Err())
		}
		return "", credentialError{err}
	}
	return creds.Source, nil
}

// Result is one advisor reply.
type Result struct {
	Text  string
	Usage protocol.Usage
}

// Converse sends one system prompt and one user message to model.
func Converse(ctx context.Context, cfg aws.Config, model, system, prompt string, maxTokens int) (Result, error) {
	client := bedrockruntime.NewFromConfig(cfg)
	out, err := client.Converse(ctx, &bedrockruntime.ConverseInput{
		ModelId: aws.String(model),
		System:  []types.SystemContentBlock{&types.SystemContentBlockMemberText{Value: system}},
		Messages: []types.Message{{
			Role:    types.ConversationRoleUser,
			Content: []types.ContentBlock{&types.ContentBlockMemberText{Value: prompt}},
		}},
		InferenceConfig: &types.InferenceConfiguration{MaxTokens: aws.Int32(clampTokens(maxTokens))},
	})
	if err != nil {
		return Result{}, err
	}
	var res Result
	if msg, ok := out.Output.(*types.ConverseOutputMemberMessage); ok {
		var parts []string
		for _, c := range msg.Value.Content {
			if t, ok := c.(*types.ContentBlockMemberText); ok {
				parts = append(parts, t.Value)
			}
		}
		res.Text = strings.TrimSpace(strings.Join(parts, "\n"))
	}
	if out.Usage != nil {
		res.Usage = protocol.Usage{
			InputTokens:  int(aws.ToInt32(out.Usage.InputTokens)),
			OutputTokens: int(aws.ToInt32(out.Usage.OutputTokens)),
		}
	}
	if res.Text == "" {
		return res, fmt.Errorf("advisor returned no text (stop reason %q)", out.StopReason)
	}
	return res, nil
}

func clampTokens(n int) int32 {
	switch {
	case n <= 0:
		return 2048
	case n > 64000:
		return 64000
	default:
		return int32(n) // #nosec G115 -- bounded above
	}
}

// Classify maps an error to a protocol error kind, so the plugin can decide
// whether to fall back to Claude Code's own model call.
func Classify(err error) string {
	var cred credentialError
	if errors.As(err, &cred) || errors.Is(err, ErrNoRegion) {
		return protocol.ErrCredentials
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return protocol.ErrTimeout
	}
	var api smithy.APIError
	if errors.As(err, &api) {
		switch api.ErrorCode() {
		case "AccessDeniedException", "UnrecognizedClientException", "ExpiredTokenException", "InvalidSignatureException":
			return protocol.ErrAccessDenied
		case "ThrottlingException", "ServiceQuotaExceededException", "TooManyRequestsException":
			return protocol.ErrThrottled
		}
		return protocol.ErrAPI
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "credential") || strings.Contains(msg, "no ec2 imds role") || strings.Contains(msg, "sso token") || strings.Contains(msg, "sso session") {
		return protocol.ErrCredentials
	}
	return protocol.ErrAPI
}

var (
	arnPattern     = regexp.MustCompile(`arn:aws[a-zA-Z-]*:[^\s"',)]+`)
	accountPattern = regexp.MustCompile(`\b\d{12}\b`)
)

// Sanitize removes ARNs and account ids from an error message before it can
// reach an agent's transcript.
func Sanitize(msg string) string {
	return accountPattern.ReplaceAllString(arnPattern.ReplaceAllString(msg, "arn:…"), "<account>")
}
