package development

import (
	"bufio"
	"bytes"
	"context"
	"debug/buildinfo"
	"debug/elf"
	"debug/macho"
	"debug/pe"
	"errors"
	"fmt"
	"io"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"mac/internal/configvalue"
	"mac/internal/portablepath"
	"mac/internal/processutil"
)

// BinaryTool inspects compiled binaries without requiring platform-specific
// utilities for the common read-only operations. External tools are used only
// where the Go toolchain or a native strip utility is the actual implementation.
type BinaryTool struct {
	workspace string
}

func NewBinaryTool(workspace string) *BinaryTool {
	return &BinaryTool{workspace: workspace}
}

func (t *BinaryTool) Name() string { return "binary" }

func (t *BinaryTool) MaxResultSize() int { return 24000 }

func (t *BinaryTool) Description() string {
	return "Reverse engineer or inspect binaries: format, strings, symbols, and focused disassembly. " +
		"Actions: analyze (format, architecture, size, imports, security), " +
		"strings (built-in printable string extraction), symbols (search symbol table), " +
		"disassemble (focused function disassembly), run (direct execution without shell), " +
		"compare (compare two binaries for size/feature changes), " +
		"strip (strip debug info and symbols to reduce size)."
}

func (t *BinaryTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action": map[string]interface{}{
				"type":        "string",
				"enum":        []string{"analyze", "strings", "symbols", "disassemble", "run", "compare", "strip"},
				"description": "Read-only inspection: analyze, strings, symbols, disassemble. run executes the binary directly without a shell. Other actions: compare, strip.",
			},
			"file": map[string]interface{}{
				"type":        "string",
				"description": "Binary path, relative to the workspace or absolute",
			},
			"file2": map[string]interface{}{
				"type":        "string",
				"description": "Second binary for comparison",
			},
			"output": map[string]interface{}{
				"type":        "string",
				"description": "Output path for stripped binary",
			},
			"contains": map[string]interface{}{
				"type":        "string",
				"description": "Case-insensitive filter for strings or symbols; strongly recommended for focused output",
			},
			"symbol": map[string]interface{}{
				"type":        "string",
				"description": "Function/symbol name or regular expression to disassemble, for example main.verify",
			},
			"min_length": map[string]interface{}{
				"type":        "integer",
				"description": "Minimum printable string length (default 4, range 2-1024)",
				"minimum":     2,
				"maximum":     1024,
			},
			"limit": map[string]interface{}{
				"type":        "integer",
				"description": "Maximum strings or symbols returned (default 100, maximum 1000)",
				"minimum":     1,
				"maximum":     1000,
			},
			"max_lines": map[string]interface{}{
				"type":        "integer",
				"description": "Maximum disassembly lines returned (default 240, maximum 1200)",
				"minimum":     1,
				"maximum":     1200,
			},
			"args": map[string]interface{}{
				"type":        "array",
				"description": "Argument vector for action=run; each item is passed verbatim without shell parsing",
				"items":       map[string]interface{}{"type": "string"},
			},
			"stdin": map[string]interface{}{
				"type":        "string",
				"description": "Optional standard input for action=run",
			},
			"work_dir": map[string]interface{}{
				"type":        "string",
				"description": "Working directory for action=run, relative to the workspace (default workspace root)",
			},
			"timeout": map[string]interface{}{
				"type":        "integer",
				"description": "Timeout in seconds for action=run (default 30, range 1-120)",
				"minimum":     1,
				"maximum":     120,
			},
		},
		"required": []string{"action", "file"},
	}
}

func (t *BinaryTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	action := configvalue.String(params, "action", "analyze")
	file := configvalue.String(params, "file", "")
	file2 := configvalue.String(params, "file2", "")
	output := configvalue.String(params, "output", "")

	if file == "" {
		return "Error: 'file' is required", nil
	}

	switch action {
	case "analyze":
		return t.analyzeBinary(ctx, file)
	case "strings":
		return t.extractStrings(
			file,
			configvalue.String(params, "contains", ""),
			clampBinaryInt(configvalue.Int(params, "min_length", 4), 2, 1024),
			clampBinaryInt(configvalue.Int(params, "limit", 100), 1, 1000),
		)
	case "symbols":
		return t.listSymbols(
			ctx,
			file,
			configvalue.String(params, "contains", ""),
			clampBinaryInt(configvalue.Int(params, "limit", 100), 1, 1000),
		)
	case "disassemble":
		return t.disassemble(
			ctx,
			file,
			configvalue.String(params, "symbol", ""),
			clampBinaryInt(configvalue.Int(params, "max_lines", 240), 1, 1200),
		)
	case "run":
		return t.runBinary(
			ctx,
			file,
			binaryStringSlice(params["args"]),
			configvalue.String(params, "stdin", ""),
			configvalue.String(params, "work_dir", ""),
			time.Duration(clampBinaryInt(configvalue.Int(params, "timeout", 30), 1, 120))*time.Second,
		)
	case "compare":
		return t.compareBinaries(file, file2)
	case "strip":
		return t.stripBinary(ctx, file, output)
	default:
		return "", fmt.Errorf("unknown action: %s", action)
	}
}

// ================== Analyze ==================

type binaryInfo struct {
	Path       string
	Size       int64
	SizeHuman  string
	Format     string // "elf", "macho", "pe"
	Arch       string // "amd64", "arm64", "386"
	GoVersion  string
	BuildInfo  map[string]string
	Symbols    int
	Imports    []string
	GotSymbols int // Go runtime symbols
	Sections   []sectionInfo
	Security   securityInfo
}

