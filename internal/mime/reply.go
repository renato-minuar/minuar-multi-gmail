package mime

import (
	"errors"
	"net/mail"
	"strings"
)

// ReplySubject prefixes "Re: " unless the subject already starts with
// "re:" in any letter case.
func ReplySubject(orig string) string {
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(orig)), "re:") {
		return orig
	}
	return "Re: " + orig
}

// Original holds the headers of the message being replied to, raw as
// Gmail returns them (display names allowed, comma-separated lists split).
type Original struct {
	From    string
	ReplyTo string
	To      []string
	Cc      []string
}

func bareAddress(s string) string {
	a, err := mail.ParseAddress(strings.TrimSpace(s))
	if err != nil {
		return strings.ToLower(strings.TrimSpace(s))
	}
	return strings.ToLower(a.Address)
}

// ReplyRecipients applies the spec's reply rules:
// explicit To wins; else Reply-To, else From. reply_all adds the original
// To and Cc to Cc, minus self, minus duplicates, minus anyone already in To.
func ReplyRecipients(o Original, self string, replyAll bool, explicitTo []string) (to, cc []string, err error) {
	if len(explicitTo) > 0 {
		to, err = ParseAddresses(explicitTo)
		if err != nil {
			return nil, nil, err
		}
	} else {
		primary := strings.TrimSpace(o.ReplyTo)
		if primary == "" {
			primary = strings.TrimSpace(o.From)
		}
		if primary != "" {
			to, err = ParseAddresses([]string{primary})
			if err != nil {
				return nil, nil, err
			}
		}
	}
	seen := map[string]bool{bareAddress(self): true}
	for _, t := range to {
		seen[bareAddress(t)] = true
	}
	if replyAll {
		for _, raw := range append(append([]string{}, o.To...), o.Cc...) {
			parsed, perr := ParseAddresses([]string{raw})
			if perr != nil || len(parsed) == 0 {
				continue // an unparsable original recipient is skipped, never guessed
			}
			key := bareAddress(parsed[0])
			if seen[key] {
				continue
			}
			seen[key] = true
			cc = append(cc, parsed[0])
		}
	}
	if len(to)+len(cc) == 0 {
		return nil, nil, errors.New("no recipients: the original message has no usable sender and no explicit to was given")
	}
	return to, cc, nil
}

// DedupeCc appends extra to cc, skipping any address already present in to
// or cc and skipping self, all compared case-insensitively on the bare
// address. cc, to and extra are expected already canonicalised (e.g. by
// ParseAddresses); the addresses are appended as given, never reparsed.
func DedupeCc(cc, to []string, self string, extra []string) []string {
	seen := map[string]bool{bareAddress(self): true}
	for _, a := range to {
		seen[bareAddress(a)] = true
	}
	for _, a := range cc {
		seen[bareAddress(a)] = true
	}
	out := append([]string{}, cc...)
	for _, a := range extra {
		key := bareAddress(a)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, a)
	}
	return out
}
