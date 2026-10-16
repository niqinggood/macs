package processutil

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// NativeCmd represents a command implemented in pure Go (no shell required).
type NativeCmd func(ctx context.Context, args []string, workDir string) (string, error)

// nativeCommands maps command names to their Go implementations.
// Both Linux and Windows command names are supported as aliases.
var nativeCommands = map[string]NativeCmd{
	// File listing
	"ls":  nativeLs,
	"dir": nativeLs,
	// File reading
	"cat":  nativeCat,
	"type": nativeCat,
	// Text search
	"grep":    nativeGrep,
	"findstr": nativeGrep,
	// Word/line count
	"wc": nativeWc,
	// Head / tail
	"head": nativeHead,
	"tail": nativeTail,
	// Copy / move / delete
	"cp":   nativeCp,
	"copy": nativeCp,
	"mv":   nativeMv,
	"move": nativeMv,
	"rm":   nativeRm,
	"del":  nativeRm,
	// Directory operations
	"mkdir": nativeMkdir,
	"rmdir": nativeRmdir,
	// Basic utils
	"echo":  nativeEcho,
	"pwd":   nativePwd,
	"which": nativeWhich,
	"sort":  nativeSort,
	"uniq":  nativeUniq,
	// Network & HTTP
	"curl": nativeCurl,
	"wget": nativeCurl,
	// File find
	"find": nativeFind,
	// Text processing
	"sed": nativeSed,
	"awk": nativeAwk,
}

// RouteCommand tries to execute a command natively. Returns (output, handled, error).
func RouteCommand(ctx context.Context, cmdStr string, workDir string) (string, bool, error) {
	if workDir == "" {
		workDir = "."
	}
	absDir, err := filepath.Abs(workDir)
	if err != nil {
		absDir = workDir
	}

	// Parse command: handle quoted strings
	parts := parseCommandLine(cmdStr)
	if len(parts) == 0 {
		return "", false, nil
	}

	name := strings.ToLower(parts[0])
	// Strip .exe suffix on Windows
	name = strings.TrimSuffix(name, ".exe")

	handler, ok := nativeCommands[name]
	if !ok {
		return "", false, nil // Not a native command, let shell handle it
	}

	output, err := handler(ctx, parts[1:], absDir)
	return output, true, err
}

func LookupNativeCommand(name string) (NativeCmd, bool) {
	command, ok := nativeCommands[strings.ToLower(strings.TrimSuffix(strings.TrimSpace(name), ".exe"))]
	return command, ok
}

// parseCommandLine splits a command string into arguments, respecting quotes.
func parseCommandLine(cmd string) []string {
	return ParseCommandLine(cmd)
}

// ========== Native Implementations ==========

func nativeLs(ctx context.Context, args []string, dir string) (string, error) {
	target := dir
	showAll := false
	showLong := false

	for _, a := range args {
		switch {
		case a == "-la" || a == "-al":
			showAll = true
			showLong = true
		case a == "-l":
			showLong = true
		case a == "-a":
			showAll = true
		case strings.HasPrefix(a, "-"):
			// ignore unknown flags
		default:
			p := a
			if !filepath.IsAbs(p) {
				p = filepath.Join(dir, p)
			}
			target = p
		}
	}

	entries, err := os.ReadDir(target)
	if err != nil {
		return "", fmt.Errorf("ls: %w", err)
	}

	sort.Slice(entries, func(i, j int) bool {
		if entries[i].IsDir() != entries[j].IsDir() {
			return entries[i].IsDir()
		}
		return strings.ToLower(entries[i].Name()) < strings.ToLower(entries[j].Name())
	})

	var out strings.Builder
	for _, e := range entries {
		if !showAll && strings.HasPrefix(e.Name(), ".") {
			continue
		}
		info, _ := e.Info()
		if showLong && info != nil {
			mode := info.Mode().String()
			size := info.Size()
			modTime := info.ModTime().Format("Jan 02 15:04")
			out.WriteString(fmt.Sprintf("%s %8d %s %s", mode, size, modTime, e.Name()))
		} else {
			if e.IsDir() {
				out.WriteString(e.Name() + "/")
			} else {
				out.WriteString(e.Name())
			}
		}
		out.WriteByte('\n')
	}
	return out.String(), nil
}