type sectionInfo struct {
	Name string
	Size int64
	Pct  float64
}

type securityInfo struct {
	PIE         bool // Position Independent Executable
	StackCanary bool
	NX          bool // Non-executable stack
	RELRO       bool // Read-only relocations
	Striped     bool
	UPX         bool // Compressed with UPX
}

func (t *BinaryTool) analyzeBinary(ctx context.Context, filePath string) (string, error) {
	fullPath := t.resolvePath(filePath)
	info, err := os.Stat(fullPath)
	if err != nil {
		return "", fmt.Errorf("stat binary: %w", err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("binary path is a directory: %s", filePath)
	}

	bi := &binaryInfo{
		Path:      filePath,
		Size:      info.Size(),
		SizeHuman: formatBinarySize(info.Size()),
		Sections:  make([]sectionInfo, 0),
	}

	if err := inspectNativeBinary(fullPath, bi); err != nil {
		bi.Format = "unknown"
		bi.Arch = "unknown"
	}

	// Go build information and symbol tables are available cross-platform
	// through the standard library and the installed Go toolchain.
	if goInfo, err := buildinfo.ReadFile(fullPath); err == nil {
		t.analyzeGoBinary(ctx, fullPath, bi, goInfo)
	} else if bi.Format == "elf" {
		t.enrichELFSecurity(fullPath, bi)
	}

	return t.formatBinaryReport(bi), nil
}

func (t *BinaryTool) analyzeGoBinary(ctx context.Context, fullPath string, bi *binaryInfo, info *buildinfo.BuildInfo) {
	bi.GoVersion = info.GoVersion
	bi.BuildInfo = make(map[string]string)
	if info.Path != "" {
		bi.BuildInfo["import_path"] = info.Path
	}
	if info.Main.Path != "" {
		module := info.Main.Path
		if info.Main.Version != "" && info.Main.Version != "(devel)" {
			module += " " + info.Main.Version
		}
		bi.BuildInfo["module"] = module
	}
	for _, setting := range info.Settings {
		switch setting.Key {
		case "-buildmode", "-compiler", "CGO_ENABLED", "GOARCH", "GOOS", "vcs", "vcs.revision", "vcs.time", "vcs.modified":
			bi.BuildInfo[setting.Key] = setting.Value
		}
		if setting.Key == "-buildmode" && setting.Value == "pie" {
			bi.Security.PIE = true
		}
	}

	symOut, symErr := runBinaryCmdContext(ctx, "go", "tool", "nm", "-size", fullPath)
	if symErr != nil {
		bi.Security.Striped = true
		return
	}
	lines := nonEmptyBinaryLines(symOut)
	bi.Symbols = len(lines)
	bi.Security.Striped = bi.Symbols == 0
	for _, line := range lines {
		if strings.Contains(line, " runtime.") || strings.Contains(line, " reflect.") {
			bi.GotSymbols++
		}
		if strings.Contains(line, "__stack_chk_fail") {
			bi.Security.StackCanary = true
		}
	}
}

func inspectNativeBinary(fullPath string, bi *binaryInfo) error {
	if file, err := pe.Open(fullPath); err == nil {
		defer file.Close()
		bi.Format = "pe"
		bi.Arch = peArchitecture(file.Machine)
		for _, section := range file.Sections {
			bi.Sections = append(bi.Sections, sectionInfo{Name: section.Name, Size: int64(section.Size)})
			if strings.HasPrefix(strings.ToUpper(section.Name), "UPX") {
				bi.Security.UPX = true
			}
		}
		setBinarySectionPercentages(bi.Sections)
		if imports, err := file.ImportedLibraries(); err == nil {
			bi.Imports = binaryUniqueSortedStrings(imports)
		}
		var characteristics uint16
		switch header := file.OptionalHeader.(type) {
		case *pe.OptionalHeader32:
			characteristics = header.DllCharacteristics
		case *pe.OptionalHeader64:
			characteristics = header.DllCharacteristics
		}
		bi.Security.PIE = characteristics&pe.IMAGE_DLLCHARACTERISTICS_DYNAMIC_BASE != 0
		bi.Security.NX = characteristics&pe.IMAGE_DLLCHARACTERISTICS_NX_COMPAT != 0
		bi.Security.Striped = file.Characteristics&pe.IMAGE_FILE_DEBUG_STRIPPED != 0
		return nil
	}

	if file, err := elf.Open(fullPath); err == nil {
		defer file.Close()
		bi.Format = "elf"
		bi.Arch = elfArchitecture(file.Machine)
		bi.Security.PIE = file.Type == elf.ET_DYN
		for _, section := range file.Sections {
			bi.Sections = append(bi.Sections, sectionInfo{Name: section.Name, Size: int64(section.Size)})
			if strings.HasPrefix(strings.ToUpper(section.Name), "UPX") {
				bi.Security.UPX = true
			}
		}
		setBinarySectionPercentages(bi.Sections)
		if imports, err := file.ImportedLibraries(); err == nil {
			bi.Imports = binaryUniqueSortedStrings(imports)
		}
		const (
			ptGNUStack = elf.ProgType(0x6474e551)
			ptGNURelro = elf.ProgType(0x6474e552)
		)
		for _, program := range file.Progs {
			switch program.Type {
			case ptGNUStack:
				bi.Security.NX = program.Flags&elf.PF_X == 0
			case ptGNURelro:
				bi.Security.RELRO = true
			}
		}
		return nil
	}

	if file, err := macho.Open(fullPath); err == nil {
		defer file.Close()
		bi.Format = "macho"
		bi.Arch = machoArchitecture(file.Cpu)
		const machoPIE = uint32(0x200000)
		bi.Security.PIE = file.Flags&machoPIE != 0
		for _, section := range file.Sections {
			bi.Sections = append(bi.Sections, sectionInfo{Name: section.Name, Size: int64(section.Size)})
			if strings.HasPrefix(strings.ToUpper(section.Name), "UPX") {
				bi.Security.UPX = true
			}
		}
		setBinarySectionPercentages(bi.Sections)
		if imports, err := file.ImportedLibraries(); err == nil {
			bi.Imports = binaryUniqueSortedStrings(imports)
		}
		return nil
	}

	return fmt.Errorf("unsupported or unrecognized binary format")
}

func (t *BinaryTool) enrichELFSecurity(fullPath string, bi *binaryInfo) {
	file, err := elf.Open(fullPath)
	if err != nil {
		return
	}
	defer file.Close()
	symbols, err := file.Symbols()
	if err != nil {
		bi.Security.Striped = true
		symbols, _ = file.DynamicSymbols()
	}
	bi.Symbols = len(symbols)
	for _, symbol := range symbols {
		if symbol.Name == "__stack_chk_fail" {
			bi.Security.StackCanary = true
			break
		}
	}
}

func (t *BinaryTool) formatBinaryReport(bi *binaryInfo) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("## Binary Analysis: `%s`\n\n", filepath.Base(bi.Path)))

	// Basic info
	sb.WriteString("### Overview\n\n")
	sb.WriteString(fmt.Sprintf("| Property | Value |\n"))
	sb.WriteString(fmt.Sprintf("|----------|-------|\n"))
	sb.WriteString(fmt.Sprintf("| Size | %s |\n", bi.SizeHuman))
	sb.WriteString(fmt.Sprintf("| Format | %s |\n", bi.Format))
	sb.WriteString(fmt.Sprintf("| Arch | %s |\n", bi.Arch))
	if bi.GoVersion != "" {
		sb.WriteString(fmt.Sprintf("| Go version | %s |\n", bi.GoVersion))
	}
	if bi.Symbols > 0 {
		sb.WriteString(fmt.Sprintf("| Symbols | %d (%d Go runtime) |\n", bi.Symbols, bi.GotSymbols))
	}
	sb.WriteString("\n")

	// Security
	sb.WriteString("### Security\n\n")
	sb.WriteString("| Feature | Status |\n")
	sb.WriteString("|---------|--------|\n")
	sb.WriteString(fmt.Sprintf("| PIE | %s |\n", checkMark(bi.Security.PIE)))
	sb.WriteString(fmt.Sprintf("| NX | %s |\n", checkMark(bi.Security.NX)))
	sb.WriteString(fmt.Sprintf("| Stack Canary | %s |\n", checkMark(bi.Security.StackCanary)))
	sb.WriteString(fmt.Sprintf("| RELRO | %s |\n", checkMark(bi.Security.RELRO)))
	sb.WriteString(fmt.Sprintf("| Stripped | %s |\n", checkMark(bi.Security.Striped)))
	sb.WriteString(fmt.Sprintf("| UPX | %s |\n", checkMark(bi.Security.UPX)))
	sb.WriteString("\n")

	// Security recommendations
	var recommendations []string
	if !bi.Security.PIE {
		recommendations = append(recommendations, "Enable PIE: `-buildmode=pie` for Go, `-fPIE -pie` for C/C++")
	}
	if !bi.Security.Striped && bi.Symbols > 1000 {
		recommendations = append(recommendations, fmt.Sprintf("Strip symbols: `strip %s` to reduce size (~30-50%%)", filepath.Base(bi.Path)))
	}
	if bi.Security.UPX {
		recommendations = append(recommendations, "UPX detected — considered unpacking for debugging or security review")
	}
	if bi.Size > 50*1024*1024 {
		recommendations = append(recommendations, fmt.Sprintf("Large binary (%s). Consider: UPX compression, removing unused dependencies, or splitting into microservices", bi.SizeHuman))
	}

	if len(recommendations) > 0 {
		sb.WriteString("### Recommendations\n\n")
		for _, r := range recommendations {
			sb.WriteString(fmt.Sprintf("- %s\n", r))
		}
		sb.WriteString("\n")
	}

	// Build info for Go binaries
	if len(bi.BuildInfo) > 0 {
		sb.WriteString("### Build Info\n\n")
		keys := make([]string, 0, len(bi.BuildInfo))
		for key := range bi.BuildInfo {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, k := range keys {
			v := bi.BuildInfo[k]
			sb.WriteString(fmt.Sprintf("- **%s:** %s\n", k, v))
		}
		sb.WriteString("\n")
	}

	if len(bi.Imports) > 0 {
		sb.WriteString("### Imported Libraries\n\n")
		shown := bi.Imports
		if len(shown) > 30 {
			shown = shown[:30]
		}
		for _, imported := range shown {
			sb.WriteString(fmt.Sprintf("- %s\n", imported))
		}
		if len(bi.Imports) > len(shown) {
			sb.WriteString(fmt.Sprintf("- ... %d more\n", len(bi.Imports)-len(shown)))
		}
		sb.WriteString("\n")
	}

	// Section sizes
	if len(bi.Sections) > 0 {
		sb.WriteString("### Sections\n\n")
		sb.WriteString("| Section | Size | %% |\n")
		sb.WriteString("|---------|------|----|\n")
		for _, s := range bi.Sections {
			sb.WriteString(fmt.Sprintf("| %s | %s | %.1f%% |\n", s.Name, formatBinarySize(s.Size), s.Pct))
		}
	}

	return sb.String()
}

