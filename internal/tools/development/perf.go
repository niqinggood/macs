package development

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"mac/internal/codeutil"
	"mac/internal/configvalue"
	"mac/internal/textutil"
)

// PerfTool provides project performance profiling and benchmarking.
type PerfTool struct {
	workspace string
}

func NewPerfTool(workspace string) *PerfTool { return &PerfTool{workspace: workspace} }

func (tool *PerfTool) Name() string { return "perf" }

func (tool *PerfTool) Description() string {
	return "Performance profiling and benchmarking. " +
		"Actions: bench (run benchmarks), profile (CPU/memory profiling), " +
		"analyze (analyze profile output), compare (compare two benchmarks). " +
		"Supports: Go (testing.B + pprof), Python (cProfile), Node (clinic)."
}

func (tool *PerfTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action": map[string]interface{}{
				"type":        "string",
				"enum":        []string{"bench", "profile", "analyze", "compare"},
				"description": "bench: run benchmarks. profile: run with profiling. analyze: analyze results. compare: compare two runs.",
			},
			"target": map[string]interface{}{
				"type":        "string",
				"description": "Target package or test for bench/profile (e.g., './pkg/...' or 'BenchmarkFoo')",
			},
			"profile_type": map[string]interface{}{
				"type":        "string",
				"enum":        []string{"cpu", "mem", "block", "mutex", "trace"},
				"description": "Type of profile to collect (for profile action). Default: cpu.",
			},
			"bench_cmd": map[string]interface{}{
				"type":        "string",
				"description": "Custom benchmark command (overrides auto-detection)",
			},
			"output": map[string]interface{}{
				"type":        "string",
				"description": "Output file for profile data",
			},
		},
		"required": []string{"action"},
	}
}

func (tool *PerfTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	action := configvalue.String(params, "action", "bench")
	target := configvalue.String(params, "target", "./...")
	profileType := configvalue.String(params, "profile_type", "cpu")
	benchmarkCommand := configvalue.String(params, "bench_cmd", "")
	output := configvalue.String(params, "output", "")
	switch action {
	case "bench":
		return tool.runBenchmark(ctx, target, benchmarkCommand)
	case "profile":
		return tool.runProfile(ctx, target, profileType, output)
	case "analyze":
		return tool.analyzeResults(ctx)
	case "compare":
		return "", fmt.Errorf("compare action not yet implemented — run bench twice and compare manually")
	default:
		return "", fmt.Errorf("unknown action: %s", action)
	}
}

type BenchResult struct {
	Name        string
	Ops         int
	NsPerOp     float64
	BytesPerOp  int64
	AllocsPerOp int64
	MBPerSec    float64
}

func (tool *PerfTool) runBenchmark(ctx context.Context, target, benchmarkCommand string) (string, error) {
	language := codeutil.DetectProjectLanguage(tool.workspace)
	if benchmarkCommand != "" {
		output, _ := runPerfCommand(ctx, tool.workspace, 300, "sh", "-c", benchmarkCommand)
		return fmt.Sprintf("## Benchmark Results\n\nCommand: `%s`\n\n```\n%s\n```\n", benchmarkCommand, textutil.Truncate(output, 3000)), nil
	}
	switch language {
	case "Go":
		return tool.goBenchmark(ctx, target)
	case "Python":
		return tool.pythonBenchmark(ctx, target)
	case "Node":
		return tool.nodeBenchmark(ctx)
	case "Rust":
		return tool.rustBenchmark(ctx)
	default:
		return "No benchmark support for detected language. Use bench_cmd parameter for custom commands.", nil
	}
}

func (tool *PerfTool) goBenchmark(ctx context.Context, target string) (string, error) {
	started := time.Now()
	output, benchmarkErr := runPerfCommand(ctx, tool.workspace, 300, "go", "test", "-bench="+target, "-benchmem", "-count=3", "./...")
	duration := time.Since(started)
	if benchmarkErr != nil {
		return fmt.Sprintf("## Benchmark Results\n\n❌ Benchmark failed (%.1fs):\n```\n%s\n```\n", duration.Seconds(), textutil.Truncate(output, 2000)), nil
	}
	results := ParseGoBenchOutput(output)
	var report strings.Builder
	report.WriteString(fmt.Sprintf("## Benchmark Results (%.1fs)\n\n", duration.Seconds()))
	report.WriteString("| Benchmark | Ops | ns/op | B/op | allocs/op |\n")
	report.WriteString("|-----------|-----|-------|------|----------|\n")
	for _, result := range results {
		report.WriteString(fmt.Sprintf("| `%s` | %d | %.1f | %d | %d |\n",
			result.Name, result.Ops, result.NsPerOp, result.BytesPerOp, result.AllocsPerOp))
	}
	if len(results) == 0 {
		report.WriteString("| (no benchmarks found) |\n")
	}
	report.WriteString("\n### Raw Output\n```\n")
	report.WriteString(textutil.Truncate(output, 2000))
	report.WriteString("\n```\n")
	report.WriteString(benchmarkTips(results))
	return report.String(), nil
}

