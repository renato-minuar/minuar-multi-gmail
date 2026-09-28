package tools

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/renato-minuar/minuar-multi-gmail/internal/gmail"
)

// allowedAttachmentTypes is the fixed list of file extensions
// get_attachment saves. Every other type is refused before any download.
// The list is code, not config, so nothing can widen it by accident.
var allowedAttachmentTypes = []string{
	"pdf", "png", "jpg", "jpeg", "gif", "webp", "txt", "md", "csv", "json", "xml", "html", "ics", "eml",
	"docx", "xlsx", "pptx",
}

// maxAttachmentNameRunes caps the saved name without its extension, well
// under the 255 byte limit of a file name on macOS.
const maxAttachmentNameRunes = 150

// messageIDRe is the shape of a Gmail message id. The id becomes a
// directory name, so nothing that reads as a path may pass.
var messageIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

func allowedTypesText() string { return strings.Join(allowedAttachmentTypes, ", ") }

// attachmentType returns the lower-case extension of a saved name and
// whether it is on the allowed list.
func attachmentType(name string) (string, bool) {
	ext := strings.ToLower(strings.TrimPrefix(path.Ext(name), "."))
	for _, t := range allowedAttachmentTypes {
		if ext == t {
			return ext, true
		}
	}
	return ext, false
}

// safeAttachmentName turns the sender's filename into a plain file name:
// no directory part, no control characters, no leading dot, bounded length.
func safeAttachmentName(name string) string {
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		if r == '\\' {
			return '/'
		}
		return r
	}, name)
	name = path.Base(name)
	name = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(name), "."))
	if name == "" || name == "/" {
		return "attachment"
	}
	ext := path.Ext(name)
	stem := []rune(strings.TrimSuffix(name, ext))
	if len(stem) > maxAttachmentNameRunes {
		name = string(stem[:maxAttachmentNameRunes]) + ext
	}
	return name
}

// savedNames gives every attachment of a message its file name, in message
// order. Names that collide, compared without case because the default
// macOS file system ignores it, get a number before the extension. The
// result depends only on the message, so a repeated call writes the same
// paths.
func savedNames(atts []gmail.Attachment) []string {
	used := map[string]bool{}
	out := make([]string, len(atts))
	for i, a := range atts {
		name := safeAttachmentName(a.Filename)
		ext := path.Ext(name)
		stem := strings.TrimSuffix(name, ext)
		candidate := name
		for n := 2; used[strings.ToLower(candidate)]; n++ {
			candidate = fmt.Sprintf("%s-%d%s", stem, n, ext)
		}
		used[strings.ToLower(candidate)] = true
		out[i] = candidate
	}
	return out
}

