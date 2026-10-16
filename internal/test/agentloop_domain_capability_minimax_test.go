package app

import (
	"context"
	crand "crypto/rand"
	"crypto/sha256"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	mrand "math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"mac/internal/textutil"
)

// These are local, deterministic capability probes. They are intentionally
// gated because they call a real model and create evidence outside the normal
// Go test temp tree when OPENKITTY_DOMAIN_EVAL_ARTIFACT_DIR is set.
func TestDomainCapabilityMiniMax(t *testing.T) {
	if strings.TrimSpace(os.Getenv("OPENKITTY_DOMAIN_EVAL")) != "1" {
		t.Skip("set OPENKITTY_DOMAIN_EVAL=1 to run the MiniMax domain capability batch")
	}
	if strings.TrimSpace(os.Getenv("MINIMAX_API_KEY")) == "" {
		t.Fatal("MINIMAX_API_KEY is required through the process environment")
	}

	t.Run("data-modeling", testDomainDataModeling)
	t.Run("reverse-engineering", testDomainReverseEngineering)
}

type domainEvalRun struct {
	Agent     *LLMAgent
	Context   context.Context
	Workspace string
}

type domainEvalArtifact struct {
	Case         string                 `json:"case"`
	Prompt       string                 `json:"prompt"`
	Answer       string                 `json:"answer,omitempty"`
	DurationMS   int64                  `json:"duration_ms"`
	Error        string                 `json:"error,omitempty"`
	Verification map[string]interface{} `json:"verification,omitempty"`
}

const domainEvalSystemMessage = `You are OpenKitty's general-purpose local task agent.

Work only inside the current workspace and complete the user's real deliverables.
- Inspect the supplied files before deciding how to solve the task.
- Do not use the network and do not ask the user for information that is already available locally.
- Input fixtures are immutable. Create or edit only the requested output artifacts.
- Use the available local tools and runtime; do not merely describe commands that should be run.
- Validate generated artifacts and report concrete evidence. Never claim a file or verification result that does not exist.
- Prefer a maintainable, reproducible solution over a one-off manual answer.
- Finish with a concise summary of artifacts and verification performed.`