func nativeCat(ctx context.Context, args []string, dir string) (string, error) {
	if len(args) == 0 {
		// Read from stdin? Not supported natively, fall through
		return "", fmt.Errorf("cat: no file specified")
	}

	var out strings.Builder
	for _, f := range args {
		path := f
		if !filepath.IsAbs(path) {
			path = filepath.Join(dir, path)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("cat %s: %w", f, err)
		}
		out.Write(data)
	}
	return out.String(), nil
}

func nativeGrep(ctx context.Context, args []string, dir string) (string, error) {
	caseInsensitive := false
	invertMatch := false
	countOnly := false
	var pattern string
	var files []string

	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-i":
			caseInsensitive = true
		case a == "-v":
			invertMatch = true
		case a == "-c":
			countOnly = true
		case a == "-r" || a == "-R":
			// Recursive not fully implemented, skip flag
		case strings.HasPrefix(a, "-"):
			// skip other flags
		default:
			if pattern == "" {
				pattern = a
			} else {
				files = append(files, a)
			}
		}
	}

	if pattern == "" {
		return "", fmt.Errorf("grep: missing pattern")
	}

	var re *regexp.Regexp
	var err error
	if caseInsensitive {
		re, err = regexp.Compile("(?i)" + pattern)
	} else {
		re, err = regexp.Compile(pattern)
	}
	if err != nil {
		return "", fmt.Errorf("grep: invalid pattern: %w", err)
	}

	if len(files) == 0 {
		return "", fmt.Errorf("grep: no input files")
	}

	var out strings.Builder
	for _, f := range files {
		path := f
		if !filepath.IsAbs(path) {
			path = filepath.Join(dir, path)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		lines := strings.Split(string(data), "\n")
		matchCount := 0
		for _, line := range lines {
			matched := re.MatchString(line)
			if invertMatch {
				matched = !matched
			}
			if matched {
				if countOnly {
					matchCount++
				} else {
					if len(files) > 1 {
						out.WriteString(f + ":")
					}
					out.WriteString(line + "\n")
				}
			}
		}
		if countOnly {
			out.WriteString(fmt.Sprintf("%s:%d\n", f, matchCount))
		}
	}
	return out.String(), nil
}

func nativeWc(ctx context.Context, args []string, dir string) (string, error) {
	linesOnly := false
	wordsOnly := false
	charsOnly := false

	var files []string
	for _, a := range args {
		switch a {
		case "-l":
			linesOnly = true
		case "-w":
			wordsOnly = true
		case "-c", "-m":
			charsOnly = true
		default:
			files = append(files, a)
		}
	}

	if len(files) == 0 {
		return "", fmt.Errorf("wc: no file specified")
	}

	var out strings.Builder
	for _, f := range files {
		path := f
		if !filepath.IsAbs(path) {
			path = filepath.Join(dir, path)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("wc %s: %w", f, err)
		}
		content := string(data)
		lines := strings.Count(content, "\n")
		words := len(strings.Fields(content))
		chars := len(content)

		if linesOnly {
			out.WriteString(fmt.Sprintf("%d %s\n", lines, f))
		} else if wordsOnly {
			out.WriteString(fmt.Sprintf("%d %s\n", words, f))
		} else if charsOnly {
			out.WriteString(fmt.Sprintf("%d %s\n", chars, f))
		} else {
			out.WriteString(fmt.Sprintf("%d %d %d %s\n", lines, words, chars, f))
		}
	}
	return out.String(), nil
}

func nativeHead(ctx context.Context, args []string, dir string) (string, error) {
	n := 10
	var file string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "-n" && i+1 < len(args) {
			n, _ = strconv.Atoi(args[i+1])
			i++
		} else if strings.HasPrefix(a, "-n") {
			n, _ = strconv.Atoi(a[2:])
		} else {
			file = a
		}
	}
	if file == "" {
		return "", fmt.Errorf("head: no file specified")
	}
	path := file
	if !filepath.IsAbs(path) {
		path = filepath.Join(dir, path)
	}
	return readLines(path, 0, n)
}

func nativeTail(ctx context.Context, args []string, dir string) (string, error) {
	n := 10
	var file string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "-n" && i+1 < len(args) {
			n, _ = strconv.Atoi(args[i+1])
			i++
		} else if strings.HasPrefix(a, "-n") {
			n, _ = strconv.Atoi(a[2:])
		} else {
			file = a
		}
	}
	if file == "" {
		return "", fmt.Errorf("tail: no file specified")
	}
	path := file
	if !filepath.IsAbs(path) {
		path = filepath.Join(dir, path)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("tail %s: %w", file, err)
	}
	lines := strings.Split(string(data), "\n")
	start := len(lines) - n
	if start < 0 {
		start = 0
	}
	return strings.Join(lines[start:], "\n"), nil
}