// writeAttachment writes data to dir/name atomically with mode 0600: temp
// file in the same directory, then rename.
func writeAttachment(dir, name string, data []byte) (string, error) {
	final := filepath.Join(dir, name)
	if filepath.Dir(final) != filepath.Clean(dir) {
		return "", fmt.Errorf("attachment name %q leaves its directory", name)
	}
	tmp, err := os.CreateTemp(dir, ".attachment-*.tmp")
	if err != nil {
		return "", fmt.Errorf("temp file in %s: %w", dir, err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return "", fmt.Errorf("write %s: %w", final, err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return "", fmt.Errorf("chmod %s: %w", final, err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("write %s: %w", final, err)
	}
	if err := os.Rename(tmp.Name(), final); err != nil {
		return "", fmt.Errorf("save %s: %w", final, err)
	}
	return final, nil
}

func (h *handlers) getAttachment(ctx context.Context, _ *mcp.CallToolRequest, in GetAttachmentInput) (*mcp.CallToolResult, GetAttachmentOutput, error) {
	var zero GetAttachmentOutput
	if err := requireID(in.MessageID, "message_id"); err != nil {
		return nil, zero, err
	}
	messageID := strings.TrimSpace(in.MessageID)
	if !messageIDRe.MatchString(messageID) {
		return nil, zero, errors.New("message_id may hold only letters, digits, - and _")
	}
	if h.d.AttachmentDir == "" {
		return nil, zero, errors.New("the attachment directory is not configured")
	}
	acct, svc, err := h.resolve(ctx, in.Account)
	if err != nil {
		return nil, zero, err
	}
	msg, err := svc.GetMessage(ctx, messageID, 1)
	if err != nil {
		return nil, zero, err
	}
	if len(msg.Attachments) == 0 {
		return nil, zero, fmt.Errorf("message %s has no attachments", messageID)
	}

	names := savedNames(msg.Attachments)
	wanted := strings.TrimSpace(in.Filename)
	var picked []int
	for i, a := range msg.Attachments {
		if wanted == "" || strings.EqualFold(strings.TrimSpace(a.Filename), wanted) {
			picked = append(picked, i)
		}
	}
	if len(picked) == 0 {
		have := make([]string, 0, len(msg.Attachments))
		for _, a := range msg.Attachments {
			have = append(have, fmt.Sprintf("%q", a.Filename))
		}
		return nil, zero, fmt.Errorf("message %s has no attachment named %q; it has %s", messageID, wanted, strings.Join(have, ", "))
	}

	out := GetAttachmentOutput{Account: acct.Alias, Email: acct.Email, MessageID: messageID,
		Files: []SavedAttachment{}, Skipped: []SkippedAttachment{}}
	var allowed []int
	var announced int64
	for _, i := range picked {
		a := msg.Attachments[i]
		ext, ok := attachmentType(names[i])
		if !ok {
			reason := "type " + ext + " is not on the allowed list"
			if ext == "" {
				reason = "the name has no file type"
			}
			out.Skipped = append(out.Skipped, SkippedAttachment{Filename: a.Filename, Reason: reason})
			continue
		}
		allowed = append(allowed, i)
		announced += a.SizeBytes
	}
	if len(allowed) == 0 {
		parts := make([]string, 0, len(out.Skipped))
		for _, s := range out.Skipped {
			parts = append(parts, fmt.Sprintf("%q (%s)", s.Filename, s.Reason))
		}
		return nil, zero, fmt.Errorf("nothing to save: %s. Allowed types: %s", strings.Join(parts, ", "), allowedTypesText())
	}
	// The cap is checked before any download, on the sizes Gmail announced.
	if announced > maxForwardBytes {
		return nil, zero, fmt.Errorf("attachments total %d bytes, above the 100 MB limit", announced)
	}

	dir := filepath.Join(h.d.AttachmentDir, acct.Alias, messageID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, zero, fmt.Errorf("create %s: %w", dir, err)
	}
	// MkdirAll leaves an existing directory's mode alone.
	for _, d := range []string{h.d.AttachmentDir, filepath.Dir(dir), dir} {
		if err := os.Chmod(d, 0o700); err != nil {
			return nil, zero, fmt.Errorf("chmod %s: %w", d, err)
		}
	}

	var total int64
	for _, i := range allowed {
		a := msg.Attachments[i]
		var data []byte
		switch {
		case a.AttachmentID != "":
			data, err = svc.GetAttachment(ctx, messageID, a.AttachmentID)
			if err != nil {
				return nil, zero, err
			}
		case a.Data != nil:
			// Gmail delivered the bytes inline; nothing to download.
			data = a.Data
		default:
			return nil, zero, fmt.Errorf("attachment %q has no attachment id", a.Filename)
		}
		// Second guard: the exact length, in case an announced size was low.
		total += int64(len(data))
		if total > maxForwardBytes {
			return nil, zero, fmt.Errorf("attachments total more than %d bytes, above the 100 MB limit", maxForwardBytes)
		}
		saved, err := writeAttachment(dir, names[i], data)
		if err != nil {
			return nil, zero, err
		}
		out.Files = append(out.Files, SavedAttachment{Filename: a.Filename, Path: saved, MimeType: a.MimeType, SizeBytes: int64(len(data))})
	}
	h.log().Info("get_attachment", "account", acct.Alias, "message_id", messageID, "files", len(out.Files), "skipped", len(out.Skipped), "bytes", total)
	return nil, out, nil
}