// ================== Read-only reverse-engineering primitives ==================

type extractedBinaryString struct {
	Offset   int64
	Encoding string
	Value    string
}

func (t *BinaryTool) extractStrings(filePath, contains string, minLength, limit int) (string, error) {
	fullPath := t.resolvePath(filePath)
	info, err := os.Stat(fullPath)
	if err != nil {
		return "", fmt.Errorf("stat binary: %w", err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("binary path is a directory: %s", filePath)
	}

	filter := strings.ToLower(strings.TrimSpace(contains))
	values, matched, err := scanPrintableStrings(fullPath, filter, minLength, limit)
	if err != nil {
		return "", err
	}
	sort.Slice(values, func(i, j int) bool {
		if values[i].Offset == values[j].Offset {
			return values[i].Encoding < values[j].Encoding
		}
		return values[i].Offset < values[j].Offset
	})
	if len(values) > limit {
		values = values[:limit]
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("## Printable Strings: `%s`\n\n", filepath.Base(filePath)))
	sb.WriteString(fmt.Sprintf("Minimum length: %d; matched: %d; showing: %d", minLength, matched, len(values)))
	if contains != "" {
		sb.WriteString(fmt.Sprintf("; filter: %s", strconv.Quote(contains)))
	}
	sb.WriteString("\n\n")
	for _, value := range values {
		sb.WriteString(fmt.Sprintf("0x%08x [%s] %s\n", value.Offset, value.Encoding, strconv.Quote(value.Value)))
	}
	if len(values) == 0 {
		sb.WriteString("No matching printable strings found.\n")
	} else if matched > len(values) {
		sb.WriteString(fmt.Sprintf("\nOutput limited to %d entries. Use `contains` to narrow the result.\n", limit))
	}
	return sb.String(), nil
}

func scanPrintableStrings(path, filter string, minLength, limit int) ([]extractedBinaryString, int, error) {
	results := make([]extractedBinaryString, 0, min(limit*3, 384))
	retainedByEncoding := make(map[string]int, 3)
	matched := 0
	accept := func(value extractedBinaryString) {
		if len(value.Value) < minLength {
			return
		}
		if filter != "" && !strings.Contains(strings.ToLower(value.Value), filter) {
			return
		}
		matched++
		// Retain a bounded prefix for each encoding. The final sort and
		// truncate keep output deterministic without retaining every string
		// from a large binary in memory.
		if retainedByEncoding[value.Encoding] < limit {
			results = append(results, value)
			retainedByEncoding[value.Encoding]++
		}
	}

	if err := scanASCIIStrings(path, accept); err != nil {
		return nil, 0, fmt.Errorf("extract ASCII strings: %w", err)
	}
	if err := scanUTF16Strings(path, accept); err != nil {
		return nil, 0, fmt.Errorf("extract UTF-16 strings: %w", err)
	}
	return results, matched, nil
}

func scanASCIIStrings(path string, accept func(extractedBinaryString)) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()

	reader := bufio.NewReaderSize(file, 64*1024)
	var start int64 = -1
	var value strings.Builder
	var offset int64
	flush := func() {
		if start >= 0 {
			accept(extractedBinaryString{Offset: start, Encoding: "ascii", Value: value.String()})
		}
		start = -1
		value.Reset()
	}
	for {
		current, readErr := reader.ReadByte()
		if readErr != nil {
			if readErr == io.EOF {
				flush()
				return nil
			}
			return readErr
		}
		if current >= 0x20 && current <= 0x7e {
			if start < 0 {
				start = offset
			}
			if value.Len() < 16*1024 {
				value.WriteByte(current)
			}
		} else {
			flush()
		}
		offset++
	}
}

