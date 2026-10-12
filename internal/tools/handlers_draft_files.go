package tools

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/renato-minuar/minuar-multi-gmail/internal/mime"
)

// maxDraftBytes caps the whole encoded message of a draft that carries
// local files. Gmail refuses to send a message above 25 MB, and base64
// makes a file about a third larger than it is on disk. A var so tests can
// lower it.
var maxDraftBytes int64 = 25 * 1000 * 1000

// attachmentContentTypes gives every type on the allowed list its
// Content-Type. A fixed table, so the result does not depend on the mime
// database of the machine.
var attachmentContentTypes = map[string]string{
	"pdf":  "application/pdf",
	"png":  "image/png",
	"jpg":  "image/jpeg",
	"jpeg": "image/jpeg",
	"gif":  "image/gif",
	"webp": "image/webp",
	"txt":  "text/plain",
	"md":   "text/markdown",
	"csv":  "text/csv",
	"json": "application/json",
	"xml":  "application/xml",
	"html": "text/html",
	"ics":  "text/calendar",
	"eml":  "message/rfc822",
	"docx": "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
	"xlsx": "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
	"pptx": "application/vnd.openxmlformats-officedocument.presentationml.presentation",
}

// localFile is one path of an attachments input that passed checkLocalFiles.
type localFile struct {
	path  string
	name  string
	ctype string
	info  os.FileInfo
}

// checkLocalFiles validates the paths of an attachments input without
// reading any file: absolute, an existing regular file that is not a
// symbolic link, not empty, and of a type on the allowed list. Every error
// names the path.
func checkLocalFiles(paths []string) ([]localFile, error) {
	files := make([]localFile, 0, len(paths))
	for i, p := range paths {
		if strings.TrimSpace(p) == "" {
			return nil, fmt.Errorf("attachments[%d] is empty", i)
		}
		if !filepath.IsAbs(p) {
			return nil, fmt.Errorf("attachment %s: the path must be absolute", p)
		}
		p = filepath.Clean(p)
		// Lstat, not Stat: a link named invoice.pdf could point at any
		// file, and the type check reads only the name.
		info, err := os.Lstat(p)
		if err != nil {
			return nil, fmt.Errorf("attachment %s: %w", p, pathErr(err))
		}
		switch {
		case info.Mode()&fs.ModeSymlink != 0:
			return nil, fmt.Errorf("attachment %s: is a symbolic link; give the path of the file itself", p)
		case info.IsDir():
			return nil, fmt.Errorf("attachment %s: is a directory", p)
		case !info.Mode().IsRegular():
			return nil, fmt.Errorf("attachment %s: is not a regular file", p)
		}
		name := filepath.Base(p)
		ext, ok := attachmentType(name)
		if !ok {
			if ext == "" {
				return nil, fmt.Errorf("attachment %s: the name has no file type. Allowed types: %s", p, allowedTypesText())
			}
			return nil, fmt.Errorf("attachment %s: type %s is not on the allowed list. Allowed types: %s", p, ext, allowedTypesText())
		}
		if info.Size() == 0 {
			return nil, fmt.Errorf("attachment %s: the file is empty", p)
		}
		files = append(files, localFile{path: p, name: name, ctype: attachmentContentTypes[ext], info: info})
	}
	return files, nil
}

// pathErr drops the operation and path an *fs.PathError repeats, so the
// caller's message names the path once.
func pathErr(err error) error {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return pe.Err
	}
	return err
}

// encodedSize is the size of n bytes after base64 in 76-column CRLF lines.
func encodedSize(n int64) int64 {
	enc := (n + 2) / 3 * 4
	return enc + (enc/76+1)*2
}

// checkDraftSize refuses the files when the message would pass
// maxDraftBytes. base is the size of the message without them. It runs on
// the sizes from checkLocalFiles, before any file is read, and names the
// file that crosses the limit.
func checkDraftSize(files []localFile, base int64) error {
	total := base
	for _, f := range files {
		total += encodedSize(f.info.Size())
		if total > maxDraftBytes {
			return fmt.Errorf("attachment %s: the message would be about %d bytes with this file, above Gmail's 25 MB limit", f.path, total)
		}
	}
	return nil
}

// readLocalFiles reads the checked files. A file that was replaced or
// changed size since the check is refused.
func readLocalFiles(files []localFile) ([]mime.Attachment, []AttachmentOut, error) {
	atts := make([]mime.Attachment, 0, len(files))
	out := make([]AttachmentOut, 0, len(files))
	for _, f := range files {
		data, err := readLocalFile(f)
		if err != nil {
			return nil, nil, err
		}
		atts = append(atts, mime.Attachment{Filename: f.name, ContentType: f.ctype, Data: data})
		out = append(out, AttachmentOut{Filename: f.name, MimeType: f.ctype, SizeBytes: int64(len(data))})
	}
	return atts, out, nil
}

func readLocalFile(f localFile) ([]byte, error) {
	file, err := os.Open(f.path)
	if err != nil {
		return nil, fmt.Errorf("attachment %s: %w", f.path, pathErr(err))
	}
	defer file.Close()
	// Second guard: the open file must be the one that was checked.
	now, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("attachment %s: %w", f.path, pathErr(err))
	}
	if !os.SameFile(f.info, now) {
		return nil, fmt.Errorf("attachment %s: the file was replaced while it was read", f.path)
	}
	data, err := io.ReadAll(io.LimitReader(file, f.info.Size()+1))
	if err != nil {
		return nil, fmt.Errorf("attachment %s: %w", f.path, pathErr(err))
	}
	if int64(len(data)) != f.info.Size() {
		return nil, fmt.Errorf("attachment %s: the file changed size while it was read", f.path)
	}
	return data, nil
}
