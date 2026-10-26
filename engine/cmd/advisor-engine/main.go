// Command advisor-engine is the local backend of the bedrock-advisor Claude
// Code plugin. It reads an agent's transcript, builds an advisor prompt, and
// calls the advisor model on Amazon Bedrock.
//
//	advisor-engine ask      read a protocol.Request on stdin, write a Response
//	advisor-engine doctor   check AWS credentials and region (--ping to call a model)
//	advisor-engine version
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/model-harness/claude-advisor/engine/internal/advise"
	"github.com/model-harness/claude-advisor/engine/internal/bedrock"
	"github.com/model-harness/claude-advisor/engine/internal/protocol"
	"github.com/model-harness/claude-advisor/engine/internal/transcript"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

const (
	defaultMaxTokens  = 2048
	defaultMaxContext = 400_000
	defaultTimeout    = 240 * time.Second
	maxRequestBytes   = 1 << 20
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "ask":
		os.Exit(ask(os.Stdin, os.Stdout))
	case "doctor":
		os.Exit(doctor(os.Args[2:], os.Stdout))
	case "version", "--version", "-v":
		fmt.Printf("{\"version\":%q,\"protocol\":%d}\n", version, protocol.Version)
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: advisor-engine ask|doctor [--ping --model ID --region R]|version")
}

func ask(in io.Reader, out io.Writer) int {
	resp := handle(in)
	resp.Protocol = protocol.Version
	if err := json.NewEncoder(out).Encode(resp); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

func fail(kind, format string, a ...any) protocol.Response {
	return protocol.Response{Error: &protocol.ErrorInfo{Kind: kind, Message: fmt.Sprintf(format, a...)}}
}

func handle(in io.Reader) protocol.Response {
	raw, err := io.ReadAll(io.LimitReader(in, maxRequestBytes+1))
	if err != nil {
		return fail(protocol.ErrInput, "read request: %v", err)
	}
	if len(raw) > maxRequestBytes {
		return fail(protocol.ErrInput, "request is larger than %d bytes", maxRequestBytes)
	}
	var req protocol.Request
	if err := json.Unmarshal(raw, &req); err != nil {
		return fail(protocol.ErrInput, "parse request: %v", err)
	}
	if req.Protocol != protocol.Version {
		return fail(protocol.ErrInput, "protocol %d not supported; this engine speaks %d, reinstall to match the plugin", req.Protocol, protocol.Version)
	}
	if req.Mode != protocol.ModeAsk && req.Mode != protocol.ModePrompt {
		return fail(protocol.ErrInput, "mode must be %q or %q", protocol.ModeAsk, protocol.ModePrompt)
	}
	maxChars := req.MaxContextChars
	if maxChars <= 0 {
		maxChars = defaultMaxContext
	}
	text, stats, err := transcript.RenderFile(req.TranscriptPath, req.ProjectsRoot, maxChars)
	if err != nil {
		return fail(protocol.ErrTranscript, "transcript: %v", err)
	}
	prompt := advise.Prompt(text, req.Question, req.Trigger, req.Detail)
	if req.Mode == protocol.ModePrompt {
		return protocol.Response{OK: true, System: advise.System, Prompt: prompt, Stats: &stats}
	}

	if strings.TrimSpace(req.Model) == "" {
		return fail(protocol.ErrInput, "model is required in ask mode")
	}
	timeout := defaultTimeout
	if req.TimeoutMs > 0 {
		timeout = time.Duration(req.TimeoutMs) * time.Millisecond
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cfg, err := bedrock.Load(ctx, bedrock.Region(req.Region))
	if err != nil {
		return fail(protocol.ErrCredentials, "load AWS config: %s", bedrock.Sanitize(err.Error()))
	}
	if _, err := bedrock.Ready(ctx, cfg); err != nil {
		r := fail(bedrock.Classify(err), "%s", bedrock.Sanitize(err.Error()))
		r.Stats = &stats
		return r
	}
	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = defaultMaxTokens
	}
	res, err := bedrock.Converse(ctx, cfg, req.Model, advise.System, prompt, maxTokens)
	if err != nil {
		r := fail(bedrock.Classify(err), "bedrock: %s", bedrock.Sanitize(err.Error()))
		r.Stats = &stats
		return r
	}
	return protocol.Response{OK: true, Text: res.Text, Model: req.Model, Usage: &res.Usage, Stats: &stats}
}

type doctorReport struct {
	Version     string `json:"version"`
	Protocol    int    `json:"protocol"`
	Region      string `json:"region"`
	Credentials string `json:"credentials"`
	Source      string `json:"credentialSource,omitempty"`
	Ping        string `json:"ping,omitempty"`
	OK          bool   `json:"ok"`
	Error       string `json:"error,omitempty"`
}

func doctor(args []string, out io.Writer) int {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	ping := fs.Bool("ping", false, "send a one-line request to the model")
	model := fs.String("model", "", "model or inference profile id for --ping")
	region := fs.String("region", "", "AWS region (default AWS_REGION)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	rep := doctorReport{Version: version, Protocol: protocol.Version, Region: bedrock.Region(*region)}
	defer func() { _ = json.NewEncoder(out).Encode(rep) }()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cfg, err := bedrock.Load(ctx, rep.Region)
	if err != nil {
		rep.Credentials, rep.Error = "error", bedrock.Sanitize(err.Error())
		return 1
	}
	rep.Region = cfg.Region
	source, err := bedrock.Ready(ctx, cfg)
	if err != nil {
		rep.Credentials, rep.Error = "missing", bedrock.Sanitize(err.Error())
		return 1
	}
	rep.Credentials, rep.Source = "ok", source
	if *ping {
		if *model == "" {
			rep.Error = "--ping needs --model"
			return 2
		}
		res, err := bedrock.Converse(ctx, cfg, *model, "Reply with the single word OK.", "ping", 16)
		if err != nil {
			rep.Ping, rep.Error = bedrock.Classify(err), bedrock.Sanitize(err.Error())
			return 1
		}
		rep.Ping = "ok: " + res.Text
	}
	rep.OK = true
	return 0
}