func scanUTF16Strings(path string, accept func(extractedBinaryString)) error {
	for alignment := int64(0); alignment <= 1; alignment++ {
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		if _, err := file.Seek(alignment, io.SeekStart); err != nil {
			file.Close()
			return err
		}
		reader := bufio.NewReaderSize(file, 64*1024)
		offset := alignment
		start := int64(-1)
		encoding := ""
		var value strings.Builder
		flush := func() {
			if start >= 0 {
				accept(extractedBinaryString{Offset: start, Encoding: encoding, Value: value.String()})
			}
			start = -1
			encoding = ""
			value.Reset()
		}
		for {
			first, firstErr := reader.ReadByte()
			if firstErr != nil {
				if firstErr == io.EOF {
					flush()
					break
				}
				file.Close()
				return firstErr
			}
			second, secondErr := reader.ReadByte()
			if secondErr != nil {
				flush()
				if secondErr != io.EOF {
					file.Close()
					return secondErr
				}
				break
			}

			currentEncoding := ""
			var current byte
			switch {
			case first >= 0x20 && first <= 0x7e && second == 0:
				currentEncoding, current = "utf16le", first
			case first == 0 && second >= 0x20 && second <= 0x7e:
				currentEncoding, current = "utf16be", second
			}
			if currentEncoding == "" {
				flush()
			} else {
				if start < 0 || encoding != currentEncoding {
					flush()
					start = offset
					encoding = currentEncoding
				}
				if value.Len() < 16*1024 {
					value.WriteByte(current)
				}
			}
			offset += 2
		}
		file.Close()
	}
	return nil
}