func readLines(path string, start, count int) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	var lines []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if len(lines) <= start {
		return "", nil
	}
	end := start + count
	if end > len(lines) {
		end = len(lines)
	}
	return strings.Join(lines[start:end], "\n"), nil
}

func nativeCp(ctx context.Context, args []string, dir string) (string, error) {
	recursive := false
	var files []string
	for _, a := range args {
		if a == "-r" || a == "-R" {
			recursive = true
		} else {
			files = append(files, a)
		}
	}
	if len(files) < 2 {
		return "", fmt.Errorf("cp: source and destination required")
	}

	src := files[0]
	dst := files[len(files)-1]
	if !filepath.IsAbs(src) {
		src = filepath.Join(dir, src)
	}
	if !filepath.IsAbs(dst) {
		dst = filepath.Join(dir, dst)
	}

	srcInfo, err := os.Stat(src)
	if err != nil {
		return "", fmt.Errorf("cp: %w", err)
	}

	if srcInfo.IsDir() {
		if !recursive {
			return "", fmt.Errorf("cp: -r not specified; omitting directory '%s'", src)
		}
		return "", CopyDir(src, dst)
	}
	return "", copyFileNative(src, dst)
}

func copyFileNative(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0644)
}

func CopyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0755)
		}
		return copyFileNative(path, target)
	})
}

func nativeMv(ctx context.Context, args []string, dir string) (string, error) {
	if len(args) < 2 {
		return "", fmt.Errorf("mv: source and destination required")
	}
	src := args[0]
	dst := args[len(args)-1]
	if !filepath.IsAbs(src) {
		src = filepath.Join(dir, src)
	}
	if !filepath.IsAbs(dst) {
		dst = filepath.Join(dir, dst)
	}
	return "", os.Rename(src, dst)
}

func nativeRm(ctx context.Context, args []string, dir string) (string, error) {
	recursive := false
	force := false
	var files []string
	for _, a := range args {
		switch a {
		case "-r", "-R":
			recursive = true
		case "-f":
			force = true
		case "-rf", "-fr":
			recursive = true
			force = true
		default:
			files = append(files, a)
		}
	}
	for _, f := range files {
		path := f
		if !filepath.IsAbs(path) {
			path = filepath.Join(dir, path)
		}
		info, err := os.Stat(path)
		if err != nil {
			if !force {
				return "", fmt.Errorf("rm %s: %w", f, err)
			}
			continue
		}
		if info.IsDir() {
			if !recursive {
				return "", fmt.Errorf("rm: cannot remove '%s': Is a directory", f)
			}
			if err := os.RemoveAll(path); err != nil && !force {
				return "", err
			}
		} else {
			if err := os.Remove(path); err != nil && !force {
				return "", err
			}
		}
	}
	return "", nil
}

func nativeMkdir(ctx context.Context, args []string, dir string) (string, error) {
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			continue
		}
		path := a
		if !filepath.IsAbs(path) {
			path = filepath.Join(dir, path)
		}
		if err := os.MkdirAll(path, 0755); err != nil {
			return "", err
		}
	}
	return "", nil
}

func nativeRmdir(ctx context.Context, args []string, dir string) (string, error) {
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			continue
		}
		path := a
		if !filepath.IsAbs(path) {
			path = filepath.Join(dir, path)
		}
		if err := os.Remove(path); err != nil {
			return "", err
		}
	}
	return "", nil
}

func nativeEcho(ctx context.Context, args []string, dir string) (string, error) {
	return strings.Join(args, " ") + "\n", nil
}

func nativePwd(ctx context.Context, args []string, dir string) (string, error) {
	return dir + "\n", nil
}

func nativeWhich(ctx context.Context, args []string, dir string) (string, error) {
	if len(args) == 0 {
		return "", fmt.Errorf("which: missing argument")
	}
	// Check PATH
	for _, p := range strings.Split(os.Getenv("PATH"), string(os.PathListSeparator)) {
		full := filepath.Join(p, args[0])
		if _, err := os.Stat(full); err == nil {
			return full + "\n", nil
		}
		// Windows: also check with .exe
		fullExe := full + ".exe"
		if _, err := os.Stat(fullExe); err == nil {
			return fullExe + "\n", nil
		}
	}
	return "", fmt.Errorf("which: %s not found", args[0])
}

