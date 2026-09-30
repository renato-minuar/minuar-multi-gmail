package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/renato-minuar/minuar-multi-gmail/internal/config"
	"github.com/renato-minuar/minuar-multi-gmail/internal/secrets"
	"github.com/renato-minuar/minuar-multi-gmail/internal/tools"
)

func buildBinary(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "minuar-multi-gmail")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("go build: %v", err)
	}
	return bin
}

// TestServeOverStdio starts the real binary the way Claude Code does and
// talks MCP to it. It never touches the real Keychain: the only calls made
// resolve before any service is built.
func TestServeOverStdio(t *testing.T) {
	bin := buildBinary(t)
	cfgDir := t.TempDir()
	cfg := &config.File{Version: 1}
	if err := cfg.Add(config.Account{Alias: "work", Email: "control@example.com", AddedAt: time.Now(),
		Description: "the company mailbox; use it by default"}); err != nil {
		t.Fatal(err)
	}
	if err := cfg.Add(config.Account{Alias: "personal", Email: "p@gmail.com", AddedAt: time.Now(),
		Description: "the personal mailbox; use it when they say personal"}); err != nil {
		t.Fatal(err)
	}
	if err := config.Save(cfgDir, cfg); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.Command(bin, "serve")
	cmd.Env = append(os.Environ(), config.EnvDir+"="+cfgDir, envAttachmentDir+"="+t.TempDir(), secrets.EnvKind+"=file", secrets.EnvService+"=com.minuar.multi-gmail.test")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	client := mcp.NewClient(&mcp.Implementation{Name: "minuar-multi-gmail-test", Version: "0"}, nil)
	session, err := client.Connect(ctx, &mcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		t.Fatalf("connect: %v\nstderr:\n%s", err, stderr.String())
	}
	defer session.Close()

	init := session.InitializeResult()
	if init == nil {
		t.Fatal("no initialize result")
	}
	for _, want := range []string{
		"forward_message", "get_attachment",
		"control@example.com", "p@gmail.com",
		"the company mailbox; use it by default", "the personal mailbox; use it when they say personal",
	} {
		if !strings.Contains(init.Instructions, want) {
			t.Fatalf("server instructions lack %q: %q", want, init.Instructions)
		}
	}

	wantAccount := tools.AccountDescription(cfg)
	if !strings.Contains(wantAccount, "the personal mailbox; use it when they say personal") {
		t.Fatalf("rendered account description = %q", wantAccount)
	}

	list, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, tool := range list.Tools {
		got = append(got, tool.Name)
		if tool.Name == "search_threads" {
			schema, _ := json.Marshal(tool.InputSchema)
			if !strings.Contains(string(schema), wantAccount) {
				t.Fatalf("search_threads schema lacks the account description: %s", schema)
			}
			if !strings.Contains(string(schema), `"required":["query"]`) {
				t.Fatalf("search_threads must require only query: %s", schema)
			}
		}
		if tool.Name == "forward_message" {
			schema, _ := json.Marshal(tool.InputSchema)
			if !strings.Contains(string(schema), wantAccount) {
				t.Fatalf("forward_message schema lacks the account description: %s", schema)
			}
			if !strings.Contains(string(schema), `"required":["message_id","to"]`) {
				t.Fatalf("forward_message must require exactly message_id and to: %s", schema)
			}
			if !strings.Contains(string(schema), "Set only when the user asks for eml or to forward as attachment") {
				t.Fatalf("forward_message as_eml description missing: %s", schema)
			}
			if !strings.Contains(string(schema), "draft_only") {
				t.Fatalf("forward_message schema lacks draft_only: %s", schema)
			}
		}
		if tool.Name == "get_attachment" {
			schema, _ := json.Marshal(tool.InputSchema)
			if !strings.Contains(string(schema), wantAccount) {
				t.Fatalf("get_attachment schema lacks the account description: %s", schema)
			}
			if !strings.Contains(string(schema), `"required":["message_id"]`) {
				t.Fatalf("get_attachment must require only message_id: %s", schema)
			}
		}
		if tool.Name == "forward_messages" {
			schema, _ := json.Marshal(tool.InputSchema)
			if !strings.Contains(string(schema), wantAccount) {
				t.Fatalf("forward_messages schema lacks the account description: %s", schema)
			}
			if !strings.Contains(string(schema), `"required":["messages","to"]`) {
				t.Fatalf("forward_messages must require exactly messages and to: %s", schema)
			}
		}
		if tool.Name == "forward_message" || tool.Name == "forward_messages" || tool.Name == "send_draft" {
			if v, _ := tool.Meta["anthropic/requiresUserInteraction"].(bool); !v {
				t.Fatalf("%s must carry Meta[anthropic/requiresUserInteraction] = true, got %v", tool.Name, tool.Meta)
			}
		}
	}
	want := append([]string{}, tools.ToolNames...)
	sort.Strings(got)
	sort.Strings(want)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("tools = %v, want %v", got, want)
	}

	res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "accounts_list", Arguments: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("accounts_list error: %+v", res.Content)
	}
	text, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("content[0] = %T, want TextContent", res.Content[0])
	}
	var out struct {
		Default  string `json:"default"`
		Accounts []struct {
			Alias string `json:"alias"`
			Email string `json:"email"`
		} `json:"accounts"`
	}
	if err := json.Unmarshal([]byte(text.Text), &out); err != nil {
		t.Fatalf("parse %q: %v", text.Text, err)
	}
	if out.Default != "work" || len(out.Accounts) != 2 || out.Accounts[1].Email != "p@gmail.com" {
		t.Fatalf("accounts_list = %+v", out)
	}

	res, err = session.CallTool(ctx, &mcp.CallToolParams{Name: "search_threads", Arguments: map[string]any{"account": "nope", "query": "x"}})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("unknown account must be a tool error")
	}
	errText := res.Content[0].(*mcp.TextContent).Text
	if !strings.Contains(errText, "personal") || !strings.Contains(errText, "work") {
		t.Fatalf("error text = %q, want the known aliases", errText)
	}

	// A known account with an empty file store: the tool call reaches the
	// store and reports the missing OAuth client instead of crashing.
	res, err = session.CallTool(ctx, &mcp.CallToolParams{Name: "search_threads", Arguments: map[string]any{"query": "x"}})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || !strings.Contains(res.Content[0].(*mcp.TextContent).Text, "secret not found") {
		t.Fatalf("search_threads with an empty secret store: %+v", res.Content)
	}

	// The SDK validates arguments against the schema before the handler
	// runs, so an omitted message_id is a schema error. A blank one passes
	// the schema and reaches the handler's own check.
	res, err = session.CallTool(ctx, &mcp.CallToolParams{Name: "forward_message", Arguments: map[string]any{"message_id": " ", "to": []string{"d@example.com"}}})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || !strings.Contains(res.Content[0].(*mcp.TextContent).Text, "message_id is required") {
		t.Fatalf("forward_message with blank message_id: %+v", res.Content)
	}

	// A message id that reads as a path is refused before any account or
	// file is touched.
	res, err = session.CallTool(ctx, &mcp.CallToolParams{Name: "get_attachment", Arguments: map[string]any{"message_id": "../x"}})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || !strings.Contains(res.Content[0].(*mcp.TextContent).Text, "message_id may hold only") {
		t.Fatalf("get_attachment with a path as message_id: %+v", res.Content)
	}

	// An empty messages list passes the schema (the field is present) and
	// reaches the handler's own check.
	res, err = session.CallTool(ctx, &mcp.CallToolParams{Name: "forward_messages", Arguments: map[string]any{"messages": []any{}, "to": []string{"d@example.com"}}})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || !strings.Contains(res.Content[0].(*mcp.TextContent).Text, "messages is required") {
		t.Fatalf("forward_messages with an empty list: %+v", res.Content)
	}

	// Close before reading stderr. os/exec copies the child's stderr into
	// the buffer on a background goroutine that Cmd.Wait joins; reading it
	// beforehand races with that goroutine under -race. Close is idempotent
	// (mcp.ClientSession docs), so the deferred call above is a no-op after this.
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr.String(), "minuar-multi-gmail serve") {
		t.Fatalf("startup log line missing on stderr:\n%s", stderr.String())
	}
}