func (t *BinaryTool) listSymbols(ctx context.Context, filePath, contains string, limit int) (string, error) {
	fullPath := t.resolvePath(filePath)
	if info, err := os.Stat(fullPath); err != nil {
		return "", fmt.Errorf("stat binary: %w", err)
	} else if info.IsDir() {
		return "", fmt.Errorf("binary path is a directory: %s", filePath)
	}

	var source string
	var lines []string
	if _, err := buildinfo.ReadFile(fullPath); err == nil {
		output, commandErr := runBinaryCmdContext(ctx, "go", "tool", "nm", "-size", fullPath)
		if commandErr != nil {
			return "", fmt.Errorf("read Go symbol table: %w: %s", commandErr, strings.TrimSpace(output))
		}
		source = "go tool nm -size"
		lines = nonEmptyBinaryLines(output)
	} else {
		source, lines = nativeSymbolLines(fullPath)
	}

	filter := strings.ToLower(strings.TrimSpace(contains))
	matches := make([]string, 0, min(limit, len(lines)))
	matched := 0
	for _, line := range lines {
		if filter != "" && !strings.Contains(strings.ToLower(line), filter) {
			continue
		}
		matched++
		if len(matches) < limit {
			matches = append(matches, line)
		}
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("## Symbols: `%s`\n\n", filepath.Base(filePath)))
	sb.WriteString(fmt.Sprintf("Source: %s; matched: %d; showing: %d", source, matched, len(matches)))
	if contains != "" {
		sb.WriteString(fmt.Sprintf("; filter: %s", strconv.Quote(contains)))
	}
	sb.WriteString("\n\n")
	for _, line := range matches {
		sb.WriteString(line)
		sb.WriteByte('\n')
	}
	if len(matches) == 0 {
		sb.WriteString("No matching symbols found. The binary may be stripped or the filter may be too narrow.\n")
	} else if matched > len(matches) {
		sb.WriteString(fmt.Sprintf("\nOutput limited to %d entries. Use `contains` to narrow the result.\n", limit))
	}
	return sb.String(), nil
}

func nativeSymbolLines(fullPath string) (string, []string) {
	if file, err := pe.Open(fullPath); err == nil {
		defer file.Close()
		lines := make([]string, 0, len(file.Symbols))
		for _, symbol := range file.Symbols {
			lines = append(lines, fmt.Sprintf("%08x section=%d type=%04x %s",
				symbol.Value, symbol.SectionNumber, symbol.Type, symbol.Name))
		}
		return "PE COFF symbol table", lines
	}
	if file, err := elf.Open(fullPath); err == nil {
		defer file.Close()
		symbols, err := file.Symbols()
		source := "ELF symbol table"
		if err != nil {
			symbols, _ = file.DynamicSymbols()
			source = "ELF dynamic symbol table"
		}
		lines := make([]string, 0, len(symbols))
		for _, symbol := range symbols {
			lines = append(lines, fmt.Sprintf("%016x size=%d %s", symbol.Value, symbol.Size, symbol.Name))
		}
		return source, lines
	}
	if file, err := macho.Open(fullPath); err == nil {
		defer file.Close()
		if file.Symtab == nil {
			return "Mach-O symbol table", nil
		}
		lines := make([]string, 0, len(file.Symtab.Syms))
		for _, symbol := range file.Symtab.Syms {
			lines = append(lines, fmt.Sprintf("%016x type=%02x section=%d %s",
				symbol.Value, symbol.Type, symbol.Sect, symbol.Name))
		}
		return "Mach-O symbol table", lines
	}
	return "unrecognized symbol table", nil
}

func (t *BinaryTool) disassemble(ctx context.Context, filePath, symbol string, maxLines int) (string, error) {
	fullPath := t.resolvePath(filePath)
	if info, err := os.Stat(fullPath); err != nil {
		return "", fmt.Errorf("stat binary: %w", err)
	} else if info.IsDir() {
		return "", fmt.Errorf("binary path is a directory: %s", filePath)
	}
	symbol = strings.TrimSpace(symbol)
	if symbol == "" {
		return "", fmt.Errorf("'symbol' is required for focused disassembly; use action=symbols with contains first")
	}
	if len(symbol) > 256 {
		return "", fmt.Errorf("'symbol' must be at most 256 characters")
	}

	var source string
	var output string
	var commandErr error
	if _, err := buildinfo.ReadFile(fullPath); err == nil {
		source = "go tool objdump"
		output, commandErr = runBinaryCmdContext(ctx, "go", "tool", "objdump", "-s", symbol, fullPath)
	} else {
		objdumpPath, lookupErr := exec.LookPath("objdump")
		if lookupErr != nil {
			return "", fmt.Errorf("focused disassembly for this non-Go binary requires objdump in PATH")
		}
		source = filepath.Base(objdumpPath)
		output, commandErr = runBinaryCmdContext(ctx, objdumpPath, "-d", "--disassemble="+symbol, fullPath)
	}
	if commandErr != nil {
		return "", fmt.Errorf("%s failed: %w: %s", source, commandErr, strings.TrimSpace(output))
	}

	lines := annotateDisassemblyImmediates(nonEmptyBinaryLines(output))
	if len(lines) == 0 {
		return "", fmt.Errorf("no disassembly matched symbol expression %q; use action=symbols to find the exact name", symbol)
	}
	totalLines := len(lines)
	if len(lines) > maxLines {
		lines = lines[:maxLines]
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("## Disassembly: `%s` in `%s`\n\n", symbol, filepath.Base(filePath)))
	sb.WriteString(fmt.Sprintf("Source: %s; lines: %d; showing: %d\n\n", source, totalLines, len(lines)))
	sb.WriteString(strings.Join(lines, "\n"))
	sb.WriteByte('\n')
	if totalLines > len(lines) {
		sb.WriteString(fmt.Sprintf("\nOutput limited to %d lines. Narrow `symbol` or increase `max_lines`.\n", maxLines))
	}
	return sb.String(), nil
}