func nativeSort(ctx context.Context, args []string, dir string) (string, error) {
	reverse := false
	var file string
	for _, a := range args {
		if a == "-r" {
			reverse = true
		} else if !strings.HasPrefix(a, "-") {
			file = a
		}
	}
	if file == "" {
		return "", fmt.Errorf("sort: no file specified")
	}
	path := file
	if !filepath.IsAbs(path) {
		path = filepath.Join(dir, path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("sort: %w", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if reverse {
		sort.Sort(sort.Reverse(sort.StringSlice(lines)))
	} else {
		sort.Strings(lines)
	}
	return strings.Join(lines, "\n") + "\n", nil
}

func nativeUniq(ctx context.Context, args []string, dir string) (string, error) {
	var file string
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			file = a
		}
	}
	if file == "" {
		return "", fmt.Errorf("uniq: no file specified")
	}
	path := file
	if !filepath.IsAbs(path) {
		path = filepath.Join(dir, path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("uniq: %w", err)
	}
	lines := strings.Split(string(data), "\n")
	var out strings.Builder
	var prev string
	for _, line := range lines {
		if line != prev {
			out.WriteString(line + "\n")
			prev = line
		}
	}
	return out.String(), nil
}

// ========== curl (native HTTP client) ==========
func nativeCurl(ctx context.Context, args []string, dir string) (string, error) {
	method := "GET"
	var urlStr, data string
	headers := http.Header{}
	silent := false
	failOnHTTPError := false
	timeout := 30 * time.Second
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case (a == "-X" || a == "--request") && i+1 < len(args):
			method = args[i+1]
			i++
		case strings.HasPrefix(a, "-X") && len(a) > 2:
			method = a[2:]
		case (a == "-d" || a == "--data" || a == "--data-raw" || a == "--data-binary") && i+1 < len(args):
			data = args[i+1]
			i++
		case strings.HasPrefix(a, "--data="):
			data = strings.TrimPrefix(a, "--data=")
		case strings.HasPrefix(a, "--data-raw="):
			data = strings.TrimPrefix(a, "--data-raw=")
		case strings.HasPrefix(a, "--data-binary="):
			data = strings.TrimPrefix(a, "--data-binary=")
		case (a == "-H" || a == "--header") && i+1 < len(args):
			if key, value, ok := parseCurlHeader(args[i+1]); ok {
				headers.Add(key, value)
			}
			i++
		case strings.HasPrefix(a, "--header="):
			if key, value, ok := parseCurlHeader(strings.TrimPrefix(a, "--header=")); ok {
				headers.Add(key, value)
			}
		case (a == "-m" || a == "--max-time" || a == "--connect-timeout") && i+1 < len(args):
			if parsed, ok := parseCurlSeconds(args[i+1]); ok {
				timeout = parsed
			}
			i++
		case strings.HasPrefix(a, "-m") && len(a) > 2:
			if parsed, ok := parseCurlSeconds(a[2:]); ok {
				timeout = parsed
			}
		case strings.HasPrefix(a, "--max-time="):
			if parsed, ok := parseCurlSeconds(strings.TrimPrefix(a, "--max-time=")); ok {
				timeout = parsed
			}
		case strings.HasPrefix(a, "--connect-timeout="):
			if parsed, ok := parseCurlSeconds(strings.TrimPrefix(a, "--connect-timeout=")); ok {
				timeout = parsed
			}
		case (a == "--url") && i+1 < len(args):
			urlStr = args[i+1]
			i++
		case strings.HasPrefix(a, "--url="):
			urlStr = strings.TrimPrefix(a, "--url=")
		case a == "-s" || a == "--silent" || (isCurlShortFlag(a) && strings.Contains(a[1:], "s")):
			silent = true
			if isCurlShortFlag(a) && strings.Contains(a[1:], "f") {
				failOnHTTPError = true
			}
		case a == "-f" || a == "--fail" || (isCurlShortFlag(a) && strings.Contains(a[1:], "f")):
			failOnHTTPError = true
		case a == "-I" || a == "--head":
			method = "HEAD"
		case a == "-L" || a == "--location" || a == "-S" || a == "--show-error" || a == "--compressed":
			// Go's client follows redirects by default. Other flags are display-only here.
		case strings.HasPrefix(a, "-"):
			if curlFlagConsumesValue(a) && i+1 < len(args) {
				i++
			}
		default:
			if !strings.HasPrefix(a, "-") && urlStr == "" {
				urlStr = a
			}
		}
	}
	if urlStr == "" {
		return "", fmt.Errorf("curl: no URL specified")
	}
	if !strings.HasPrefix(urlStr, "http") {
		urlStr = "https://" + urlStr
	}
	var body io.Reader
	if data != "" {
		body = strings.NewReader(data)
		if method == "GET" {
			method = "POST"
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, urlStr, body)
	if err != nil {
		return "", fmt.Errorf("curl: %w", err)
	}
	req.Header = headers
	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("curl: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if failOnHTTPError && resp.StatusCode >= 400 {
		return string(respBody), fmt.Errorf("curl: HTTP status %d", resp.StatusCode)
	}
	if silent {
		return string(respBody), nil
	}
	return fmt.Sprintf("HTTP %d\n%s", resp.StatusCode, string(respBody)), nil
}

// nativeCurlSupportsArgs guards the Go fast path from silently accepting curl
// flags whose behavior it does not implement. Cookie jars, authentication,
// output files, proxies, and other advanced options must go to the real curl
// binary so login/session workflows keep their semantics.
func NativeCurlSupportsArgs(args []string) bool {
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case (a == "-X" || a == "--request" || a == "-d" || a == "--data" || a == "--data-raw" || a == "--data-binary" ||
			a == "-H" || a == "--header" || a == "-m" || a == "--max-time" || a == "--connect-timeout" || a == "--url"):
			if i+1 >= len(args) {
				return false
			}
			i++
		case strings.HasPrefix(a, "-X") && len(a) > 2:
		case strings.HasPrefix(a, "--data=") || strings.HasPrefix(a, "--data-raw=") || strings.HasPrefix(a, "--data-binary="):
		case strings.HasPrefix(a, "--header=") || strings.HasPrefix(a, "--max-time=") || strings.HasPrefix(a, "--connect-timeout=") || strings.HasPrefix(a, "--url="):
		case a == "-s" || a == "--silent" || a == "-f" || a == "--fail" ||
			a == "-I" || a == "--head" || a == "-L" || a == "--location" ||
			a == "-S" || a == "--show-error" || a == "--compressed":
		case isSupportedCurlShortFlagGroup(a):
		case strings.HasPrefix(a, "-"):
			return false
		default:
			// Positional URL.
		}
	}
	return true
}

