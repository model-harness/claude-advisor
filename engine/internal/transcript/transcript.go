// Package transcript turns a Claude Code session or subagent JSONL transcript
// into compact text an advisor model can read.
//
// User and assistant messages are kept, with the context hooks attached after
// tool results. Other attachments, API request snapshots, meta messages and
// thinking blocks are dropped: in a typical subagent transcript they are most
// of the bytes and none of the work.
package transcript

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unicode/utf8"

	"github.com/model-harness/claude-advisor/engine/internal/protocol"
)

// Limits for single blocks, so one huge tool result can't crowd out the rest.
const (
	maxTextChars       = 8000
	maxToolInputChars  = 2000
	maxToolResultChars = 4000
	// The first entry is the agent's task; it is always kept, up to this share
	// of the budget.
	taskShare = 5
)

// ErrOutsideRoot is returned when a transcript path is not under the
// projects root.
var ErrOutsideRoot = errors.New("transcript path is outside the projects root")

type record struct {
	Type       string      `json:"type"`
	IsMeta     bool        `json:"isMeta"`
	Message    *message    `json:"message"`
	Attachment *attachment `json:"attachment"`
}

// attachment is a record Claude Code adds beside the conversation. Only
// hook_additional_context is kept: it is text a hook gave the agent after a
// tool result, such as this plugin's own earlier advice.
type attachment struct {
	Type    string          `json:"type"`
	Content json.RawMessage `json:"content"`
}

type message struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type block struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
	ToolUseID string          `json:"tool_use_id"`
}

// ProjectsRoot is the Claude Code projects directory on this machine:
// $CLAUDE_CONFIG_DIR/projects, or ~/.claude/projects.
func ProjectsRoot() (string, error) {
	dir := os.Getenv("CLAUDE_CONFIG_DIR")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(home, ".claude")
	}
	return filepath.Abs(filepath.Join(dir, "projects"))
}

// Open opens a transcript for reading. The file must be a regular .jsonl file
// inside the projects root, which the engine derives itself; requestedRoot,
// when set, must name the same directory. The file is opened through an
// os.Root, so neither ".." nor a symlink can leave the root.
func Open(path, requestedRoot string) (*os.File, error) {
	root, err := ProjectsRoot()
	if err != nil {
		return nil, fmt.Errorf("projects root: %w", err)
	}
	if requestedRoot != "" {
		req, err := filepath.Abs(requestedRoot)
		if err != nil || !samePath(req, root) {
			return nil, fmt.Errorf("projectsRoot %q is not this machine's Claude Code projects directory %q", requestedRoot, root)
		}
	}
	if path == "" {
		return nil, errors.New("transcriptPath is required")
	}
	if !strings.EqualFold(filepath.Ext(path), ".jsonl") {
		return nil, errors.New("transcript path must be a .jsonl file")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return nil, ErrOutsideRoot
	}
	r, err := os.OpenRoot(root)
	if err != nil {
		return nil, fmt.Errorf("projects root: %w", err)
	}
	defer r.Close()
	// Stat before opening, so a FIFO or device can't block the open.
	if st, err := r.Stat(rel); err != nil {
		return nil, classifyRootErr(err)
	} else if !st.Mode().IsRegular() {
		return nil, errors.New("transcript is not a regular file")
	}
	f, err := r.Open(rel)
	if err != nil {
		return nil, classifyRootErr(err)
	}
	if st, err := f.Stat(); err != nil || !st.Mode().IsRegular() {
		_ = f.Close() // read-only; the error that matters is the one returned
		return nil, errors.New("transcript is not a regular file")
	}
	return f, nil
}

