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
// modification time after since, or "".
func newestClientFile(dir string, since time.Time) string {
	matches, _ := filepath.Glob(filepath.Join(dir, clientFilePattern))
	var best string
	var bestTime time.Time
	for _, m := range matches {
		st, err := os.Stat(m)
		if err != nil || !st.Mode().IsRegular() || !st.ModTime().After(since) {
			continue
		}
		if best == "" || st.ModTime().After(bestTime) {
			best, bestTime = m, st.ModTime()
		}
	}
	return best
}

// handBack puts a line the watcher read but did not use back at the front
// of the input, so the next prompt reads it. It replaces d.lineCh with a
// channel that delivers held first and then everything the old one sends.
func handBack(d *wizardDeps, held []lineResult) {
	if len(held) == 0 {
		return
	}
	old := d.lines()
	ch := make(chan lineResult)
	d.lineCh = ch
	go func() {
		for _, r := range held {
			ch <- r
		}
		for r := range old {
			ch <- r
		}
		close(ch)
	}()
}

// watchDownloads waits for the client file: a new client_secret*.json in
// the Downloads directory, or the path of an existing file pasted on stdin.
// An empty line on stdin re-prints the hint and keeps waiting. Any other
// line is an answer typed ahead for a later prompt: the watcher stops
// reading stdin and hands the line back when it returns. A closed stdin
// ends the pasted-path route only; the watcher goes on until the file
// appears or the wait runs out.
func watchDownloads(d *wizardDeps, since time.Time) (string, error) {
	deadline := d.now().Add(d.waitFile)
	input := d.lines()
	var held []lineResult
	defer func() { handBack(d, held) }()
	for {
		if p := newestClientFile(d.downloads, since); p != "" {
			return p, nil
		}
		select {
		case r, ok := <-input:
			line := strings.TrimSpace(r.line)
			switch {
			case !ok:
				input = nil // a nil channel never fires again
			case r.err != nil:
				return "", r.err
			case line == "":
				fmt.Fprintf(d.out, "still waiting for the file in %s; paste its path here if it is elsewhere\n", d.downloads)
			case isRegularFile(line):
				return line, nil
			default:
				held = append(held, r)
				input = nil // stop reading so the held line stays in order
			}
		default:
		}
		if !d.now().Before(deadline) {
			return "", fmt.Errorf("no client file appeared in %s within %s; download it and run the wizard again", d.downloads, d.waitFile)
		}
		d.sleep(time.Second)
	}
}

func isRegularFile(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.Mode().IsRegular()
}

// wizardClient is step w3: the guided console, the Downloads watcher, and
// the store through storeClient.
func wizardClient(ctx context.Context, d *wizardDeps, kind secrets.Kind, store secrets.Store) error {
	heading(d, "OAuth client")
	if _, err := googleauth.LoadClientCreds(store); err == nil {
		fmt.Fprintln(d.out, "OAuth client stored.")
		return nil
	}
	fmt.Fprintln(d.out, "Google needs an OAuth client for this tool. The browser opens four console pages, one at a time.")
	started := d.now()
	for i, page := range consolePages {
		fmt.Fprintf(d.out, "\n%d/4  %s\n     %s\n", i+1, page.Instruction, page.URL)
		if err := d.openURL(page.URL); err != nil {
			fmt.Fprintf(d.out, "     (could not open a browser: %v; open the address yourself)\n", err)
		}
		if i < 3 {
			if _, err := d.readLine(); err != nil {
				return err
			}
		}
	}
	fmt.Fprintf(d.out, "Waiting for the downloaded file in %s (paste its path here if it lands elsewhere).\n", d.downloads)
	path, err := watchDownloads(d, started)
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
	del, err := askYesNo(d, "Delete the downloaded file?", true)
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