func newDomainEvalAgent(
	t *testing.T,
	caseName string,
	workspace string,
	enabledTools []string,
	readOnlyPaths []string,
	timeout time.Duration,
) domainEvalRun {
	t.Helper()
	apiKey := strings.TrimSpace(os.Getenv("MINIMAX_API_KEY"))
	modelConfig := ModelConfig{
		Type:         "minimax",
		APIKey:       apiKey,
		ModelName:    envOrDefault("OPENKITTY_DOMAIN_EVAL_MODEL", "MiniMax-M3"),
		BaseURL:      envOrDefault("OPENKITTY_DOMAIN_EVAL_BASE_URL", "https://api.minimaxi.com/v1"),
		Temperature:  0.1,
		MaxTokens:    12288,
		ThinkingMode: "disabled",
	}
	globalConfig := &Config{
		Workspace:                  workspace,
		ModelConfig:                map[string]ModelConfig{"domain-eval-minimax": modelConfig},
		PermissionMode:             "bypassPermissions",
		DangerouslySkipPermissions: true,
	}
	globalConfig.MemoryConfig.Enabled = false
	globalConfig.SkillsConfig.DisEnabled = true
	globalConfig.ToolsConfig.Enabled = true
	memoryDisabled := false
	agentConfig := AgentConfig{
		Name:          "DomainCapability",
		Type:          AgentTypeCustom,
		Model:         "domain-eval-minimax",
		SystemMessage: domainEvalSystemMessage,
		Temperature:   0.1,
		MaxTokens:     12288,
		MaxIterations: 50,
		HasMemory:     &memoryDisabled,
		ReadOnlyPaths: readOnlyPaths,
		EnabledTools:  enabledTools,
	}
	agent := NewLLMAgent(agentConfig, &modelConfig, nil, globalConfig, log.New(io.Discard, "", 0))
	t.Cleanup(func() {
		if agent.ptcServer != nil {
			agent.ptcServer.Stop()
		}
		if agent.lspManager != nil {
			agent.lspManager.Close()
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	t.Cleanup(cancel)
	return domainEvalRun{Agent: agent, Context: ctx, Workspace: workspace}
}

func domainEvalWorkspace(t *testing.T, caseName string) string {
	t.Helper()
	root := strings.TrimSpace(os.Getenv("OPENKITTY_DOMAIN_EVAL_ARTIFACT_DIR"))
	if root == "" {
		return t.TempDir()
	}
	runName := fmt.Sprintf("run-%s-%d", time.Now().Format("20060102-150405.000"), os.Getpid())
	workspace := filepath.Join(root, caseName, runName)
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatalf("create domain evaluation workspace: %v", err)
	}
	return workspace
}

func writeDomainEvalArtifact(t *testing.T, workspace string, artifact domainEvalArtifact) {
	t.Helper()
	data, err := json.MarshalIndent(artifact, "", "  ")
	if err != nil {
		t.Fatalf("encode domain evaluation artifact: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "evaluation.json"), data, 0o600); err != nil {
		t.Fatalf("write domain evaluation artifact: %v", err)
	}
}

type renewalFixture struct {
	Targets    map[string]int
	HoldoutIDs map[string]bool
}

func testDomainDataModeling(t *testing.T) {
	workspace := domainEvalWorkspace(t, "data-modeling")
	dataDir := filepath.Join(workspace, "data")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	dataPath := filepath.Join(dataDir, "customer_renewal.csv")
	fixture := writeRenewalFixture(t, dataPath)

	run := newDomainEvalAgent(
		t,
		"data-modeling",
		workspace,
		[]string{
			"read_file", "list_dir", "glob", "grep", "write_file", "edit_file",
			"persistent_shell", "cli", "code_check", "goal_verify", "tool_detail",
		},
		[]string{"data/**"},
		4*time.Minute,
	)
	prompt := `Build a reproducible binary-classification model from data/customer_renewal.csv.

Business goal:
- Predict the target column renewed for future customer snapshots.
- At scoring time, only information available on snapshot_date may be used. Inspect names and values for post-outcome leakage and exclude it.
- Evaluate on the newest 20% of rows by snapshot_date; do not use a random split.
- Handle missing numeric and categorical values without dropping the holdout rows.
- Compare the model with a majority-class baseline.

Required deliverables:
1. outputs/train_model.py — a reproducible entry point that accepts:
   python outputs/train_model.py --data <csv> --output-dir <directory>
2. outputs/model.joblib
3. outputs/holdout_predictions.csv with exactly these columns:
   customer_id,actual,probability,predicted
4. outputs/metrics.json with this structure:
   target, split_strategy, train_rows, holdout_rows, feature_columns,
   excluded_columns, and metrics containing roc_auc, accuracy, baseline_accuracy
5. outputs/report.md explaining data quality, leakage exclusions, temporal validation,
   model choice, metrics, limitations, and how to rerun it.

Run the entry point yourself, verify every artifact exists, and report the measured holdout metrics.`

	started := time.Now()
	answer, generateErr := run.Agent.Generate(run.Context, prompt)
	duration := time.Since(started)
	verification := verifyDataModelingArtifacts(t, context.Background(), workspace, fixture)
	writeDomainEvalArtifact(t, workspace, domainEvalArtifact{
		Case: "data-modeling", Prompt: prompt, Answer: answer,
		DurationMS: durationMilliseconds(duration), Error: errorString(generateErr),
		Verification: verification,
	})
	t.Logf("data-modeling workspace=%s duration=%s verification=%v", workspace, duration.Round(time.Millisecond), verification)
	if generateErr != nil {
		t.Fatalf("data-modeling agent: %v", generateErr)
	}
	if passed, _ := verification["passed"].(bool); !passed {
		t.Fatalf("data-modeling independent verification failed: %v; answer=%s", verification, answer)
	}
}

func writeRenewalFixture(t *testing.T, path string) renewalFixture {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	writer := csv.NewWriter(file)
	defer writer.Flush()
	header := []string{
		"customer_id", "snapshot_date", "region", "channel", "plan",
		"tenure_months", "monthly_usage", "support_tickets", "discount_pct",
		"renewal_confirmation_id", "post_renewal_credit", "renewed",
	}
	if err := writer.Write(header); err != nil {
		t.Fatal(err)
	}

	rng := mrand.New(mrand.NewSource(20260724))
	regions := []string{"north", "south", "east", "west"}
	channels := []string{"direct", "partner", "retail"}
	plans := []string{"basic", "plus", "pro"}
	start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	fixture := renewalFixture{
		Targets:    make(map[string]int, 240),
		HoldoutIDs: make(map[string]bool, 48),
	}

	for i := 0; i < 240; i++ {
		id := fmt.Sprintf("C%04d", i+1)
		region := regions[(i*7+i/9)%len(regions)]
		channel := channels[(i*5+i/13)%len(channels)]
		plan := plans[(i*11+i/17)%len(plans)]
		tenure := 2 + (i*7)%70
		usage := 28 + float64((i*13)%55) + rng.NormFloat64()*7
		tickets := (i*3 + i/8) % 5
		discount := (i * 5) % 25
		planEffect := map[string]float64{"basic": -0.45, "plus": 0.10, "pro": 0.65}[plan]
		channelEffect := map[string]float64{"direct": 0.20, "partner": 0.0, "retail": -0.25}[channel]
		drift := 0.0
		if i >= 192 {
			drift = -0.25
		}
		logit := -0.65 +
			0.040*float64(tenure) +
			0.047*(usage-52) -
			0.62*float64(tickets) +
			0.018*float64(discount) +
			planEffect + channelEffect + drift
		probability := 1 / (1 + math.Exp(-logit))
		renewed := 0
		if rng.Float64() < probability {
			renewed = 1
		}
		fixture.Targets[id] = renewed
		if i >= 192 {
			fixture.HoldoutIDs[id] = true
		}

		usageText := fmt.Sprintf("%.3f", usage)
		if i%19 == 0 {
			usageText = ""
		}
		if i%23 == 0 {
			region = ""
		}
		confirmation := ""
		postCredit := "0"
		if renewed == 1 {
			confirmation = fmt.Sprintf("RC-%06d", 810000+i)
			postCredit = strconv.Itoa(75 + (i % 30))
		}
		row := []string{
			id,
			start.AddDate(0, 0, i).Format("2006-01-02"),
			region,
			channel,
			plan,
			strconv.Itoa(tenure),
			usageText,
			strconv.Itoa(tickets),
			strconv.Itoa(discount),
			confirmation,
			postCredit,
			strconv.Itoa(renewed),
		}
		if err := writer.Write(row); err != nil {
			t.Fatal(err)
		}
	}
	return fixture
}

type dataMetricsManifest struct {
	Target          string   `json:"target"`
	SplitStrategy   string   `json:"split_strategy"`
	TrainRows       int      `json:"train_rows"`
	HoldoutRows     int      `json:"holdout_rows"`
	FeatureColumns  []string `json:"feature_columns"`
	ExcludedColumns []string `json:"excluded_columns"`
	Metrics         struct {
		ROCAUC           float64 `json:"roc_auc"`
		Accuracy         float64 `json:"accuracy"`
		BaselineAccuracy float64 `json:"baseline_accuracy"`
	} `json:"metrics"`
}

func verifyDataModelingArtifacts(
	t *testing.T,
	ctx context.Context,
	workspace string,
	fixture renewalFixture,
) map[string]interface{} {
	t.Helper()
	result := map[string]interface{}{"passed": false}
	required := []string{
		"outputs/train_model.py",
		"outputs/model.joblib",
		"outputs/holdout_predictions.csv",
		"outputs/metrics.json",
		"outputs/report.md",
	}
	var missing []string
	for _, rel := range required {
		info, err := os.Stat(filepath.Join(workspace, filepath.FromSlash(rel)))
		if err != nil || info.IsDir() || info.Size() == 0 {
			missing = append(missing, rel)
		}
	}
	if len(missing) > 0 {
		result["reason"] = "missing required artifacts"
		result["missing"] = missing
		return result
	}

	verifyDir := filepath.Join(workspace, "verification")
	cmdCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(
		cmdCtx,
		"python",
		filepath.Join("outputs", "train_model.py"),
		"--data", filepath.Join("data", "customer_renewal.csv"),
		"--output-dir", "verification",
	)
	cmd.Dir = workspace
	cmd.Env = append(os.Environ(), "PYTHONUTF8=1", "PYTHONDONTWRITEBYTECODE=1")
	reproductionOutput, reproductionErr := cmd.CombinedOutput()
	result["reproduction_output"] = textutil.TruncateRunes(string(reproductionOutput), 1200)
	if reproductionErr != nil {
		result["reason"] = fmt.Sprintf("reproduction command failed: %v", reproductionErr)
		return result
	}

	manifestPath := filepath.Join(verifyDir, "metrics.json")
	manifestData, err := os.ReadFile(manifestPath)
	if err != nil {
		result["reason"] = "verification metrics.json missing"
		return result
	}
	var manifest dataMetricsManifest
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		result["reason"] = "invalid verification metrics.json: " + err.Error()
		return result
	}
	result["manifest"] = manifest
	if manifest.Target != "renewed" {
		result["reason"] = "wrong target"
		return result
	}
	split := strings.ToLower(manifest.SplitStrategy)
	if !strings.Contains(split, "chron") && !strings.Contains(split, "time") && !strings.Contains(split, "newest") {
		result["reason"] = "split strategy is not temporal"
		return result
	}
	if manifest.TrainRows != 192 || manifest.HoldoutRows != 48 {
		result["reason"] = fmt.Sprintf("wrong split sizes: train=%d holdout=%d", manifest.TrainRows, manifest.HoldoutRows)
		return result
	}

	features := lowerStringSet(manifest.FeatureColumns)
	excluded := lowerStringSet(manifest.ExcludedColumns)
	for _, leak := range []string{"renewal_confirmation_id", "post_renewal_credit"} {
		if features[leak] || !excluded[leak] {
			result["reason"] = "post-outcome leakage was not excluded: " + leak
			return result
		}
	}

	predictionsPath := filepath.Join(verifyDir, "holdout_predictions.csv")
	ids, actual, probabilities, predicted, err := readDomainPredictions(predictionsPath)
	if err != nil {
		result["reason"] = err.Error()
		return result
	}
	if len(ids) != 48 {
		result["reason"] = fmt.Sprintf("wrong prediction row count: %d", len(ids))
		return result
	}
	seen := make(map[string]bool, len(ids))
	for i, id := range ids {
		if !fixture.HoldoutIDs[id] || seen[id] {
			result["reason"] = "predictions are not exactly the newest holdout rows"
			return result
		}
		seen[id] = true
		if actual[i] != fixture.Targets[id] {
			result["reason"] = "prediction actual label does not match source data for " + id
			return result
		}
		if probabilities[i] < 0 || probabilities[i] > 1 ||
			(predicted[i] != 0 && predicted[i] != 1) {
			result["reason"] = "invalid probability or predicted class"
			return result
		}
	}

	auc := binaryAUC(actual, probabilities)
	accuracy := binaryAccuracy(actual, predicted)
	baseline := majorityAccuracy(actual)
	result["independent_roc_auc"] = auc
	result["independent_accuracy"] = accuracy
	result["independent_baseline_accuracy"] = baseline
	if math.IsNaN(auc) || auc < 0.70 {
		result["reason"] = fmt.Sprintf("holdout ROC AUC below acceptance threshold: %.4f", auc)
		return result
	}
	if math.Abs(manifest.Metrics.ROCAUC-auc) > 0.03 ||
		math.Abs(manifest.Metrics.Accuracy-accuracy) > 0.03 ||
		math.Abs(manifest.Metrics.BaselineAccuracy-baseline) > 0.03 {
		result["reason"] = "reported metrics do not match independent calculations"
		return result
	}

	modelInfo, modelErr := os.Stat(filepath.Join(verifyDir, "model.joblib"))
	if modelErr != nil || modelInfo.IsDir() || modelInfo.Size() < 500 {
		result["reason"] = "reproduced model.joblib missing or unexpectedly small"
		return result
	}
	reportPath := filepath.Join(workspace, "outputs", "report.md")
	reportInfo, reportErr := os.Stat(reportPath)
	if reportErr != nil || reportInfo.IsDir() || reportInfo.Size() < 500 {
		result["reason"] = "delivered report.md missing or unexpectedly small"
		return result
	}
	reportData, _ := os.ReadFile(reportPath)
	report := strings.ToLower(string(reportData))
	if !strings.Contains(report, "leak") && !strings.Contains(report, "泄漏") {
		result["reason"] = "report does not discuss leakage"
		return result
	}
	if !strings.Contains(report, "chron") && !strings.Contains(report, "时间") && !strings.Contains(report, "temporal") {
		result["reason"] = "report does not discuss temporal validation"
		return result
	}

	result["passed"] = true
	return result
}

func lowerStringSet(values []string) map[string]bool {
	out := make(map[string]bool, len(values))
	for _, value := range values {
		out[strings.ToLower(strings.TrimSpace(value))] = true
	}
	return out
}

func readDomainPredictions(path string) ([]string, []int, []float64, []int, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("open predictions: %w", err)
	}
	defer file.Close()
	rows, err := csv.NewReader(file).ReadAll()
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("read predictions: %w", err)
	}
	if len(rows) < 2 {
		return nil, nil, nil, nil, fmt.Errorf("predictions are empty")
	}
	expected := []string{"customer_id", "actual", "probability", "predicted"}
	if len(rows[0]) != len(expected) {
		return nil, nil, nil, nil, fmt.Errorf("prediction columns must be %v", expected)
	}
	for i, want := range expected {
		if strings.TrimSpace(strings.TrimPrefix(rows[0][i], "\ufeff")) != want {
			return nil, nil, nil, nil, fmt.Errorf("prediction columns must be %v", expected)
		}
	}
	ids := make([]string, 0, len(rows)-1)
	actual := make([]int, 0, len(rows)-1)
	probabilities := make([]float64, 0, len(rows)-1)
	predicted := make([]int, 0, len(rows)-1)
	for rowIndex, row := range rows[1:] {
		if len(row) != len(expected) {
			return nil, nil, nil, nil, fmt.Errorf("prediction row %d has wrong column count", rowIndex+2)
		}
		actualValue, actualErr := strconv.Atoi(strings.TrimSpace(row[1]))
		probability, probabilityErr := strconv.ParseFloat(strings.TrimSpace(row[2]), 64)
		predictedValue, predictedErr := strconv.Atoi(strings.TrimSpace(row[3]))
		if actualErr != nil || probabilityErr != nil || predictedErr != nil {
			return nil, nil, nil, nil, fmt.Errorf("prediction row %d has invalid numeric values", rowIndex+2)
		}
		ids = append(ids, strings.TrimSpace(row[0]))
		actual = append(actual, actualValue)
		probabilities = append(probabilities, probability)
		predicted = append(predicted, predictedValue)
	}
	return ids, actual, probabilities, predicted, nil
}