func samePath(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// classifyRootErr reports an os.Root escape as ErrOutsideRoot.
func classifyRootErr(err error) error {
	if strings.Contains(err.Error(), "path escapes from parent") {
		return ErrOutsideRoot
	}
	return err
}

// RenderFile reads and renders the transcript at path.
func RenderFile(path, requestedRoot string, maxChars int) (string, protocol.Stats, error) {
	f, err := Open(path, requestedRoot)
	if err != nil {
		return "", protocol.Stats{}, err
	}
	defer f.Close()
	return Render(f, maxChars)
}

// Render reads JSONL from r and returns the conversation as text that fits in
// maxChars. It keeps the first entry (the task) and as many of the newest
// entries as fit, and marks what it left out.
func Render(r io.Reader, maxChars int) (string, protocol.Stats, error) {
	var stats protocol.Stats
	var entries []string
	br := bufio.NewReaderSize(r, 1<<20)
	for {
		line, err := br.ReadBytes('\n')
		stats.RawBytes += len(line)
		if len(strings.TrimSpace(string(line))) > 0 {
			stats.Records++
			if e := renderLine(line); e != "" {
				entries = append(entries, e)
			}
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", stats, err
		}
	}
	out, kept := fit(entries, maxChars)
	stats.Kept = kept
	stats.Omitted = len(entries) - kept
	stats.Chars = runes(out)
	return out, stats, nil
}

func renderLine(line []byte) string {
	var rec record
	if err := json.Unmarshal(line, &rec); err != nil {
		// A partly written last line, or a record type we don't know.
		return ""
	}
	if rec.Type == "attachment" && rec.Attachment != nil && rec.Attachment.Type == "hook_additional_context" {
		var parts []string
		if err := json.Unmarshal(rec.Attachment.Content, &parts); err != nil {
			var one string
			if json.Unmarshal(rec.Attachment.Content, &one) != nil {
				return ""
			}
			parts = []string{one}
		}
		var out []string
		for _, p := range parts {
			if strings.TrimSpace(p) != "" {
				out = append(out, entry("CONTEXT", clip(p, maxTextChars)))
			}
		}
		return strings.Join(out, "\n")
	}
	if (rec.Type != "user" && rec.Type != "assistant") || rec.IsMeta || rec.Message == nil {
		return ""
	}
	role := strings.ToUpper(rec.Message.Role)
	if role == "" {
		role = strings.ToUpper(rec.Type)
	}
	var s string
	if err := json.Unmarshal(rec.Message.Content, &s); err == nil {
		if strings.TrimSpace(s) == "" {
			return ""
		}
		return entry(role, clip(s, maxTextChars))
	}
	var blocks []block
	if err := json.Unmarshal(rec.Message.Content, &blocks); err != nil {
		return ""
	}
	var parts []string
	for _, b := range blocks {
		switch b.Type {
		case "text":
			if strings.TrimSpace(b.Text) != "" {
				parts = append(parts, entry(role, clip(b.Text, maxTextChars)))
			}
		case "tool_use":
			parts = append(parts, entry("TOOL CALL "+oneLine(b.Name), clip(compact(b.Input), maxToolInputChars)))
		case "tool_result":
			label := "TOOL RESULT"
			if b.IsError {
				label = "TOOL ERROR"
			}
			parts = append(parts, entry(label, clip(resultText(b.Content), maxToolResultChars)))
		}
	}
	return strings.Join(parts, "\n")
}

// entry labels one transcript entry. Continuation lines are indented, so only
// a real entry starts a line with a label and quoted text can't pose as one.
func entry(label, text string) string {
	return label + ": " + strings.ReplaceAll(text, "\n", "\n  ")
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// resultText flattens a tool_result's content, which is a string or a list of
// blocks.
func resultText(raw json.RawMessage) string {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var blocks []block
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return compact(raw)
	}
	var parts []string
	for _, b := range blocks {
		switch b.Type {
		case "text":
			parts = append(parts, b.Text)
		case "":
		default:
			parts = append(parts, "["+b.Type+"]")
		}
	}
	return strings.Join(parts, "\n")
}

// compact removes insignificant whitespace from JSON, leaving whitespace
// inside strings, such as an Edit's indentation, as it is.
func compact(raw json.RawMessage) string {
	if len(raw) == 0 {
		return "{}"
	}
	var b bytes.Buffer
	if err := json.Compact(&b, raw); err != nil {
		return string(raw)
	}
	return b.String()
}

func runes(s string) int { return utf8.RuneCountInString(s) }

// clipMarker says the advisor's copy was shortened, so the advisor doesn't
// mistake it for output the agent itself never saw.
func clipMarker(removed int) string {
	return fmt.Sprintf("\n…[%d characters left out of the advisor's copy; the agent saw the full text]…\n", removed)
}

// clip keeps the head and tail of s within n characters, marker included.
func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n <= 0 {
		return ""
	}
	// Size the marker for the most it could report, so the real one fits.
	keep := n - runes(clipMarker(len(r)))
	if keep <= 0 {
		return string(r[:n])
	}
	head := keep / 2
	return string(r[:head]) + clipMarker(len(r)-keep) + string(r[len(r)-(keep-head):])
}

func omittedMarker(n int) string { return fmt.Sprintf("[… %d earlier entries omitted …]", n) }

// fit joins the task and the newest entries within maxChars characters. It
// returns the text and how many entries it kept.
func fit(entries []string, maxChars int) (string, int) {
	if len(entries) == 0 || maxChars <= 0 {
		return "", 0
	}
	const sep = "\n\n"
	task := clip(entries[0], maxChars/taskShare)
	if task == "" {
		return "", 0
	}
	// Reserve room for the omitted marker up front; it may not be needed.
	budget := maxChars - runes(task)
	if len(entries) > 1 {
		budget -= runes(sep + omittedMarker(len(entries)-1))
	}
	var tail []string
	for i := len(entries) - 1; i >= 1 && budget > 0; i-- {
		need := runes(entries[i]) + runes(sep)
		if need > budget {
			break
		}
		budget -= need
		tail = append(tail, entries[i])
	}
	for i, j := 0, len(tail)-1; i < j; i, j = i+1, j-1 {
		tail[i], tail[j] = tail[j], tail[i]
	}
	parts := []string{task}
	if omitted := len(entries) - 1 - len(tail); omitted > 0 && budget >= 0 {
		parts = append(parts, omittedMarker(omitted))
	}
	parts = append(parts, tail...)
	return strings.Join(parts, sep), 1 + len(tail)
}
