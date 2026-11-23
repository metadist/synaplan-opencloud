package handler

import (
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

// screenCopyName rejects a name before any download. A slash is not
// stripped: path.Base would hide which file the person asked for.
func screenCopyName(raw string) error {
	name := strings.TrimSpace(raw)
	if name == "" || name == "." || name == ".." {
		return errCopyNameRequired
	}
	if strings.ContainsAny(name, `/\`) || strings.ContainsRune(name, 0) {
		return errCopyName
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return errCopyName
		}
	}
	if blockedCopyExt[strings.ToLower(path.Ext(name))] {
		return errCopyKind
	}
	return nil
}

// acceptedCopy is the name to write and a check that the bytes match a
// kind this copy will put in the folder. The name the client sent is
// used when it is a single safe file name; otherwise the download's
// Content-Disposition is used.
func acceptedCopy(clientName string, header http.Header, content []byte) (string, error) {
	if len(content) == 0 {
		return "", errCopyEmpty
	}
	name, ok := safeCopyName(clientName)
	if !ok {
		name, ok = safeCopyName(dispositionName(header.Get("Content-Disposition")))
	}
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

func dispositionName(header string) string {
	_, params, err := mime.ParseMediaType(header)
	if err != nil {
		return ""
	}
	return params["filename"]
}

func copyKind(name, headerType string, content []byte) error {
	ext := strings.ToLower(path.Ext(name))
	if blockedCopyExt[ext] {
		return errCopyKind
	}
	media := mediaType(headerType)
	if media == "" || media == "application/octet-stream" {
		media = mediaType(http.DetectContentType(content))
	}
	if dangerousMedia(media) {
		return errCopyKind
	}
	if copyableExt[ext] || safeMedia(media) {
		return nil
	}
	return errCopyKind
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
	case "text/html", "application/javascript", "text/javascript", "application/x-msdownload", "application/x-executable", "application/x-dosexec", "application/x-sh":
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
