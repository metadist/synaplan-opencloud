package handler

import (
	"bytes"
	"encoding/binary"
	"errors"
	"mime"
	"net/http"
	"path"
	"strings"
	"unicode"
)

var (
	errCopyNameRequired = errors.New("copy name required")
	errCopyName         = errors.New("copy name")
	errCopyKind         = errors.New("copy kind")
	errCopyEmpty        = errors.New("copy empty")
)

// screenCopyName rejects a name before any download. The rules are the
// same as safeCopyName: a trailing dot or a name that is too long is
// not repaired from the download's Content-Disposition.
func screenCopyName(raw string) error {
	name := strings.TrimSpace(raw)
	if name == "" || name == "." || name == ".." {
		return errCopyNameRequired
	}
	cleaned, ok := safeCopyName(raw)
	if !ok {
		return errCopyName
	}
	if blockedCopyExt[strings.ToLower(path.Ext(cleaned))] {
		return errCopyKind
	}
	return nil
}

// acceptedCopy is the name to write and a check that the bytes match a
// kind this copy will put in the folder. The name is the one the client
// sent. A download header does not replace it.
func acceptedCopy(clientName string, header http.Header, content []byte) (string, error) {
	if len(content) == 0 {
		return "", errCopyEmpty
	}
	name, ok := safeCopyName(clientName)
	if !ok {
		return "", errCopyName
	}
	if err := copyKind(name, header.Get("Content-Type"), content); err != nil {
		return "", err
	}
	return name, nil
}

func safeCopyName(raw string) (string, bool) {
	name := strings.TrimSpace(raw)
	if name == "" || strings.ContainsAny(name, `/\`) || strings.ContainsRune(name, 0) {
		return "", false
	}
	if name != path.Base(name) || name == "." || name == ".." {
		return "", false
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return "", false
		}
	}
	if len(name) > 240 || strings.HasSuffix(name, " ") || strings.HasSuffix(name, ".") {
		return "", false
	}
	return name, true
}

func copyKind(name, headerType string, content []byte) error {
	ext := strings.ToLower(path.Ext(name))
	if blockedCopyExt[ext] || executableBytes(content) {
		return errCopyKind
	}
	declared := mediaType(headerType)
	sniffed := mediaType(http.DetectContentType(content))
	// The declared type and the bytes are checked on their own. An
	// allowed extension does not excuse HTML, SVG, or XHTML, and a
	// harmless Content-Type does not excuse HTML in the body.
	if dangerousMedia(declared) || dangerousMedia(sniffed) {
		return errCopyKind
	}
	media := declared
	if media == "" || media == "application/octet-stream" {
		media = sniffed
	}
	if copyableExt[ext] || safeMedia(media) {
		return nil
	}
	return errCopyKind
}

// executableBytes recognises formats http.DetectContentType reports as
// application/octet-stream, so a renamed binary is not saved as notes.txt.
func executableBytes(content []byte) bool {
	if len(content) >= 4 && bytes.Equal(content[:4], []byte{0x7f, 'E', 'L', 'F'}) {
		return true
	}
	if len(content) >= 2 && content[0] == 'M' && content[1] == 'Z' {
		return true
	}
	if len(content) >= 4 {
		switch binary.BigEndian.Uint32(content[:4]) {
		case 0xFEEDFACE, 0xFEEDFACF, 0xCEFAEDFE, 0xCFFAEDFE:
			return true
		}
	}
	return false
}

func mediaType(value string) string {
	media, _, err := mime.ParseMediaType(value)
	if err != nil {
		return ""
	}
	return strings.ToLower(media)
}

func dangerousMedia(media string) bool {
	switch media {
	case "text/html", "application/xhtml+xml", "image/svg+xml", "application/javascript", "text/javascript", "application/x-msdownload", "application/x-executable", "application/x-dosexec", "application/x-sh":
		return true
	default:
		return false
	}
}

func safeMedia(media string) bool {
	switch {
	case media == "text/plain", media == "text/csv", media == "text/markdown", media == "text/calendar", media == "application/pdf", media == "application/json", media == "application/rtf", media == "application/zip":
		return true
	case strings.HasPrefix(media, "image/") && media != "image/svg+xml":
		return true
	case strings.HasPrefix(media, "audio/"), strings.HasPrefix(media, "video/"):
		return true
	case strings.HasPrefix(media, "application/vnd.openxmlformats-officedocument."), strings.HasPrefix(media, "application/vnd.oasis.opendocument."):
		return true
	case media == "application/msword", media == "application/vnd.ms-excel", media == "application/vnd.ms-powerpoint":
		return true
	default:
		return false
	}
}

func writeCopyNameFailure(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errCopyNameRequired):
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "name is required"})
	case errors.Is(err, errCopyEmpty):
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "This file is empty.", Code: "file_empty"})
	case errors.Is(err, errCopyKind):
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "Synaplan cannot read this kind of file.", Code: "unsupported_type"})
	default:
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "This name cannot be used for a file.", Code: "bad_file_name"})
	}
}

// copyableExt is a kind a person keeps in a folder. Anything else needs
// a content type safeMedia recognises.
var copyableExt = map[string]bool{
	".txt": true, ".md": true, ".csv": true, ".json": true, ".pdf": true, ".rtf": true,
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true, ".tif": true, ".tiff": true, ".bmp": true,
	".mp3": true, ".wav": true, ".ogg": true, ".m4a": true, ".mp4": true, ".webm": true, ".mov": true,
	".doc": true, ".docx": true, ".xls": true, ".xlsx": true, ".ppt": true, ".pptx": true,
	".odt": true, ".ods": true, ".odp": true, ".ics": true, ".zip": true, ".epub": true,
}

// blockedCopyExt is active content. It is refused even when the
// content type looks harmless.
var blockedCopyExt = map[string]bool{
	".exe": true, ".dll": true, ".so": true, ".dylib": true, ".bat": true, ".cmd": true, ".com": true,
	".scr": true, ".msi": true, ".js": true, ".jse": true, ".vbs": true, ".ps1": true, ".sh": true,
	".html": true, ".htm": true, ".xhtml": true, ".svg": true, ".php": true, ".jar": true, ".app": true,
}
