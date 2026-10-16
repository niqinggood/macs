package agentos

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"mac/internal/attachments"
	"mac/internal/projectctx"
)

const (
	MaxUploadBytes     = int64(25 << 20)
	MaxMultipartBytes  = MaxUploadBytes + (1 << 20)
	maxUploadNameRunes = 160
)

var (
	ErrUploadTooLarge = errors.New("AgentOS upload is too large")
	ErrUploadEmpty    = errors.New("AgentOS upload is empty")
)

func SaveUploadedFile(workspace, originalName string, source io.Reader) (attachments.File, error) {
	root, err := filepath.Abs(strings.TrimSpace(workspace))
	if err != nil {
		return attachments.File{}, fmt.Errorf("resolve upload workspace: %w", err)
	}
	resolvedRoot, err := projectctx.ResolvePathThroughExistingAncestor(root)
	if err != nil {
		return attachments.File{}, fmt.Errorf("resolve upload workspace: %w", err)
	}

	uploadID, err := NewUploadID()
	if err != nil {
		return attachments.File{}, err
	}
	relativeDirectory := filepath.Join("inputs", uploadID)
	destinationDirectory := filepath.Join(root, relativeDirectory)
	resolvedDirectory, err := projectctx.ResolvePathThroughExistingAncestor(destinationDirectory)
	if err != nil {
		return attachments.File{}, fmt.Errorf("resolve upload directory: %w", err)
	}
	if !projectctx.PathWithinEqualFold(resolvedRoot, resolvedDirectory) {
		return attachments.File{}, errors.New("upload directory is outside the workspace")
	}
	if err := os.MkdirAll(resolvedDirectory, 0o700); err != nil {
		return attachments.File{}, fmt.Errorf("create upload directory: %w", err)
	}

	fileName := SafeUploadName(originalName)
	destination := filepath.Join(resolvedDirectory, fileName)
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		_ = os.Remove(resolvedDirectory)
		return attachments.File{}, fmt.Errorf("create uploaded file: %w", err)
	}

	written, copyErr := io.Copy(output, io.LimitReader(source, MaxUploadBytes+1))
	closeErr := output.Close()
	if copyErr != nil || closeErr != nil || written == 0 || written > MaxUploadBytes {
		_ = os.Remove(destination)
		_ = os.Remove(resolvedDirectory)
		switch {
		case copyErr != nil:
			return attachments.File{}, fmt.Errorf("write uploaded file: %w", copyErr)
		case closeErr != nil:
			return attachments.File{}, fmt.Errorf("close uploaded file: %w", closeErr)
		case written == 0:
			return attachments.File{}, ErrUploadEmpty
		default:
			return attachments.File{}, ErrUploadTooLarge
		}
	}

	mimeType := attachments.MIMEType(destination)
	return attachments.File{
		Name:     fileName,
		Path:     filepath.ToSlash(filepath.Join(relativeDirectory, fileName)),
		MIMEType: mimeType,
		Size:     written,
		Kind:     attachments.Kind(mimeType, destination),
	}, nil
}

func NewUploadID() (string, error) {
	random := make([]byte, 12)
	if _, err := rand.Read(random); err != nil {
		return "", fmt.Errorf("generate upload id: %w", err)
	}
	return "upl_" + hex.EncodeToString(random), nil
}

func SafeUploadName(value string) string {
	value = strings.ReplaceAll(strings.TrimSpace(value), `\`, "/")
	value = filepath.Base(filepath.FromSlash(value))
	value = strings.Map(func(character rune) rune {
		if unicode.IsControl(character) || character == '/' || character == '\\' {
			return -1
		}
		return character
	}, value)
	value = strings.TrimSpace(value)
	if value == "" || value == "." {
		return "upload.bin"
	}
	runes := []rune(value)
	if len(runes) <= maxUploadNameRunes {
		return value
	}
	extension := filepath.Ext(value)
	baseRunes := []rune(strings.TrimSuffix(value, extension))
	extensionRunes := []rune(extension)
	limit := maxUploadNameRunes - len(extensionRunes)
	if limit < 1 {
		limit = maxUploadNameRunes
		extensionRunes = nil
	}
	if len(baseRunes) > limit {
		baseRunes = baseRunes[:limit]
	}
	return string(baseRunes) + string(extensionRunes)
}