var x86MoveImmediatePattern = regexp.MustCompile(`(?i)\bMOV([BWLQ])\s+\$(-?0x[0-9a-f]+)\b`)

func annotateDisassemblyImmediates(lines []string) []string {
	annotated := make([]string, len(lines))
	for index, line := range lines {
		match := x86MoveImmediatePattern.FindStringSubmatch(line)
		if len(match) != 3 {
			annotated[index] = line
			continue
		}
		bits := map[string]int{"B": 8, "W": 16, "L": 32, "Q": 64}[strings.ToUpper(match[1])]
		unsigned, littleEndian, ok := normalizeDisassemblyImmediate(match[2], bits)
		if !ok {
			annotated[index] = line
			continue
		}
		annotated[index] = fmt.Sprintf("%s\t; imm%d=%s bytes_le=%s", line, bits, unsigned, littleEndian)
	}
	return annotated
}

func normalizeDisassemblyImmediate(value string, bits int) (string, string, bool) {
	if bits != 8 && bits != 16 && bits != 32 && bits != 64 {
		return "", "", false
	}
	negative := strings.HasPrefix(value, "-")
	magnitudeText := strings.TrimPrefix(value, "-")
	magnitudeText = strings.TrimPrefix(strings.ToLower(magnitudeText), "0x")
	magnitude := new(big.Int)
	if _, ok := magnitude.SetString(magnitudeText, 16); !ok {
		return "", "", false
	}
	modulus := new(big.Int).Lsh(big.NewInt(1), uint(bits))
	unsigned := new(big.Int).Mod(magnitude, modulus)
	if negative && unsigned.Sign() != 0 {
		unsigned.Sub(modulus, unsigned)
	}
	byteCount := bits / 8
	bigEndian := make([]byte, byteCount)
	unsigned.FillBytes(bigEndian)
	littleEndian := make([]string, byteCount)
	for index := range bigEndian {
		littleEndian[index] = fmt.Sprintf("%02x", bigEndian[byteCount-1-index])
	}
	return fmt.Sprintf("0x%0*x", bits/4, unsigned), strings.Join(littleEndian, " "), true
}

