package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/model-harness/claude-advisor/engine/internal/protocol"
)

func request(t *testing.T, r protocol.Request) protocol.Response {
	t.Helper()
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	return handle(strings.NewReader(string(b)))
}

func TestHandleRejectsBadRequests(t *testing.T) {
	for name, tc := range map[string]struct {
		raw  string
		kind string
	}{
		"not json":       {`{`, protocol.ErrInput},
		"wrong protocol": {`{"protocol":99,"mode":"ask"}`, protocol.ErrInput},
		"bad mode":       {`{"protocol":1,"mode":"run"}`, protocol.ErrInput},
		"no transcript":  {`{"protocol":1,"mode":"prompt"}`, protocol.ErrTranscript},
	} {
		t.Run(name, func(t *testing.T) {
			resp := handle(strings.NewReader(tc.raw))
			if resp.OK || resp.Error == nil || resp.Error.Kind != tc.kind {
				t.Errorf("got %+v, want error kind %q", resp, tc.kind)
			}
		})
	}
}

func TestHandlePromptMode(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := filepath.Join(cfg, "projects")
	path := filepath.Join(root, "p", "s.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","message":{"role":"user","content":"Refactor the parser."}}` + "\n"
	if err := os.WriteFile(path, []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	resp := request(t, protocol.Request{Protocol: 1, Mode: protocol.ModePrompt, TranscriptPath: path, ProjectsRoot: root, Question: "Is this safe?"})
	if !resp.OK {
		t.Fatalf("not ok: %+v", resp.Error)
	}
	if !strings.Contains(resp.Prompt, "USER: Refactor the parser.") || !strings.Contains(resp.Prompt, "Is this safe?") {
		t.Errorf("prompt = %q", resp.Prompt)
	}
	if resp.System == "" || resp.Stats == nil || resp.Stats.Kept != 1 {
		t.Errorf("system or stats missing: %+v", resp)
	}
}

func TestHandleAskNeedsModel(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := filepath.Join(cfg, "projects")
	path := filepath.Join(root, "p", "s.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	resp := request(t, protocol.Request{Protocol: 1, Mode: protocol.ModeAsk, TranscriptPath: path, ProjectsRoot: root})
	if resp.OK || resp.Error == nil || resp.Error.Kind != protocol.ErrInput {
		t.Errorf("got %+v, want an input error", resp)
	}
}