// A store that cannot be opened must not stop the server: it starts, lists
// every tool, and reports the problem when a tool needs the store.
func TestServeWithAnUnopenableStore(t *testing.T) {
	bin := buildBinary(t)
	cfgDir := t.TempDir()
	cfg := &config.File{Version: 1}
	if err := cfg.Add(config.Account{Alias: "work", Email: "control@example.com", AddedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := config.Save(cfgDir, cfg); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.Command(bin, "serve")
	cmd.Env = append(os.Environ(), config.EnvDir+"="+cfgDir, envAttachmentDir+"="+t.TempDir(), secrets.EnvKind+"=bogus")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	client := mcp.NewClient(&mcp.Implementation{Name: "minuar-multi-gmail-test", Version: "0"}, nil)
	session, err := client.Connect(ctx, &mcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		t.Fatalf("connect: %v\nstderr:\n%s", err, stderr.String())
	}
	defer session.Close()

	list, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, tool := range list.Tools {
		got = append(got, tool.Name)
	}
	want := append([]string{}, tools.ToolNames...)
	sort.Strings(got)
	sort.Strings(want)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("tools = %v, want %v", got, want)
	}

	res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "search_threads", Arguments: map[string]any{"query": "x"}})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || !strings.Contains(res.Content[0].(*mcp.TextContent).Text, "unknown secret store") {
		t.Fatalf("search_threads with an unknown store: %+v", res.Content)
	}

	// Close before reading stderr, as TestServeOverStdio does.
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
}