func binaryAUC(actual []int, scores []float64) float64 {
	var positives, negatives []float64
	for i, label := range actual {
		if label == 1 {
			positives = append(positives, scores[i])
		} else {
			negatives = append(negatives, scores[i])
		}
	}
	if len(positives) == 0 || len(negatives) == 0 {
		return math.NaN()
	}
	var wins float64
	for _, positive := range positives {
		for _, negative := range negatives {
			switch {
			case positive > negative:
				wins++
			case positive == negative:
				wins += 0.5
			}
		}
	}
	return wins / float64(len(positives)*len(negatives))
}

func binaryAccuracy(actual, predicted []int) float64 {
	if len(actual) == 0 || len(actual) != len(predicted) {
		return math.NaN()
	}
	correct := 0
	for i := range actual {
		if actual[i] == predicted[i] {
			correct++
		}
	}
	return float64(correct) / float64(len(actual))
}

func majorityAccuracy(actual []int) float64 {
	positive := 0
	for _, label := range actual {
		if label == 1 {
			positive++
		}
	}
	negative := len(actual) - positive
	if positive > negative {
		return float64(positive) / float64(len(actual))
	}
	return float64(negative) / float64(len(actual))
}

func testDomainReverseEngineering(t *testing.T) {
	workspace := domainEvalWorkspace(t, "reverse-engineering")
	challengePath := filepath.Join(workspace, "challenge.exe")
	secret := randomChallengeCode(t, 12)
	buildReverseEngineeringChallenge(t, challengePath, secret)
	readme := `Synthetic reverse-engineering exercise.
The supplied file challenge.exe is an unstripped Windows amd64 Go binary.
It accepts one command-line argument and prints ACCESS GRANTED only for the valid code.
The source is intentionally not included. The binary and this README are immutable inputs.
`
	if err := os.WriteFile(filepath.Join(workspace, "README.txt"), []byte(readme), 0o600); err != nil {
		t.Fatal(err)
	}
	originalHash := fileSHA256(t, challengePath)

	run := newDomainEvalAgent(
		t,
		"reverse-engineering",
		workspace,
		[]string{
			"read_file", "list_dir", "glob", "grep", "write_file", "edit_file",
			"persistent_shell", "cli", "binary", "goal_verify", "tool_detail",
		},
		[]string{"challenge.exe", "README.txt"},
		4*time.Minute,
	)
	prompt := `Reverse engineer the synthetic local program challenge.exe and recover its accepted code.

Constraints:
- Analyze only the supplied local challenge. Do not use the network.
- Do not modify, replace, patch, or rebuild challenge.exe.
- Do not brute-force the full 12-character keyspace.
- Use static and/or dynamic evidence from the binary and verify the recovered code by executing the program.

Required deliverables:
1. outputs/solution.txt containing only the accepted code.
2. outputs/reverse_engineering_report.md explaining the binary format/architecture,
   the relevant function or code path, the recovered validation algorithm and constants,
   the derivation of the accepted input, and verification evidence.
3. outputs/verification.txt containing the command used and the real program output.

Complete the analysis and create all three artifacts now.`

	started := time.Now()
	answer, generateErr := run.Agent.Generate(run.Context, prompt)
	duration := time.Since(started)
	verification := verifyReverseEngineeringArtifacts(t, context.Background(), workspace, challengePath, originalHash)
	writeDomainEvalArtifact(t, workspace, domainEvalArtifact{
		Case: "reverse-engineering", Prompt: prompt, Answer: answer,
		DurationMS: durationMilliseconds(duration), Error: errorString(generateErr),
		Verification: verification,
	})
	t.Logf("reverse-engineering workspace=%s duration=%s verification=%v", workspace, duration.Round(time.Millisecond), verification)
	if generateErr != nil {
		t.Fatalf("reverse-engineering agent: %v", generateErr)
	}
	if passed, _ := verification["passed"].(bool); !passed {
		t.Fatalf("reverse-engineering independent verification failed: %v; answer=%s", verification, answer)
	}
}