func (tool *PerfTool) pythonBenchmark(ctx context.Context, target string) (string, error) {
	command := fmt.Sprintf("python -m pytest --benchmark-only %s -v 2>/dev/null || python -c \"import timeit; print(timeit.timeit('main()', setup='from __main__ import main', number=1000))\"", target)
	output, _ := runPerfCommand(ctx, tool.workspace, 120, "sh", "-c", command)
	return fmt.Sprintf("## Python Benchmark\n\n```\n%s\n```\n", textutil.Truncate(output, 3000)), nil
}

func (tool *PerfTool) nodeBenchmark(ctx context.Context) (string, error) {
	command := "npx jest --testPathPattern='bench|perf' --verbose 2>/dev/null || echo 'No benchmark tests found'"
	output, _ := runPerfCommand(ctx, tool.workspace, 120, "sh", "-c", command)
	return fmt.Sprintf("## Node Benchmark\n\n```\n%s\n```\n", textutil.Truncate(output, 3000)), nil
}

func (tool *PerfTool) rustBenchmark(ctx context.Context) (string, error) {
	output, _ := runPerfCommand(ctx, tool.workspace, 300, "cargo", "bench")
	return fmt.Sprintf("## Rust Benchmark\n\n```\n%s\n```\n", textutil.Truncate(output, 3000)), nil
}

func (tool *PerfTool) runProfile(ctx context.Context, target, profileType, output string) (string, error) {
	switch language := codeutil.DetectProjectLanguage(tool.workspace); language {
	case "Go":
		return tool.goProfile(ctx, target, profileType, output)
	case "Python":
		return tool.pythonProfile(ctx, target, output)
	default:
		return fmt.Sprintf("Profiling not supported for %s. Use bench_cmd for custom profiling commands.", language), nil
	}
}

func (tool *PerfTool) goProfile(ctx context.Context, target, profileType, output string) (string, error) {
	profileDirectory := filepath.Join(tool.workspace, ".openkitty", "storage", "profiles")
	if err := os.MkdirAll(profileDirectory, 0755); err != nil {
		return "", fmt.Errorf("create profile directory: %w", err)
	}
	profileFile := filepath.Join(profileDirectory, fmt.Sprintf("%s_%s.prof", profileType, time.Now().Format("20060102_150405")))
	if output != "" {
		profileFile = output
	}
	var arguments []string
	switch profileType {
	case "cpu":
		arguments = []string{"test", "-bench=" + target, "-benchmem", "-cpuprofile=" + profileFile, "-benchtime=5s", "./..."}
	case "mem":
		arguments = []string{"test", "-bench=" + target, "-benchmem", "-memprofile=" + profileFile, "-benchtime=5s", "./..."}
	case "block":
		arguments = []string{"test", "-bench=" + target, "-blockprofile=" + profileFile, "./..."}
	case "mutex":
		arguments = []string{"test", "-bench=" + target, "-mutexprofile=" + profileFile, "./..."}
	case "trace":
		profileFile = strings.Replace(profileFile, ".prof", ".trace", 1)
		arguments = []string{"test", "-bench=" + target, "-trace=" + profileFile, "./..."}
	default:
		arguments = []string{"test", "-bench=" + target, "-cpuprofile=" + profileFile, "-benchtime=5s", "./..."}
	}
	commandOutput, profileErr := runPerfCommand(ctx, tool.workspace, 300, "go", arguments...)
	var report strings.Builder
	report.WriteString(fmt.Sprintf("## Profile: %s\n\n", profileType))
	report.WriteString(fmt.Sprintf("**Profile saved:** `%s`\n\n", profileFile))
	if profileErr != nil {
		report.WriteString(fmt.Sprintf("⚠️ Profile collection had errors:\n```\n%s\n```\n", textutil.Truncate(commandOutput, 1000)))
	} else {
		report.WriteString("✅ Profile collected successfully.\n\n")
	}
	report.WriteString("### Analysis Commands\n\n")
	if strings.HasSuffix(profileFile, ".prof") {
		report.WriteString(fmt.Sprintf("```bash\ngo tool pprof -top %s\ngo tool pprof -http=:8080 %s\n```\n", profileFile, profileFile))
	} else if strings.HasSuffix(profileFile, ".trace") {
		report.WriteString(fmt.Sprintf("```bash\ngo tool trace %s\n```\n", profileFile))
	}
	return report.String(), nil
}

