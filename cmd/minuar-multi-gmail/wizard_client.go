package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/renato-minuar/minuar-multi-gmail/internal/config"
	"github.com/renato-minuar/minuar-multi-gmail/internal/googleauth"
	"github.com/renato-minuar/minuar-multi-gmail/internal/secrets"
)

type consolePage struct{ URL, Instruction string }

// consolePages are the four Google Cloud console pages a user visits to
// get an OAuth client, in order. Google has no API for any of them.
var consolePages = [4]consolePage{
	{"https://console.cloud.google.com/projectcreate", "Create a project. Name: minuar-multi-gmail. Press Enter here when it exists."},
	{"https://console.cloud.google.com/apis/library/gmail.googleapis.com", "Click Enable. Press Enter when done."},
	{"https://console.cloud.google.com/auth/overview", "Configure the consent screen: app name minuar-multi-gmail, your email, audience External, then publish it (Audience page, \"Publish app\"). Press Enter when done."},
	{"https://console.cloud.google.com/auth/clients/create", "Application type Desktop app, name minuar-multi-gmail, Create, then Download JSON."},
}

const clientFilePattern = "client_secret*.json"

// newestClientFile returns the newest client_secret*.json in dir with a
// modification time after since, or "". Empty files are skipped: the
// browser may still be writing them.
func newestClientFile(dir string, since time.Time) string {
	matches, _ := filepath.Glob(filepath.Join(dir, clientFilePattern))
	var best string
	var bestTime time.Time
	for _, m := range matches {
		st, err := os.Stat(m)
		if err != nil || !st.Mode().IsRegular() || st.Size() == 0 || !st.ModTime().After(since) {
			continue
		}
		if best == "" || st.ModTime().After(bestTime) {
			best, bestTime = m, st.ModTime()
		}
	}
	return best
}

// watchDownloads waits for the client file: a new client_secret*.json in
// the Downloads directory, or the path of an existing file pasted on stdin.
// An empty line re-prints the hint. A line that is not an existing file is
// reported and discarded. A closed stdin ends the pasted-path route only;
// the watcher goes on until the file appears or the wait runs out.
func watchDownloads(d *wizardDeps, since time.Time) (path string, fromDownloads bool, err error) {
	deadline := d.now().Add(d.waitFile)
	input := d.lines()
	for {
		if p := newestClientFile(d.downloads, since); p != "" {
			return p, true, nil
		}
		select {
		case r, ok := <-input:
			line := strings.TrimSpace(r.line)
			switch {
			case !ok:
				input = nil // a nil channel never fires again
			case r.err != nil:
				return "", false, r.err
			case line == "":
				fmt.Fprintf(d.out, "still waiting for the file in %s; paste its path here if it is elsewhere\n", d.downloads)
			case isRegularFile(normalizePath(line)):
				return normalizePath(line), false, nil
			default:
				fmt.Fprintf(d.out, "not a file: %s; still waiting for the file in %s\n", line, d.downloads)
			}
		default:
		}
		if !d.now().Before(deadline) {
			return "", false, fmt.Errorf("no client file appeared in %s within %s; download it and run the wizard again", d.downloads, d.waitFile)
		}
		d.sleep(time.Second)
	}
}

// normalizePath cleans a pasted path the way a shell would: surrounding
// spaces and one pair of quotes go, "\\ " becomes a space, a leading "~/"
// becomes the home directory.
func normalizePath(line string) string {
	p := strings.TrimSpace(line)
	if len(p) >= 2 && (p[0] == '\'' || p[0] == '"') && p[len(p)-1] == p[0] {
		p = p[1 : len(p)-1]
	}
	p = strings.ReplaceAll(p, `\ `, " ")
	if strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			p = filepath.Join(home, p[2:])
		}
	}
	return p
}

func isRegularFile(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.Mode().IsRegular()
}

// noDisplay reports a Linux machine with neither an X nor a Wayland
// display, typically an SSH session.
func (d *wizardDeps) noDisplay() bool {
	if d.goos != "linux" {
		return false
	}
	if d.getenv == nil {
		return true
	}
	return d.getenv("DISPLAY") == "" && d.getenv("WAYLAND_DISPLAY") == ""
}

// wizardClient is step w3: the guided console, the Downloads watcher, and
// the store through storeClient.
func wizardClient(ctx context.Context, d *wizardDeps, kind secrets.Kind, store secrets.Store) error {
	heading(d, "OAuth client")
	if _, err := googleauth.LoadClientCreds(store); err == nil {
		fmt.Fprintln(d.out, "OAuth client stored.")
		return recordKind(d, kind)
	}
	path, deleteDefault, err := findClientFile(d)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	creds, err := googleauth.ParseClientSecretFile(data)
	if err != nil {
		return fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	cfg, err := config.Load(d.configDir)
	if err != nil {
		return err
	}
	if err := storeClient(d.configDir, kind, store, cfg, creds, d.out); err != nil {
		return err
	}
	del, err := askYesNo(d, fmt.Sprintf("Delete %s?", path), deleteDefault)
	if err != nil {
		return err
	}
	if del {
		if err := os.Remove(path); err != nil {
			fmt.Fprintf(d.out, "could not delete %s: %v\n", path, err)
		} else {
			fmt.Fprintf(d.out, "Deleted %s.\n", path)
		}
	}
	return nil
}

// recordKind writes the secret store kind into the config when an earlier
// run stored the client but never recorded it.
func recordKind(d *wizardDeps, kind secrets.Kind) error {
	cfg, err := config.Load(d.configDir)
	if err != nil {
		return err
	}
	if cfg.Secrets != "" {
		return nil
	}
	cfg.Secrets = string(kind)
	if err := config.Save(d.configDir, cfg); err != nil {
		return err
	}
	fmt.Fprintln(d.out, "Recorded the secret store.")
	return nil
}

// findClientFile returns the client file to store and the default answer
// of the delete question: yes for a file the watcher found in Downloads,
// no for a file the user named.
func findClientFile(d *wizardDeps) (path string, deleteDefault bool, err error) {
	// An earlier, interrupted run may have left the file behind.
	if p := newestClientFile(d.downloads, time.Time{}); p != "" {
		use, err := askYesNo(d, fmt.Sprintf("Use %s?", p), true)
		if err != nil {
			return "", false, err
		}
		if use {
			return p, false, nil
		}
	}
	fmt.Fprintln(d.out, "Google needs an OAuth client for this tool. The browser opens four console pages, one at a time.")
	started := d.now()
	for i, page := range consolePages {
		fmt.Fprintf(d.out, "\n%d/4  %s\n     %s\n", i+1, page.Instruction, page.URL)
		if err := d.openURL(page.URL); err != nil {
			fmt.Fprintf(d.out, "     (could not open a browser: %v; open the address yourself)\n", err)
		}
		if i == 3 {
			break
		}
		for {
			line, err := d.readLine()
			if err != nil {
				return "", false, err
			}
			line = strings.TrimSpace(line)
			if line == "" {
				break
			}
			if p := normalizePath(line); isRegularFile(p) {
				return p, false, nil
			}
			fmt.Fprintln(d.out, "press Enter when the page is done, or paste the path of the downloaded file")
		}
	}
	fmt.Fprintf(d.out, "Waiting for the downloaded file in %s (paste its path here if it lands elsewhere).\n", d.downloads)
	if d.noDisplay() {
		fmt.Fprintln(d.out, "This machine has no display: download the file on your own computer, copy it here (scp), and paste its path.")
	}
	path, fromDownloads, err := watchDownloads(d, started)
	if err != nil {
		return "", false, err
	}
	return path, fromDownloads, nil
}
