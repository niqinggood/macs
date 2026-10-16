package main

import (
	"bytes"
	"fmt"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const maxScannedFileSize = 8 << 20

type finding struct {
	path    string
	line    int
	message string
}

var (
	credentialAssignmentPattern = regexp.MustCompile(`(?im)(password|passwd|pwd|secret|token|api[_-]?key|access[_-]?key|private[_-]?key)\s*[:=]\s*["']([^"'\r\n]+)["']`)
	publicIPCandidatePattern    = regexp.MustCompile(`(?:^|[^0-9])((?:[0-9]{1,3}\.){3}[0-9]{1,3})(?:[^0-9]|$)`)
	privateKeyPattern           = regexp.MustCompile(`-----BEGIN (?:RSA |EC |OPENSSH )?PRIVATE KEY-----`)
	personalPathPattern         = regexp.MustCompile(`(?i)[a-z]:[/\\](?:users|a?debug[0-9]*)[/\\]`)
	knownSecretPatterns         = []*regexp.Regexp{
		regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{20,}\b`),
		regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{20,}\b`),
		regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`),
	}
)

var binaryExtensions = map[string]struct{}{
	".7z": {}, ".a": {}, ".avi": {}, ".db": {}, ".dll": {}, ".docx": {},
	".dylib": {}, ".exe": {}, ".gif": {}, ".gz": {}, ".ico": {}, ".jpeg": {},
	".jpg": {}, ".keychain": {}, ".m4a": {}, ".mkv": {}, ".mov": {}, ".mp3": {},
	".mp4": {}, ".onnx": {}, ".otf": {}, ".pdf": {}, ".png": {}, ".pptx": {},
	".pt": {}, ".rar": {}, ".so": {}, ".sqlite": {}, ".syso": {}, ".tar": {},
	".tiff": {}, ".ttf": {}, ".wav": {}, ".webm": {}, ".webp": {}, ".woff": {},
	".woff2": {}, ".xlsx": {}, ".zip": {},
}

func main() {
	root := "."
	if len(os.Args) > 1 {
		root = os.Args[1]
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "release credential guard:", err)
		os.Exit(2)
	}

	findings, err := scanReleaseInputs(absRoot)
	if err != nil {
		fmt.Fprintln(os.Stderr, "release credential guard:", err)
		os.Exit(2)
	}
	findings = append(findings, trackedRuntimeConfigFindings(absRoot)...)
	if len(findings) == 0 {
		fmt.Println("release credential guard: OK")
		return
	}

	sort.Slice(findings, func(i, j int) bool {
		if findings[i].path != findings[j].path {
			return findings[i].path < findings[j].path
		}
		if findings[i].line != findings[j].line {
			return findings[i].line < findings[j].line
		}
		return findings[i].message < findings[j].message
	})
	fmt.Fprintln(os.Stderr, "release credential guard failed:")
	for _, item := range findings {
		location := item.path
		if item.line > 0 {
			location += ":" + strconv.Itoa(item.line)
		}
		fmt.Fprintf(os.Stderr, "  - %s: %s\n", location, item.message)
	}
	os.Exit(1)
}

func scanReleaseInputs(root string) ([]finding, error) {
	paths, err := gitReleasePaths(root)
	if err == nil {
		return scanReleasePaths(root, paths)
	}

	// Keep the guard usable from source archives that do not include .git.
	return scanReleasePaths(root, []string{
		"builtin",
		"openkitty-config.template.yaml",
	})
}

func gitReleasePaths(root string) ([]string, error) {
	cmd := exec.Command("git", "-C", root, "-c", "core.quotePath=false", "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	ignoredOutput, err := exec.Command("git", "-C", root, "-c", "core.quotePath=false", "ls-files", "-z", "-ci", "--exclude-standard").Output()
	if err != nil {
		return nil, err
	}
	ignored := make(map[string]struct{}, bytes.Count(ignoredOutput, []byte{0}))
	for _, raw := range bytes.Split(ignoredOutput, []byte{0}) {
		if len(raw) > 0 {
			ignored[string(raw)] = struct{}{}
		}
	}
	paths := make([]string, 0, bytes.Count(output, []byte{0}))
	for _, raw := range bytes.Split(output, []byte{0}) {
		if len(raw) == 0 {
			continue
		}
		if _, skip := ignored[string(raw)]; skip {
			continue
		}
		paths = append(paths, filepath.FromSlash(string(raw)))
	}
	return paths, nil
}

func scanReleasePaths(root string, inputs []string) ([]finding, error) {
	var findings []finding
	for _, input := range inputs {
		if !filepath.IsAbs(input) {
			input = filepath.Join(root, input)
		}
		info, err := os.Stat(input)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		if !info.IsDir() {
			if shouldSkipFile(input, fs.FileInfoToDirEntry(info)) {
				continue
			}
			data, err := os.ReadFile(input)
			if err != nil {
				return nil, err
			}
			if bytes.IndexByte(data, 0) >= 0 {
				continue
			}
			findings = append(findings, scanText(relativePath(root, input), data)...)
			continue
		}
		err = filepath.WalkDir(input, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				return nil
			}
			if shouldSkipFile(path, entry) {
				return nil
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if bytes.IndexByte(data, 0) >= 0 {
				return nil
			}
			findings = append(findings, scanText(relativePath(root, path), data)...)
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return deduplicate(findings), nil
}

func shouldSkipFile(path string, entry fs.DirEntry) bool {
	if _, skip := binaryExtensions[strings.ToLower(filepath.Ext(path))]; skip {
		return true
	}
	if info, err := entry.Info(); err == nil && info.Size() > maxScannedFileSize {
		return true
	}
	return false
}

func scanText(path string, data []byte) []finding {
	text := string(data)
	lowerPath := strings.ToLower(filepath.ToSlash(path))
	operationsScope := strings.HasPrefix(lowerPath, "builtin/devops/") ||
		strings.HasPrefix(lowerPath, "builtin/monitoring/") ||
		lowerPath == "openkitty-config.template.yaml"
	syntheticTestData := strings.HasSuffix(lowerPath, "_test.go") ||
		strings.Contains(lowerPath, "/testdata/") ||
		strings.HasPrefix(lowerPath, "testdata/")
	var findings []finding

	if !syntheticTestData {
		for _, match := range privateKeyPattern.FindAllStringIndex(text, -1) {
			findings = append(findings, finding{path, lineAt(text, match[0]), "embedded private key material"})
		}
		for _, pattern := range knownSecretPatterns {
			for _, match := range pattern.FindAllStringIndex(text, -1) {
				value := text[match[0]:match[1]]
				if !isPlaceholder(value) {
					findings = append(findings, finding{path, lineAt(text, match[0]), "value resembles a live access token"})
				}
			}
		}
	}

	if !operationsScope {
		return findings
	}
	for _, match := range credentialAssignmentPattern.FindAllStringSubmatchIndex(text, -1) {
		value := text[match[4]:match[5]]
		if !isPlaceholder(value) {
			findings = append(findings, finding{path, lineAt(text, match[0]), "literal credential assignment in release content"})
		}
	}
	for _, match := range publicIPCandidatePattern.FindAllStringSubmatchIndex(text, -1) {
		candidate := text[match[2]:match[3]]
		if isPublicIPv4(candidate) {
			findings = append(findings, finding{path, lineAt(text, match[2]), "public IPv4 address embedded in operations content"})
		}
	}
	for _, match := range personalPathPattern.FindAllStringIndex(text, -1) {
		findings = append(findings, finding{path, lineAt(text, match[0]), "operator-specific local path embedded in release content"})
	}
	if index := strings.Index(text, "AutoAddPolicy"); index >= 0 {
		findings = append(findings, finding{path, lineAt(text, index), "SSH host-key verification bypass in release content"})
	}
	return findings
}

func isPlaceholder(value string) bool {
	normalized := strings.ToLower(strings.TrimSpace(value))
	if normalized == "" || normalized == "..." || normalized == "none" || normalized == "null" {
		return true
	}
	if strings.HasPrefix(normalized, "<") && strings.HasSuffix(normalized, ">") {
		return true
	}
	if strings.HasPrefix(normalized, "${") || strings.HasPrefix(normalized, "$env:") {
		return true
	}
	markers := []string{
		"your_", "your-", "example", "placeholder", "replace", "change_me",
		"change-me", "changeme", "dummy", "not-a-real", "credential_ref",
		"secret-store", "secret_ref", "getenv", "environ", "sys.argv", "getpass",
	}
	for _, marker := range markers {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
}

func isPublicIPv4(value string) bool {
	ip := net.ParseIP(value)
	if ip == nil {
		return false
	}
	v4 := ip.To4()
	if v4 == nil {
		return false
	}
	a, b, c := v4[0], v4[1], v4[2]
	if a == 0 || a == 10 || a == 127 || a >= 224 {
		return false
	}
	if a == 100 && b >= 64 && b <= 127 {
		return false
	}
	if a == 169 && b == 254 {
		return false
	}
	if a == 172 && b >= 16 && b <= 31 {
		return false
	}
	if a == 192 && b == 168 {
		return false
	}
	if (a == 192 && b == 0 && c == 2) || (a == 198 && b == 51 && c == 100) || (a == 203 && b == 0 && c == 113) {
		return false
	}
	if a == 198 && (b == 18 || b == 19) {
		return false
	}
	return true
}

func trackedRuntimeConfigFindings(root string) []finding {
	if exec.Command("git", "-C", root, "rev-parse", "--is-inside-work-tree").Run() != nil {
		return nil
	}
	output, err := exec.Command("git", "-C", root, "ls-files", "-z").Output()
	if err != nil {
		return []finding{{".git/index", 0, "unable to inspect tracked release files"}}
	}
	var findings []finding
	for _, raw := range bytes.Split(output, []byte{0}) {
		path := filepath.ToSlash(string(raw))
		lower := strings.ToLower(path)
		switch {
		case lower == "openkitty-config.yaml":
			findings = append(findings, finding{path, 0, "live runtime config must not be tracked; keep only the template"})
		case (filepath.Base(lower) == ".env" || strings.HasPrefix(filepath.Base(lower), ".env.")) && !strings.HasSuffix(lower, ".example"):
			findings = append(findings, finding{path, 0, "environment secret file must not be tracked"})
		case filepath.Base(lower) == "id_rsa" || filepath.Base(lower) == "id_ed25519":
			findings = append(findings, finding{path, 0, "SSH private key filename must not be tracked"})
		case filepath.Ext(lower) == ".lic":
			findings = append(findings, finding{path, 0, "signed product license file must not be tracked"})
		}
	}
	return findings
}

func relativePath(root, path string) string {
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(relative)
}

func lineAt(text string, offset int) int {
	return 1 + strings.Count(text[:offset], "\n")
}

func deduplicate(items []finding) []finding {
	seen := make(map[string]struct{}, len(items))
	result := make([]finding, 0, len(items))
	for _, item := range items {
		key := fmt.Sprintf("%s:%d:%s", item.path, item.line, item.message)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, item)
	}
	return result
}