func randomChallengeCode(t *testing.T, length int) string {
	t.Helper()
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	random := make([]byte, length)
	if _, err := crand.Read(random); err != nil {
		t.Fatal(err)
	}
	for i := range random {
		random[i] = alphabet[int(random[i])%len(alphabet)]
	}
	return string(random)
}

func buildReverseEngineeringChallenge(t *testing.T, outputPath, secret string) {
	t.Helper()
	target := make([]byte, len(secret))
	var checksum uint32
	for i := range secret {
		key := byte(0x31 + i*11)
		target[i] = rotateLeft8(secret[i]^key, (i%5)+1)
		checksum = (checksum*131 + uint32(secret[i])) % 65521
	}
	constants := make([]string, len(target))
	for i, value := range target {
		constants[i] = fmt.Sprintf("0x%02x", value)
	}
	source := fmt.Sprintf(`package main

import (
	"fmt"
	"math/bits"
	"os"
)

//go:noinline
func verify(candidate string) bool {
	if len(candidate) != %d {
		return false
	}
	target := [%d]byte{%s}
	var checksum uint32
	for i := 0; i < len(candidate); i++ {
		key := byte(0x31 + i*11)
		transformed := bits.RotateLeft8(candidate[i]^key, (i%%5)+1)
		if transformed != target[i] {
			return false
		}
		checksum = (checksum*131 + uint32(candidate[i])) %% 65521
	}
	return checksum == %d
}

func main() {
	if len(os.Args) != 2 {
		fmt.Println("usage: challenge.exe CODE")
		os.Exit(2)
	}
	if verify(os.Args[1]) {
		fmt.Println("ACCESS GRANTED")
		return
	}
	fmt.Println("ACCESS DENIED")
	os.Exit(1)
}
`, len(secret), len(secret), strings.Join(constants, ", "), checksum)

	sourceDir := t.TempDir()
	sourcePath := filepath.Join(sourceDir, "main.go")
	if err := os.WriteFile(sourcePath, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("go", "build", "-gcflags=all=-l", "-o", outputPath, sourcePath)
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build reverse-engineering challenge: %v\n%s", err, output)
	}
}