func (tool *PerfTool) pythonProfile(ctx context.Context, target, output string) (string, error) {
	profileDirectory := filepath.Join(tool.workspace, ".openkitty", "storage", "profiles")
	if err := os.MkdirAll(profileDirectory, 0755); err != nil {
		return "", fmt.Errorf("create profile directory: %w", err)
	}
	profileFile := filepath.Join(profileDirectory, fmt.Sprintf("python_%s.prof", time.Now().Format("20060102_150405")))
	if output != "" {
		profileFile = output
	}
	command := fmt.Sprintf("python -m cProfile -o %s %s 2>&1", profileFile, target)
	commandOutput, _ := runPerfCommand(ctx, tool.workspace, 120, "sh", "-c", command)
	return fmt.Sprintf("## Python Profile\n\n**Profile saved:** `%s`\n\n```\n%s\n```\n\nAnalyze with:\n```bash\npython -m pstats %s\n```\n", profileFile, textutil.Truncate(commandOutput, 1000), profileFile), nil
}

func (tool *PerfTool) analyzeResults(ctx context.Context) (string, error) {
	profileDirectory := filepath.Join(tool.workspace, ".openkitty", "storage", "profiles")
	entries, err := os.ReadDir(profileDirectory)
	if err != nil {
		return fmt.Sprintf("No profiles found in %s. Run 'profile' action first.", profileDirectory), nil
	}
	var latest string
	var latestTime time.Time
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, infoErr := entry.Info()
		if infoErr == nil && info.ModTime().After(latestTime) {
			latestTime = info.ModTime()
			latest = entry.Name()
		}
	}
	if latest == "" {
		return "No profile files found.", nil
	}
	profilePath := filepath.Join(profileDirectory, latest)
	if strings.HasSuffix(latest, ".prof") && !strings.HasSuffix(latest, ".trace") {
		output, analysisErr := runPerfCommand(ctx, tool.workspace, 30, "go", "tool", "pprof", "-top", "-nodecount=20", profilePath)
		var report strings.Builder
		report.WriteString(fmt.Sprintf("## Profile Analysis: `%s`\n\n", latest))
		report.WriteString(fmt.Sprintf("**Collected:** %s\n\n", latestTime.Format("2006-01-02 15:04:05")))
		if analysisErr != nil {
			report.WriteString(fmt.Sprintf("```\n%s\n```\n", textutil.Truncate(output, 2000)))
		} else {
			report.WriteString("### Top 20 Hot Functions\n\n```\n")
			report.WriteString(textutil.Truncate(output, 3000))
			report.WriteString("\n```\n")
		}
		report.WriteString(fmt.Sprintf("\nInteractive: `go tool pprof -http=:8080 %s`\n", profilePath))
		return report.String(), nil
	}
	return fmt.Sprintf("Latest profile: `%s` (%s) — use `go tool pprof` to analyze", latest, latestTime.Format("2006-01-02 15:04:05")), nil
}

func ParseGoBenchOutput(output string) []BenchResult {
	var results []BenchResult
	for _, line := range strings.Split(output, "\n") {
		if !strings.HasPrefix(line, "Benchmark") {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) < 3 {
			continue
		}
		result := BenchResult{Name: parts[0]}
		fmt.Sscanf(parts[1], "%d", &result.Ops)
		for index, part := range parts {
			switch {
			case part == "ns/op" && index > 0:
				fmt.Sscanf(parts[index-1], "%f", &result.NsPerOp)
			case part == "B/op" && index > 0:
				fmt.Sscanf(parts[index-1], "%d", &result.BytesPerOp)
			case part == "allocs/op" && index > 0:
				fmt.Sscanf(parts[index-1], "%d", &result.AllocsPerOp)
			}
		}
		results = append(results, result)
	}
	return results
}

func benchmarkTips(results []BenchResult) string {
	var tips []string
	for _, result := range results {
		if result.AllocsPerOp > 100 {
			tips = append(tips, fmt.Sprintf("`%s`: High allocations (%d/op) — consider object pooling or pre-allocation", result.Name, result.AllocsPerOp))
		}
		if result.NsPerOp > 1_000_000 {
			tips = append(tips, fmt.Sprintf("`%s`: Slow (%.2f µs/op) — consider caching or algorithmic improvement", result.Name, result.NsPerOp/1000))
		}
		if result.BytesPerOp > 1024*1024 {
			tips = append(tips, fmt.Sprintf("`%s`: High memory (%.1f MB/op) — consider streaming or chunking", result.Name, float64(result.BytesPerOp)/(1024*1024)))
		}
	}
	if len(tips) == 0 {
		return ""
	}
	var result strings.Builder
	result.WriteString("### Performance Tips\n\n")
	for _, tip := range tips {
		result.WriteString(fmt.Sprintf("- %s\n", tip))
	}
	result.WriteString("\n")
	return result.String()
}

func RunPerfCommand(directory string, timeoutSeconds int, name string, arguments ...string) (string, error) {
	return runPerfCommand(context.Background(), directory, timeoutSeconds, name, arguments...)
}

func runPerfCommand(ctx context.Context, directory string, timeoutSeconds int, name string, arguments ...string) (string, error) {
	if timeoutSeconds > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(timeoutSeconds)*time.Second)
		defer cancel()
	}
	command := exec.CommandContext(ctx, name, arguments...)
	command.Dir = directory
	output, err := command.CombinedOutput()
	return string(output), err
}
