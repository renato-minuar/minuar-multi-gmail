package mime

import "strings"

// ForwardSubject prefixes "Fwd: " unless the subject already starts with
// "fwd:" or "fw:" in any letter case (leading spaces ignored).
func ForwardSubject(orig string) string {
	l := strings.ToLower(strings.TrimSpace(orig))
	if strings.HasPrefix(l, "fwd:") || strings.HasPrefix(l, "fw:") {
		return orig
	}
	return "Fwd: " + orig
}

// illegalFilenameRunes are the path separators plus the characters Windows
// rejects in a file name. A recipient saves the .eml under this name.
const illegalFilenameRunes = `/\:?*<>|"`

// EmlFilename derives the .eml attachment name from a subject. Control
// characters and every rune in illegalFilenameRunes become '_'. The result
// is trimmed and cut to 100 runes. An empty result gives
// "forwarded-message.eml".
func EmlFilename(subject string) string {
	var b strings.Builder
	for _, r := range subject {
		if r < 0x20 || r == 0x7f || strings.ContainsRune(illegalFilenameRunes, r) {
			b.WriteRune('_')
			continue
		}
		b.WriteRune(r)
	}
	name := strings.TrimSpace(b.String())
	if rs := []rune(name); len(rs) > 100 {
		name = strings.TrimSpace(string(rs[:100]))
	}
	if name == "" {
		return "forwarded-message.eml"
	}
	return name + ".eml"
}
