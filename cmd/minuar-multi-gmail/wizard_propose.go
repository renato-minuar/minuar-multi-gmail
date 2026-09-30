package main

import (
	"fmt"
	"strings"

	"github.com/renato-minuar/minuar-multi-gmail/internal/config"
)

// personalDomains are the consumer Gmail domains: their alias is
// "personal", every other domain is a work or organisation mailbox.
var personalDomains = map[string]bool{"gmail.com": true, "googlemail.com": true}

// emailDomain returns the lower-case domain of an address, or "".
func emailDomain(email string) string {
	at := strings.LastIndex(email, "@")
	if at < 0 || at == len(email)-1 {
		return ""
	}
	return strings.ToLower(email[at+1:])
}

// publicSuffixes are the multi-label endings this tool recognises; the
// label before them names the organisation. Anything else uses the label
// before the last dot.
var publicSuffixes = map[string]bool{"co.uk": true, "com.au": true, "com.br": true, "co.nz": true, "org.uk": true, "ac.uk": true}

// domainLabel returns the organisation label of a domain: "minuar" for
// minuar.com, "example" for mail.example.co.uk.
func domainLabel(domain string) string {
	parts := strings.Split(domain, ".")
	if len(parts) < 2 {
		return domain
	}
	if len(parts) >= 3 && publicSuffixes[strings.Join(parts[len(parts)-2:], ".")] {
		return parts[len(parts)-3]
	}
	return parts[len(parts)-2]
}

// proposeAlias suggests an alias for an address: "personal" for consumer
// Gmail, "work" for the first other domain, the domain label after that.
// A taken proposal gets 2, 3, ... appended.
func proposeAlias(email string, taken func(alias string) bool) string {
	domain := emailDomain(email)
	var base string
	switch {
	case domain == "":
		base = "account"
	case personalDomains[domain]:
		base = "personal"
	case !taken("work"):
		base = "work"
	default:
		base = domainLabel(domain)
	}
	if !config.ValidAlias(base) {
		// A label that starts with a digit or holds odd characters gets a
		// letter in front and the rest dropped.
		base = "a" + strings.Map(func(r rune) rune {
			if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' {
				return r
			}
			return -1
		}, base)
		if !config.ValidAlias(base) {
			base = "account"
		}
	}
	candidate := base
	for n := 2; taken(candidate); n++ {
		candidate = fmt.Sprintf("%s%d", base, n)
	}
	return candidate
}

// proposeDescription suggests the sentence the model reads to pick the
// account. The user can accept it with Enter or type their own.
func proposeDescription(alias, email string) string {
	domain := emailDomain(email)
	switch alias {
	case "personal":
		return "my personal gmail; use it when I say personal or private"
	case "work":
		return fmt.Sprintf("the mailbox at %s; use it by default and for anything about work", domain)
	}
	return fmt.Sprintf("the mailbox at %s; use it when I mention %s", domain, alias)
}