func (t *BinaryTool) runBinary(
	ctx context.Context,
	filePath string,
	args []string,
	stdin string,
	workDirParam string,
	timeout time.Duration,
) (string, error) {
	fullPath := t.resolvePath(filePath)
	info, err := os.Stat(fullPath)
	if err != nil {
		return "", fmt.Errorf("stat binary: %w", err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("binary path is a directory: %s", filePath)
	}
	workDir := t.workspace
	if strings.TrimSpace(workDirParam) != "" {
		workDir, err = resolveBinaryWorkDir(t.workspace, workDirParam)
		if err != nil {
			return "", fmt.Errorf("invalid run work_dir: %w", err)
		}
	}
	if ctx == nil {
		ctx = context.Background()
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	command := exec.CommandContext(runCtx, fullPath, args...)
	command.Dir = workDir
	if stdin != "" {
		command.Stdin = strings.NewReader(stdin)
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	started := time.Now()
	runErr := processutil.RunTree(command, true)
	duration := time.Since(started)
	if runCtx.Err() != nil {
		return "", fmt.Errorf("binary execution stopped after %s: %w", duration.Round(time.Millisecond), runCtx.Err())
	}
	if runErr != nil {
		var exitErr *exec.ExitError
		if !errors.As(runErr, &exitErr) {
			return "", fmt.Errorf("start binary: %w", runErr)
		}
	}

	exitCode := 0
	if command.ProcessState != nil {
		exitCode = command.ProcessState.ExitCode()
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("## Binary Run: `%s`\n\n", filepath.Base(filePath)))
	sb.WriteString(fmt.Sprintf("- Exit code: %d\n", exitCode))
	sb.WriteString(fmt.Sprintf("- Duration: %s\n", duration.Round(time.Millisecond)))
	sb.WriteString(fmt.Sprintf("- Working directory: %s\n", workDir))
	if len(args) > 0 {
		quoted := make([]string, len(args))
		for i, arg := range args {
			quoted[i] = strconv.Quote(arg)
		}
		sb.WriteString(fmt.Sprintf("- Arguments: %s\n", strings.Join(quoted, " ")))
	}
	sb.WriteString("\n### stdout\n\n")
	sb.WriteString(formatBinaryProcessStream(stdout.String()))
	sb.WriteString("\n\n### stderr\n\n")
	sb.WriteString(formatBinaryProcessStream(stderr.String()))
	sb.WriteByte('\n')
	return sb.String(), nil
}

func formatBinaryProcessStream(value string) string {
	const maximum = 12000
	value = strings.TrimRight(value, "\r\n")
	if value == "" {
		return "(empty)"
	}
	if len(value) <= maximum {
		return value
	}
	return value[:maximum] + fmt.Sprintf("\n... [truncated %d bytes]", len(value)-maximum)
}

// ================== Compare ==================

func (t *BinaryTool) compareBinaries(file1, file2 string) (string, error) {
	if strings.TrimSpace(file2) == "" {
		return "", fmt.Errorf("'file2' is required for compare")
	}
	info1, err := os.Stat(t.resolvePath(file1))
	if err != nil {
		return "", fmt.Errorf("stat %s: %w", file1, err)
	}
	info2, err := os.Stat(t.resolvePath(file2))
	if err != nil {
		return "", fmt.Errorf("stat %s: %w", file2, err)
	}

	sizeDelta := info2.Size() - info1.Size()
	deltaIcon := "📈"
	if sizeDelta < 0 {
		deltaIcon = "📉"
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("## Binary Comparison\n\n"))
	sb.WriteString(fmt.Sprintf("| Property | `%s` | `%s` | Delta |\n", file1, file2))
	sb.WriteString(fmt.Sprintf("|----------|-------|-------|-------|\n"))
	sb.WriteString(fmt.Sprintf("| Size | %s | %s | %s %+d |\n",
		formatBinarySize(info1.Size()), formatBinarySize(info2.Size()), deltaIcon, sizeDelta))
	sb.WriteString(fmt.Sprintf("| Modified | %s | %s | |\n",
		info1.ModTime().Format("2006-01-02 15:04"), info2.ModTime().Format("2006-01-02 15:04")))

	if info1.Size() == 0 {
		sb.WriteString("\nThe original file is empty; a percentage size change is undefined.\n")
	} else if pct := float64(info2.Size())/float64(info1.Size())*100 - 100; pct > 0 {
		sb.WriteString(fmt.Sprintf("\nThe new binary is **%.1f%% larger**. Consider:\n", pct))
		sb.WriteString("- Check for unintentionally included packages\n")
		sb.WriteString("- Verify build flags are consistent between versions\n")
		sb.WriteString("- Use `binary strip` to reduce size\n")
	} else if pct < 0 {
		sb.WriteString(fmt.Sprintf("\nThe new binary is **%.1f%% smaller**. 🎉\n", -pct))
	}

	return sb.String(), nil
}

// ================== Strip ==================

func (t *BinaryTool) stripBinary(ctx context.Context, filePath, output string) (string, error) {
	fullPath := t.resolvePath(filePath)
	info1, err := os.Stat(fullPath)
	if err != nil {
		return "", fmt.Errorf("stat: %w", err)
	}
	if info1.IsDir() {
		return "", fmt.Errorf("binary path is a directory: %s", filePath)
	}

	if output == "" {
		output = filePath + ".stripped"
	}
	outputPath := t.resolvePath(output)
	if samePath(fullPath, outputPath) {
		return "", fmt.Errorf("strip output must differ from the input binary")
	}
	if _, statErr := os.Stat(outputPath); statErr == nil {
		return "", fmt.Errorf("strip output already exists: %s", output)
	} else if !os.IsNotExist(statErr) {
		return "", fmt.Errorf("stat strip output: %w", statErr)
	}

	bi := &binaryInfo{}
	if err := inspectNativeBinary(fullPath, bi); err != nil {
		return "", fmt.Errorf("detect binary format: %w", err)
	}
	stripProgram, err := findStripProgram()
	if err != nil {
		return "", err
	}
	outputDir := filepath.Dir(outputPath)
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return "", fmt.Errorf("create strip output directory: %w", err)
	}
	tempFile, err := os.CreateTemp(outputDir, "."+filepath.Base(outputPath)+".tmp-*")
	if err != nil {
		return "", fmt.Errorf("create temporary strip output: %w", err)
	}
	tempPath := tempFile.Name()
	cleanupTemp := true
	defer func() {
		tempFile.Close()
		if cleanupTemp {
			_ = os.Remove(tempPath)
		}
	}()

	source, err := os.Open(fullPath)
	if err != nil {
		return "", fmt.Errorf("open binary: %w", err)
	}
	_, copyErr := io.Copy(tempFile, source)
	closeSourceErr := source.Close()
	closeTempErr := tempFile.Close()
	if copyErr != nil {
		return "", fmt.Errorf("copy binary for stripping: %w", copyErr)
	}
	if closeSourceErr != nil {
		return "", fmt.Errorf("close input binary: %w", closeSourceErr)
	}
	if closeTempErr != nil {
		return "", fmt.Errorf("close temporary binary: %w", closeTempErr)
	}
	if err := os.Chmod(tempPath, info1.Mode()); err != nil {
		return "", fmt.Errorf("preserve binary mode: %w", err)
	}

	args := []string{"--strip-all", tempPath}
	if bi.Format == "macho" {
		args = []string{"-S", tempPath}
	}
	commandOutput, commandErr := runBinaryCmdContext(ctx, stripProgram, args...)
	if commandErr != nil {
		return "", fmt.Errorf("strip binary: %w: %s", commandErr, strings.TrimSpace(commandOutput))
	}
	if err := os.Rename(tempPath, outputPath); err != nil {
		return "", fmt.Errorf("publish stripped binary: %w", err)
	}
	cleanupTemp = false

	info2, err := os.Stat(outputPath)
	if err != nil {
		return "", fmt.Errorf("stat stripped binary: %w", err)
	}
	saved := info1.Size() - info2.Size()
	savedPct := float64(saved) / float64(info1.Size()) * 100

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("## Binary Stripped\n\n"))
	sb.WriteString(fmt.Sprintf("**Original:** %s (%s)\n", filePath, formatBinarySize(info1.Size())))
	sb.WriteString(fmt.Sprintf("**Stripped:** %s (%s)\n", output, formatBinarySize(info2.Size())))
	sb.WriteString(fmt.Sprintf("**Saved:** %s (%.1f%%)\n", formatBinarySize(saved), savedPct))
	sb.WriteString(fmt.Sprintf("\n✅ Stripped binary: `%s`\n", output))

	return sb.String(), nil
}

// ================== Helpers ==================

func (t *BinaryTool) resolvePath(filePath string) string {
	if filepath.IsAbs(filePath) {
		return filepath.Clean(filePath)
	}
	return filepath.Join(t.workspace, filepath.Clean(filePath))
}

func formatBinarySize(size int64) string {
	switch {
	case size < 1024:
		return fmt.Sprintf("%d B", size)
	case size < 1024*1024:
		return fmt.Sprintf("%.1f KB", float64(size)/1024)
	case size < 1024*1024*1024:
		return fmt.Sprintf("%.1f MB", float64(size)/(1024*1024))
	default:
		return fmt.Sprintf("%.2f GB", float64(size)/(1024*1024*1024))
	}
}

func resolveBinaryWorkDir(workspace, requested string) (string, error) {
	requested = strings.TrimSpace(requested)
	if portablepath.IsAbs(requested) {
		if filepath.IsAbs(requested) {
			return filepath.Abs(filepath.Clean(requested))
		}
		return requested, nil
	}
	base, err := filepath.Abs(workspace)
	if err != nil {
		return "", err
	}
	resolved := filepath.Clean(filepath.Join(base, requested))
	relative, err := filepath.Rel(base, resolved)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q escapes the workspace", requested)
	}
	return resolved, nil
}

