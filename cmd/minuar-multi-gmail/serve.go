package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/renato-minuar/minuar-multi-gmail/internal/config"
	"github.com/renato-minuar/minuar-multi-gmail/internal/secrets"
	"github.com/renato-minuar/minuar-multi-gmail/internal/tools"
)

// instructionsTail is the part of the server instructions that never
// depends on the configured accounts.
const instructionsTail = "Pick the account from the user's wording and never ask which one. " +
	"Mail is sent by send_draft, forward_message and forward_messages; create_draft never sends. " +
	"forward_message sends the forward right away and asks the user for approval on every call; set draft_only when the user wants a draft to review. " +
	"It quotes the original inline with its attachments; set as_eml only when the user asks for eml or to forward as attachment. " +
	"Use forward_messages when more than one message is to be forwarded: one call, one approval. " +
	"To read an attachment, call get_attachment and open the file at the returned path."

// envAttachmentDir overrides where get_attachment writes files.
const envAttachmentDir = "MINUAR_MULTI_GMAIL_ATTACHMENT_DIR"

// attachmentDir returns the directory get_attachment writes to:
// $MINUAR_MULTI_GMAIL_ATTACHMENT_DIR when set, otherwise
// minuar-multi-gmail/attachments under the user's cache directory.
func attachmentDir() (string, error) {
	if d := os.Getenv(envAttachmentDir); d != "" {
		return d, nil
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("attachment dir: %w", err)
	}
	return filepath.Join(cache, "minuar-multi-gmail", "attachments"), nil
}

// serverInstructions renders the instructions Claude Code reads once, at
// connect time: which accounts exist, when to use each, how mail is sent.
func serverInstructions(cfg *config.File) string {
	if cfg == nil || len(cfg.Accounts) == 0 {
		return "No Gmail accounts are configured. Run minuar-multi-gmail add-account <alias> first. " + instructionsTail
	}
	var b strings.Builder
	plural := "s"
	if len(cfg.Accounts) == 1 {
		plural = ""
	}
	fmt.Fprintf(&b, "Gmail for %d account%s. ", len(cfg.Accounts), plural)
	if def, ok := cfg.Find(cfg.Default); ok {
		fmt.Fprintf(&b, "The default account is '%s' (%s). ", def.Alias, def.Email)
	}
	b.WriteString(tools.AccountSentences(cfg))
	b.WriteString(instructionsTail)
	return b.String()
}

// serveDeps installs logger as the process-wide slog default, so every
// slog caller in the process (tools, the go-sdk, any library) writes
// through the same stderr handler, then returns the Deps the tools use.
// Without an attachment directory the server still starts: get_attachment
// then reports the missing directory on its own calls.
func serveDeps(p *provider, logger *slog.Logger) tools.Deps {
	slog.SetDefault(logger)
	dir, err := attachmentDir()
	if err != nil {
		logger.Warn("get_attachment cannot save files", "err", err)
	}
	return tools.Deps{Config: p.loadConfig, Service: p.service, Logger: logger, Now: time.Now, AttachmentDir: dir}
}

// buildServer wires the MCP server and its tools for one provider. The
// SDK's own logger stays a separate, quieter (warn-level) handler so its
// internal chatter doesn't drown out minuar-multi-gmail's own operational logs.
func buildServer(p *provider, logger *slog.Logger, errOut io.Writer) *mcp.Server {
	cfg, err := p.loadConfig()
	if err != nil {
		logger.Warn("accounts unreadable; serving the no-accounts instructions", "err", err)
		cfg = &config.File{Version: 1}
	}
	server := mcp.NewServer(&mcp.Implementation{Name: "minuar-multi-gmail", Version: version}, &mcp.ServerOptions{
		Instructions: serverInstructions(cfg),
		Logger:       slog.New(slog.NewTextHandler(errOut, &slog.HandlerOptions{Level: slog.LevelWarn})),
	})
	tools.Register(server, serveDeps(p, logger))
	return server
}

func init() {
	commandHelp["serve"] = "run the MCP server over stdio (used by Claude Code)"
	commands["serve"] = func(ctx context.Context, _ []string, io stdio) int {
		logger := slog.New(slog.NewTextHandler(io.err, &slog.HandlerOptions{Level: slog.LevelInfo}))
		dir, err := config.Dir()
		if err != nil {
			logger.Error("config dir", "err", err)
			return 1
		}
		p := newProvider(secrets.NewKeychain(secrets.ServiceName()), dir)
		server := buildServer(p, logger, io.err)
		ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
		defer stop()
		logger.Info("minuar-multi-gmail serve", "version", version, "config", dir)
		if err := server.Run(ctx, &mcp.StdioTransport{}); err != nil {
			logger.Error("server stopped", "err", err)
			fmt.Fprintln(io.err, "error:", err)
			return 1
		}
		return 0
	}
}