func isSupportedCurlShortFlagGroup(flag string) bool {
	if !isCurlShortFlag(flag) || len(flag) <= 2 {
		return false
	}
	for _, r := range flag[1:] {
		if !strings.ContainsRune("sfSLI", r) {
			return false
		}
	}
	return true
}

func parseCurlSeconds(value string) (time.Duration, bool) {
	seconds, err := strconv.ParseFloat(value, 64)
	if err != nil || seconds <= 0 {
		return 0, false
	}
	return time.Duration(seconds * float64(time.Second)), true
}

func parseCurlHeader(value string) (key, headerValue string, ok bool) {
	key, headerValue, ok = strings.Cut(value, ":")
	if !ok {
		return "", "", false
	}
	key = strings.TrimSpace(key)
	headerValue = strings.TrimSpace(headerValue)
	if key == "" {
		return "", "", false
	}
	return key, headerValue, true
}

func curlFlagConsumesValue(flag string) bool {
	switch flag {
	case "-A", "--user-agent", "-b", "--cookie", "-e", "--referer", "-o", "--output", "-u", "--user", "-x", "--proxy", "--cacert", "--cert", "--key", "--resolve", "--connect-to":
		return true
	default:
		return false
	}
}

func isCurlShortFlag(flag string) bool {
	return strings.HasPrefix(flag, "-") && !strings.HasPrefix(flag, "--") && len(flag) > 1
}