func samePath(first, second string) bool {
	first = filepath.Clean(first)
	second = filepath.Clean(second)
	if filepath.Separator == '\\' {
		return strings.EqualFold(first, second)
	}
	return first == second
}

func findStripProgram() (string, error) {
	for _, candidate := range []string{"llvm-strip", "strip"} {
		if path, err := exec.LookPath(candidate); err == nil {
			return path, nil
		}
	}
	return "", fmt.Errorf("strip requires llvm-strip or strip in PATH; the input was left unchanged")
}

func clampBinaryInt(value, minimum, maximum int) int {
	if value < minimum {
		return minimum
	}
	if value > maximum {
		return maximum
	}
	return value
}

func binaryStringSlice(value interface{}) []string {
	switch items := value.(type) {
	case []string:
		return append([]string(nil), items...)
	case []interface{}:
		result := make([]string, 0, len(items))
		for _, item := range items {
			text, ok := item.(string)
			if ok {
				result = append(result, text)
			}
		}
		return result
	default:
		return nil
	}
}

func nonEmptyBinaryLines(output string) []string {
	rawLines := strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n")
	lines := make([]string, 0, len(rawLines))
	for _, line := range rawLines {
		if strings.TrimSpace(line) != "" {
			lines = append(lines, strings.TrimRight(line, "\r"))
		}
	}
	return lines
}

func binaryUniqueSortedStrings(values []string) []string {
	seen := make(map[string]bool, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func setBinarySectionPercentages(sections []sectionInfo) {
	var total int64
	for _, section := range sections {
		total += section.Size
	}
	if total <= 0 {
		return
	}
	for i := range sections {
		sections[i].Pct = float64(sections[i].Size) / float64(total) * 100
	}
}

func peArchitecture(machine uint16) string {
	switch machine {
	case pe.IMAGE_FILE_MACHINE_AMD64:
		return "amd64"
	case pe.IMAGE_FILE_MACHINE_ARM64:
		return "arm64"
	case pe.IMAGE_FILE_MACHINE_I386:
		return "386"
	case pe.IMAGE_FILE_MACHINE_ARM:
		return "arm"
	default:
		return fmt.Sprintf("machine-0x%x", machine)
	}
}

func elfArchitecture(machine elf.Machine) string {
	switch machine {
	case elf.EM_X86_64:
		return "amd64"
	case elf.EM_AARCH64:
		return "arm64"
	case elf.EM_386:
		return "386"
	case elf.EM_ARM:
		return "arm"
	default:
		return strings.ToLower(machine.String())
	}
}

func machoArchitecture(cpu macho.Cpu) string {
	switch cpu {
	case macho.CpuAmd64:
		return "amd64"
	case macho.CpuArm64:
		return "arm64"
	case macho.Cpu386:
		return "386"
	case macho.CpuArm:
		return "arm"
	default:
		return strings.ToLower(cpu.String())
	}
}

func checkMark(yes bool) string {
	if yes {
		return "✅"
	}
	return "❌"
}

func runBinaryCmdContext(ctx context.Context, name string, args ...string) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}