func rotateLeft8(value byte, shift int) byte {
	shift &= 7
	if shift == 0 {
		return value
	}
	return value<<shift | value>>(8-shift)
}

func fileSHA256(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("%x", sha256.Sum256(data))
}

func verifyReverseEngineeringArtifacts(
	t *testing.T,
	ctx context.Context,
	workspace string,
	challengePath string,
	originalHash string,
) map[string]interface{} {
	t.Helper()
	result := map[string]interface{}{"passed": false}
	if currentHash := fileSHA256(t, challengePath); currentHash != originalHash {
		result["reason"] = "challenge binary was modified"
		result["binary_unchanged"] = false
		return result
	}
	result["binary_unchanged"] = true

	solutionData, err := os.ReadFile(filepath.Join(workspace, "outputs", "solution.txt"))
	if err != nil {
		result["reason"] = "solution.txt missing"
		return result
	}
	solution := strings.TrimSpace(string(solutionData))
	if len(solution) != 12 || strings.ContainsAny(solution, "\r\n\t ") {
		result["reason"] = "solution.txt must contain one 12-character code"
		return result
	}

	commandCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	command := exec.CommandContext(commandCtx, challengePath, solution)
	command.Dir = workspace
	output, runErr := command.CombinedOutput()
	result["program_output"] = strings.TrimSpace(string(output))
	result["program_exit_success"] = runErr == nil
	if runErr != nil || !strings.Contains(string(output), "ACCESS GRANTED") {
		result["reason"] = fmt.Sprintf("recovered code was rejected: %v", runErr)
		return result
	}

	wrong := strings.Repeat("A", 12)
	if wrong == solution {
		wrong = strings.Repeat("B", 12)
	}
	wrongCommand := exec.CommandContext(commandCtx, challengePath, wrong)
	wrongCommand.Dir = workspace
	wrongOutput, wrongErr := wrongCommand.CombinedOutput()
	if wrongErr == nil || !strings.Contains(string(wrongOutput), "ACCESS DENIED") {
		result["reason"] = "challenge does not reject an incorrect code"
		return result
	}

	reportPath := filepath.Join(workspace, "outputs", "reverse_engineering_report.md")
	reportInfo, reportErr := os.Stat(reportPath)
	if reportErr != nil || reportInfo.IsDir() || reportInfo.Size() < 600 {
		result["reason"] = "reverse engineering report missing or too small"
		return result
	}
	reportData, _ := os.ReadFile(reportPath)
	evidenceCategories, missingCategories := reverseEngineeringReportEvidence(string(reportData))
	if len(missingCategories) > 0 {
		result["reason"] = "report lacks required evidence categories: " + strings.Join(missingCategories, ", ")
		result["evidence_categories"] = evidenceCategories
		result["missing_evidence_categories"] = missingCategories
		return result
	}
	verificationData, verificationErr := os.ReadFile(filepath.Join(workspace, "outputs", "verification.txt"))
	if verificationErr != nil || !strings.Contains(string(verificationData), "ACCESS GRANTED") {
		result["reason"] = "verification.txt lacks real successful program output"
		return result
	}

	result["solution_length"] = len(solution)
	result["report_bytes"] = reportInfo.Size()
	result["evidence_categories"] = evidenceCategories
	result["passed"] = true
	return result
}