// ========== find (filepath.Walk wrapper) ==========
func nativeFind(ctx context.Context, args []string, dir string) (string, error) {
	namePattern := "*"
	fType := ""
	var paths []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-name" && i+1 < len(args):
			namePattern = args[i+1]
			i++
		case a == "-type" && i+1 < len(args):
			fType = args[i+1]
			i++
		case strings.HasPrefix(a, "-"):
		default:
			paths = append(paths, a)
		}
	}
	if len(paths) == 0 {
		paths = []string{dir}
	}
	var out strings.Builder
	for _, root := range paths {
		p := root
		if !filepath.IsAbs(p) {
			p = filepath.Join(dir, p)
		}
		filepath.Walk(p, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			if fType == "f" && info.IsDir() {
				return nil
			}
			if fType == "d" && !info.IsDir() {
				return nil
			}
			matched, _ := filepath.Match(namePattern, info.Name())
			if matched {
				out.WriteString(path + "\n")
			}
			return nil
		})
	}
	return out.String(), nil
}

// ========== sed (s/pattern/replacement/g) ==========
func nativeSed(ctx context.Context, args []string, dir string) (string, error) {
	var expression, file string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "-") {
			if a == "-e" && i+1 < len(args) {
				expression = args[i+1]
				i++
			}
			continue
		}
		if expression == "" && len(a) > 2 && a[0] == 's' {
			expression = a
		} else if file == "" {
			file = a
		}
	}
	if expression == "" || file == "" {
		return "", fmt.Errorf("sed: usage: sed 's/pattern/replacement/g' file")
	}
	if expression[0] != 's' || len(expression) < 4 {
		return "", fmt.Errorf("sed: only s/pattern/replacement/ supported")
	}
	sep := string(expression[1])
	parts := strings.Split(expression[2:], sep)
	if len(parts) < 2 {
		return "", fmt.Errorf("sed: invalid expression: %s", expression)
	}
	pattern := parts[0]
	replacement := parts[1]
	global := len(parts) > 2 && strings.Contains(parts[2], "g")
	path := file
	if !filepath.IsAbs(path) {
		path = filepath.Join(dir, path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("sed: %w", err)
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return "", fmt.Errorf("sed: invalid pattern: %w", err)
	}
	if global {
		return re.ReplaceAllString(string(data), replacement), nil
	}
	loc := re.FindStringIndex(string(data))
	if loc != nil {
		result := string(data)[:loc[0]] + replacement + string(data)[loc[1]:]
		return result, nil
	}
	return string(data), nil
}

// ========== awk (basic column extraction) ==========
func nativeAwk(ctx context.Context, args []string, dir string) (string, error) {
	var program, file, fieldSep string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-F" && i+1 < len(args):
			fieldSep = args[i+1]
			i++
		case strings.HasPrefix(a, "-"):
		default:
			if program == "" {
				program = a
			} else if file == "" {
				file = a
			}
		}
	}
	if file == "" {
		return "", fmt.Errorf("awk: no input file")
	}
	path := file
	if !filepath.IsAbs(path) {
		path = filepath.Join(dir, path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("awk: %w", err)
	}
	if fieldSep == "" {
		fieldSep = " "
	}
	program = strings.Trim(program, "'\"")
	lines := strings.Split(string(data), "\n")
	var out strings.Builder
	if strings.HasPrefix(program, "{print") && strings.HasSuffix(program, "}") {
		inner := program[7 : len(program)-1]
		cols := strings.Split(inner, ",")
		for _, line := range lines {
			if line == "" {
				continue
			}
			fields := strings.Fields(line)
			if fieldSep != " " {
				fields = strings.Split(line, fieldSep)
			}
			var vals []string
			for _, col := range cols {
				col = strings.TrimSpace(col)
				if strings.HasPrefix(col, "$") {
					num, err := strconv.Atoi(col[1:])
					if err == nil && num > 0 && num <= len(fields) {
						vals = append(vals, fields[num-1])
					}
				}
			}
			if len(vals) > 0 {
				out.WriteString(strings.Join(vals, " ") + "\n")
			}
		}
	} else {
		out.Write(data)
	}
	return out.String(), nil
}

// parseNativeArgs splits a command string into arguments for native execution.
func parseNativeArgs(cmdStr string) []string {
	cmdStr = strings.TrimSpace(cmdStr)
	var args []string
	inQuote := false
	current := ""
	for _, c := range cmdStr {
		switch {
		case c == '"':
			inQuote = !inQuote
		case c == ' ' && !inQuote:
			if current != "" {
				args = append(args, current)
				current = ""
			}
		default:
			current += string(c)
		}
	}
	if current != "" {
		args = append(args, current)
	}
	return args
}
