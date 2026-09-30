package main

import "testing"

func TestProposeAlias(t *testing.T) {
	none := func(string) bool { return false }
	cases := []struct {
		email string
		taken map[string]bool
		want  string
	}{
		{"ann@gmail.com", nil, "personal"},
		{"Ann@GoogleMail.com", nil, "personal"},
		{"ann@minuar.com", nil, "work"},
		{"ann@minuar.com", map[string]bool{"work": true}, "minuar"},
		{"ann@mail.example.co.uk", map[string]bool{"work": true}, "example"},
		{"ann@minuar.com", map[string]bool{"work": true, "minuar": true}, "minuar2"},
		{"ann@minuar.com", map[string]bool{"work": true, "minuar": true, "minuar2": true}, "minuar3"},
		{"ann@gmail.com", map[string]bool{"personal": true}, "personal2"},
		{"ann@my-shop.example", map[string]bool{"work": true}, "my-shop"},
		{"ann@123.example", map[string]bool{"work": true}, "a123"},
		{"nonsense", nil, "account"},
	}
	for _, c := range cases {
		taken := none
		if c.taken != nil {
			taken = func(a string) bool { return c.taken[a] }
		}
		if got := proposeAlias(c.email, taken); got != c.want {
			t.Errorf("proposeAlias(%q, %v) = %q, want %q", c.email, c.taken, got, c.want)
		}
	}
}

func TestProposeDescription(t *testing.T) {
	cases := []struct{ alias, email, want string }{
		{"personal", "ann@gmail.com", "my personal gmail; use it when I say personal or private"},
		{"work", "ann@minuar.com", "the mailbox at minuar.com; use it by default and for anything about work"},
		{"minuar", "ann@minuar.com", "the mailbox at minuar.com; use it when I mention minuar"},
		{"house", "ann@gmail.com", "the mailbox at gmail.com; use it when I mention house"},
	}
	for _, c := range cases {
		if got := proposeDescription(c.alias, c.email); got != c.want {
			t.Errorf("proposeDescription(%q, %q) = %q, want %q", c.alias, c.email, got, c.want)
		}
	}
}

func TestEmailDomainAndLabel(t *testing.T) {
	if emailDomain("Ann@Minuar.COM") != "minuar.com" || emailDomain("nonsense") != "" {
		t.Fatal("emailDomain")
	}
	for domain, want := range map[string]string{"minuar.com": "minuar", "mail.example.co.uk": "example", "gmail.com": "gmail", "localhost": "localhost", "": ""} {
		if got := domainLabel(domain); got != want {
			t.Errorf("domainLabel(%q) = %q, want %q", domain, got, want)
		}
	}
}