// reverseEngineeringReportEvidence checks the report against the requested
// semantic sections instead of requiring one tool name or one spelling of an
// operation. A valid report may use objdump, a debugger, a decompiler, native
// binary tooling, or equivalent vocabulary.
func reverseEngineeringReportEvidence(report string) (int, []string) {
	lower := strings.ToLower(report)
	hasAny := func(terms ...string) bool {
		for _, term := range terms {
			if strings.Contains(lower, strings.ToLower(term)) {
				return true
			}
		}
		return false
	}
	checks := []struct {
		name string
		ok   bool
	}{
		{
			name: "binary format and architecture",
			ok: hasAny(
				"portable executable", "binary format", "elf", "mach-o", "mach o",
				"pe (", "二进制格式", "文件格式",
			) && hasAny(
				"amd64", "x86-64", "x86_64", "arm64", "aarch64", "architecture", "架构",
			),
		},
		{
			name: "relevant function or code path",
			ok: hasAny(
				"code path", "call path", "control flow", "entry point", "main.",
				"relevant function", "函数", "代码路径", "调用路径", "入口点",
			),
		},
		{
			name: "validation algorithm and constants",
			ok: hasAny(
				"algorithm", "validation", "validator", "equation", "xor", "rotation",
				"rotate", "rol", "ror", "checksum", "accumulator", "compare", "hash",
				"算法", "校验", "异或", "轮转", "比较",
			) && hasAny(
				"constant", "0x", "immediate", "magic number", "常量", "立即数",
			),
		},
		{
			name: "accepted-input derivation",
			ok: hasAny(
				"derivation", "derived", "derive", "equation", "invert", "recovered input",
				"accepted input", "推导", "反推", "恢复输入", "求解",
			),
		},
		{
			name: "static or dynamic analysis evidence",
			ok: hasAny(
				"disassembly", "disassembled", "decompile", "objdump", "debugger",
				"dynamic trace", "static analysis", "反汇编", "反编译", "调试器",
				"动态跟踪", "静态分析",
			),
		},
		{
			name: "execution verification evidence",
			ok: hasAny(
				"verification", "program output", "stdout", "exit code", "access granted",
				"验证", "程序输出", "退出码",
			),
		},
	}

	matched := 0
	missing := make([]string, 0)
	for _, check := range checks {
		if check.ok {
			matched++
		} else {
			missing = append(missing, check.name)
		}
	}
	return matched, missing
}

func TestReverseEngineeringReportEvidenceAcceptsEquivalentVocabulary(t *testing.T) {
	report := `# Analysis
Binary format and architecture: PE (Portable Executable), amd64.
The entry point calls the relevant function main.verify.
Recovered validation algorithm: XOR followed by left rotation; constants include 0x31 and 0x1c07.
Derivation: invert the equation at each position to recover the accepted input.
Disassembly evidence shows the compare loop and immediate values.
Verification used the real program; stdout was ACCESS GRANTED and exit code 0.`
	matched, missing := reverseEngineeringReportEvidence(report)
	if matched != 6 || len(missing) != 0 {
		t.Fatalf("equivalent report coverage matched=%d missing=%v", matched, missing)
	}
}

func TestReverseEngineeringReportEvidenceRejectsOutcomeOnly(t *testing.T) {
	matched, missing := reverseEngineeringReportEvidence(
		"PE amd64 challenge solved. The accepted code worked.",
	)
	if matched >= 6 || len(missing) == 0 {
		t.Fatalf("outcome-only report unexpectedly accepted: matched=%d missing=%v", matched, missing)
	}
}
