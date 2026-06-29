package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"time"

	"github.com/pkg/sftp"
)

// editErr is a string-backed error type so the user-facing edit messages can
// be kept verbatim from Crush (which models them as text responses, not Go
// errors). Using a typed constant preserves the exact wording — including the
// trailing punctuation Crush uses — while remaining comparable via errors.Is.
type editErr string

func (e editErr) Error() string { return string(e) }

// Edit error messages, kept verbatim from Crush's edit tool so the model sees
// the exact same guidance it is trained against.
const (
	ErrEditOldStringNotFound editErr = "old_string not found in file. Make sure it matches exactly, including whitespace and line breaks."
	ErrEditMultipleMatches   editErr = "old_string appears multiple times in the file. Please provide more context to ensure a unique match, or set replace_all to true"
	ErrEditNoChange          editErr = "new content is the same as old content. No changes made."
	ErrEditMustReadFirst     editErr = "you must read the file before editing it. Use ssh_read_file first"
)

// toUnixLineEndings converts CRLF to LF, reporting whether the input used
// CRLF. Mirrors fsext.ToUnixLineEndings in Crush.
func toUnixLineEndings(content string) (string, bool) {
	if strings.Contains(content, "\r\n") {
		return strings.ReplaceAll(content, "\r\n", "\n"), true
	}
	return content, false
}

// toWindowsLineEndings normalizes to LF then converts to CRLF, reporting
// whether the result differs from the input. Mirrors
// fsext.ToWindowsLineEndings in Crush.
func toWindowsLineEndings(content string) (string, bool) {
	unix := strings.ReplaceAll(content, "\r\n", "\n")
	converted := strings.ReplaceAll(unix, "\n", "\r\n")
	return converted, converted != content
}

// EditFile performs an exact find-and-replace edit on a remote file over
// SFTP, replicating the behavior of Crush's edit tool:
//
//   - oldString == ""  -> create a new file with newString (must not exist)
//   - newString == ""  -> delete oldString from the file
//   - otherwise        -> replace oldString with newString
//
// For replace/delete the match must be unique unless replaceAll is set. The
// file must have been read in this session (ReadFile) and must not have changed
// on the remote since that read. CRLF line endings are preserved.
func (m *SSHManager) EditFile(sessionID, remotePath, oldString, newString string, replaceAll bool) (string, error) {
	session, err := m.requireActive(sessionID)
	if err != nil {
		return "", err
	}

	client, err := newSFTPClient(session.client)
	if err != nil {
		return "", fmt.Errorf("failed to start sftp: %w", err)
	}
	defer client.Close()

	if oldString == "" {
		return m.editCreate(session, client, remotePath, newString)
	}
	return m.editExisting(session, client, remotePath, oldString, newString, replaceAll)
}

// editCreate handles the create-new-file mode: the file must not already exist.
func (m *SSHManager) editCreate(session *SSHSession, client *sftp.Client, remotePath, content string) (string, error) {
	if info, err := client.Stat(remotePath); err == nil {
		if info.IsDir() {
			return "", fmt.Errorf("path is a directory, not a file: %s", remotePath)
		}
		return "", fmt.Errorf("file already exists: %s", remotePath)
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("failed to access file: %w", err)
	}

	if dir := path.Dir(remotePath); dir != "." && dir != "" {
		if err := client.MkdirAll(dir); err != nil {
			return "", fmt.Errorf("failed to create parent directories: %w", err)
		}
	}

	if err := writeRemoteAll(client, remotePath, []byte(content)); err != nil {
		return "", err
	}

	// A freshly created file is considered read so the model can immediately
	// edit it again, matching Crush recording the read after createNewFile.
	session.recordRead(remotePath)

	return "File created: " + remotePath, nil
}

// editExisting handles the delete and replace modes against an existing file.
func (m *SSHManager) editExisting(session *SSHSession, client *sftp.Client, remotePath, oldString, newString string, replaceAll bool) (string, error) {
	info, err := client.Stat(remotePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("file not found: %s", remotePath)
		}
		return "", fmt.Errorf("failed to access file: %w", err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("path is a directory, not a file: %s", remotePath)
	}

	lastRead := session.lastReadTime(remotePath)
	if lastRead.IsZero() {
		return "", ErrEditMustReadFirst
	}

	modTime := info.ModTime().Truncate(time.Second)
	if modTime.After(lastRead) {
		return "", fmt.Errorf(
			"file %s has been modified since it was last read (mod time: %s, last read: %s)",
			remotePath, modTime.Format(time.RFC3339), lastRead.Format(time.RFC3339),
		)
	}

	raw, err := readRemoteAll(client, remotePath)
	if err != nil {
		return "", err
	}

	oldContent, isCrlf := toUnixLineEndings(string(raw))

	newContent, isDelete, err := applyEdit(oldContent, oldString, newString, replaceAll)
	if err != nil {
		return "", err
	}

	if !isDelete && oldContent == newContent {
		return "", ErrEditNoChange
	}

	out := newContent
	if isCrlf {
		out, _ = toWindowsLineEndings(newContent)
	}

	if err := writeRemoteAll(client, remotePath, []byte(out)); err != nil {
		return "", err
	}

	session.recordRead(remotePath)

	if isDelete {
		return "Content deleted from file: " + remotePath, nil
	}
	return "Content replaced in file: " + remotePath, nil
}

// applyEdit computes the new file content using Crush's exact match semantics:
// replaceAll uses a plain ReplaceAll; otherwise the match must be unique.
// isDelete is true when newString is empty (delete mode).
func applyEdit(oldContent, oldString, newString string, replaceAll bool) (newContent string, isDelete bool, err error) {
	isDelete = newString == ""

	if replaceAll {
		newContent = strings.ReplaceAll(oldContent, oldString, newString)
		if isDelete && newContent == oldContent {
			return "", isDelete, ErrEditOldStringNotFound
		}
		return newContent, isDelete, nil
	}

	index := strings.Index(oldContent, oldString)
	if index == -1 {
		return "", isDelete, ErrEditOldStringNotFound
	}
	if index != strings.LastIndex(oldContent, oldString) {
		return "", isDelete, ErrEditMultipleMatches
	}
	return oldContent[:index] + newString + oldContent[index+len(oldString):], isDelete, nil
}

// readRemoteAll reads an entire remote file into memory.
func readRemoteAll(client *sftp.Client, remotePath string) ([]byte, error) {
	f, err := client.Open(remotePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open remote file: %w", err)
	}
	defer f.Close()

	data, err := io.ReadAll(f)
	if err != nil {
		return nil, fmt.Errorf("failed to read remote file: %w", err)
	}
	return data, nil
}

// writeRemoteAll truncates/creates and writes the full contents of a remote
// file.
func writeRemoteAll(client *sftp.Client, remotePath string, data []byte) error {
	f, err := client.Create(remotePath)
	if err != nil {
		return fmt.Errorf("failed to create remote file: %w", err)
	}
	defer f.Close()

	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("failed to write remote file: %w", err)
	}
	return nil
}
