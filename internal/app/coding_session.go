package app

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	codingcore "mac/internal/coding"
	memorycore "mac/internal/memory"
	todocore "mac/internal/todo"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"mac/internal/configvalue"
	"mac/internal/fsutil"
	mcpcore "mac/internal/mcp"
	"mac/internal/projectctx"
	browsertools "mac/internal/tools/browser"
	developmenttools "mac/internal/tools/development"
	workspacetools "mac/internal/tools/workspace"
)

const codeSessionStateVersion = 2

type CodeCommandOptions struct {
	ConfigPath string
	Workspace  string
	Agent      string
	Resume     string
	Prompt     string
	Worktree   bool
	OneShot    bool
	Help       bool
}

type CodeSessionMessage struct {
	Role    string    `json:"role"`
	Content string    `json:"content"`
	Time    time.Time `json:"time"`
}

type CodeWorktreeState struct {
	ManagerName string `json:"manager_name"`
	Path        string `json:"path"`
	Branch      string `json:"branch"`
	BaseBranch  string `json:"base_branch"`
}

type CodeSessionState struct {
	Version       int                                  `json:"version"`
	ID            string                               `json:"id"`
	RootWorkspace string                               `json:"root_workspace"`
	Workspace     string                               `json:"workspace"`
	Agent         string                               `json:"agent"`
	CreatedAt     time.Time                            `json:"created_at"`
	UpdatedAt     time.Time                            `json:"updated_at"`
	Turn          int                                  `json:"turn"`
	PlanMode      bool                                 `json:"plan_mode"`
	Messages      []CodeSessionMessage                 `json:"messages"`
	Worktree      *CodeWorktreeState                   `json:"worktree,omitempty"`
	ChangeSets    []codingcore.CodeChangeSet           `json:"change_sets,omitempty"`
	Verifications []codingcore.CodeVerificationReceipt `json:"verifications,omitempty"`
}

type CodingSession struct {
	state  *CodeSessionState
	agent  Agent
	llm    *LLMAgent
	engine *MultiAgentEngine
	input  io.Reader
	output io.Writer
	errors io.Writer
	mu     sync.Mutex
}

func codeModeRequested(args []string) bool {
	return len(args) > 0 && strings.EqualFold(strings.TrimSpace(args[0]), "code")
}

func parseCodeCommandArgs(args []string) (CodeCommandOptions, error) {
	options := CodeCommandOptions{}
	var positional []string
	readValue := func(index *int, flag string) (string, error) {
		if *index+1 >= len(args) {
			return "", fmt.Errorf("%s requires a value", flag)
		}
		(*index)++
		return args[*index], nil
	}

	for index := 0; index < len(args); index++ {
		arg := args[index]
		switch arg {
		case "--help", "-h":
			options.Help = true
		case "--config", "-c":
			value, err := readValue(&index, arg)
			if err != nil {
				return options, err
			}
			options.ConfigPath = value
		case "--path", "-C":
			value, err := readValue(&index, arg)
			if err != nil {
				return options, err
			}
			options.Workspace = value
		case "--agent", "-a":
			value, err := readValue(&index, arg)
			if err != nil {
				return options, err
			}
			options.Agent = value
		case "--resume", "-r":
			value, err := readValue(&index, arg)
			if err != nil {
				return options, err
			}
			options.Resume = value
		case "--prompt", "-p":
			value, err := readValue(&index, arg)
			if err != nil {
				return options, err
			}
			options.Prompt = value
			options.OneShot = true
		case "--one-shot":
			options.OneShot = true
		case "--worktree":
			options.Worktree = true
		case "--no-worktree":
			options.Worktree = false
		default:
			if strings.HasPrefix(arg, "-") {
				return options, fmt.Errorf("unknown code option %q", arg)
			}
			positional = append(positional, arg)
		}
	}
	if options.Prompt == "" && len(positional) > 0 {
		options.Prompt = strings.Join(positional, " ")
	}
	return options, nil
}

func codeCommandUsage() string {
	return `MACS Code - repository-native coding session

Usage:
  openkitty code [task] [options]
  openkitty code --prompt "task"        Run once and exit
  openkitty code --resume latest

Options:
  -C, --path <dir>       Project workspace (default: current directory)
  -c, --config <file>    MACS configuration file
  -a, --agent <name>     Configured agent to use
  -r, --resume <id>      Resume a saved session; use "latest" for the newest
      --worktree         Create an isolated managed Git worktree
  -p, --prompt <task>    Execute one task and exit
      --one-shot         Exit after the positional task completes
  -h, --help             Show this help

Interactive commands:
  /help                  Show commands
  /instructions [full]   Audit applied project instructions
  /status                Show Git status
  /diff [stat]           Show current diff
  /test [full|path]      Run targeted or full deterministic tests
  /verify [full|path]    Verify current changes and save a receipt
  /review [scope]        Review unstaged/staged/branch/file changes
  /agents [full]         Show sub-agent progress/results
  /checkpoints           List file checkpoints
  /undo [turn]           Rewind the latest or selected turn
  /plan                  Enter plan-only mode
  /execute               Exit plan-only mode
  /clear                 Clear conversation history
  /session               Show session/worktree details
  /quit                  Save and exit`
}

func runCodeCommand(args []string) int {
	options, err := parseCodeCommandArgs(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "openkitty code: %v\n\n%s\n", err, codeCommandUsage())
		return 2
	}
	if options.Help {
		fmt.Println(codeCommandUsage())
		return 0
	}

	workspace := strings.TrimSpace(options.Workspace)
	if workspace == "" {
		workspace, err = os.Getwd()
		if err != nil {
			fmt.Fprintf(os.Stderr, "resolve current workspace: %v\n", err)
			return 1
		}
	}
	workspace = projectctx.NormalizeInstructionPath(workspace)
	rootWorkspace := workspace

	var state *CodeSessionState
	if options.Resume != "" {
		state, err = loadCodeSession(rootWorkspace, options.Resume)
		if err != nil {
			fmt.Fprintf(os.Stderr, "resume code session: %v\n", err)
			return 1
		}
		if state.RootWorkspace != "" {
			rootWorkspace = state.RootWorkspace
		}
		if state.Workspace != "" {
			if info, statErr := os.Stat(state.Workspace); statErr == nil && info.IsDir() {
				workspace = state.Workspace
			} else {
				fmt.Fprintf(os.Stderr, "warning: saved workspace %s is unavailable; using %s\n", state.Workspace, rootWorkspace)
				workspace = rootWorkspace
				state.Workspace = workspace
				state.Worktree = nil
			}
		}
	}

	if state == nil {
		now := time.Now()
		state = &CodeSessionState{
			Version:       codeSessionStateVersion,
			ID:            newCodeSessionID(now),
			RootWorkspace: rootWorkspace,
			Workspace:     workspace,
			CreatedAt:     now,
			UpdatedAt:     now,
		}
	}

	if options.Worktree && state.Worktree == nil {
		manager := developmenttools.NewWorktreeManager(rootWorkspace)
		managerName := "code-" + state.ID
		if status := gitCommandOutput(rootWorkspace, "status", "--short"); strings.TrimSpace(status) != "" {
			fmt.Fprintln(os.Stderr, "warning: the managed worktree starts from HEAD; uncommitted changes in the main workspace are not copied.")
		}
		info, worktreeErr := manager.CreateWorktree(managerName)
		if worktreeErr != nil {
			fmt.Fprintf(os.Stderr, "create code worktree: %v\n", worktreeErr)
			return 1
		}
		workspace = info.Path
		state.Workspace = info.Path
		state.Worktree = &CodeWorktreeState{
			ManagerName: managerName,
			Path:        info.Path,
			Branch:      info.Branch,
			BaseBranch:  info.BaseBranch,
		}
	}

	configPath, err := findCodeConfig(rootWorkspace, options.ConfigPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "config error: %v\n", err)
		return 1
	}
	if err := initializeCodeDatabase(configPath); err != nil {
		fmt.Fprintf(os.Stderr, "database initialization warning: %v\n", err)
	}
	config, err := loadCodeRuntimeConfig(configPath, workspace)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load code runtime: %v\n", err)
		return 1
	}
	engine, err := NewMultiAgentEngine(config)
	if err != nil {
		fmt.Fprintf(os.Stderr, "create coding agent: %v\n", err)
		return 1
	}
	defer engine.Close()

	var mcpManager *mcpcore.MCPManager
	if len(config.MCPServers) > 0 {
		mcpManager = mcpcore.NewMCPManager(log.New(os.Stderr, "[MCP] ", log.LstdFlags), CurrentVersion)
		connectCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		if connectErr := mcpManager.ConnectAll(connectCtx, config.MCPServers); connectErr != nil {
			fmt.Fprintf(os.Stderr, "MCP connection warning: %v\n", connectErr)
		}
		cancel()
		engine.registerMCPTools(mcpManager)
		defer mcpManager.DisconnectAll()
	}

	agentName, agent, ok := selectCodeAgent(engine, firstNonEmpty(options.Agent, state.Agent))
	if !ok {
		fmt.Fprintln(os.Stderr, "no configured coding agent is available")
		return 1
	}
	state.Agent = agentName
	state.Workspace = workspace
	rootPath = workspace
	browsertools.SetWorkspace(workspace)

	llmAgent, _ := agent.(*LLMAgent)
	if llmAgent != nil {
		configureCodeLLMAgent(llmAgent, state)
		if state.PlanMode {
			llmAgent.EnterPlanMode()
		}
	}

	session := &CodingSession{
		state:  state,
		agent:  agent,
		llm:    llmAgent,
		engine: engine,
		input:  os.Stdin,
		output: os.Stdout,
		errors: os.Stderr,
	}
	engine.SetProgressBroadcaster(session.handleProgress)
	if err := saveCodeSession(state); err != nil {
		fmt.Fprintf(os.Stderr, "save initial code session: %v\n", err)
	}
	return session.Run(options.Prompt, options.OneShot)
}

func findCodeConfig(rootWorkspace, explicit string) (string, error) {
	if strings.TrimSpace(explicit) != "" {
		return FindConfig(explicit)
	}
	rootWorkspace = projectctx.NormalizeInstructionPath(rootWorkspace)
	for _, candidate := range []string{
		filepath.Join(rootWorkspace, "openkitty-config.yaml"),
		filepath.Join(rootWorkspace, ".openkitty", "openkitty-config.yaml"),
	} {
		if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() {
			return candidate, nil
		}
	}
	return FindConfig("")
}

func initializeCodeDatabase(configPath string) error {
	dbConfig, configErr := configvalue.LoadDatabaseBootstrapConfig(configPath)
	if configErr != nil {
		return configErr
	}
	var err error
	if dbConfig.Host != "" {
		db, err = initDB(dbConfig.Host, dbConfig.Port, dbConfig.User, dbConfig.Password, dbConfig.Database)
	} else {
		db, err = initDB("", "", "", "", "")
	}
	if err != nil {
		return err
	}
	memorycore.InitMemoryDB(db)
	todocore.InitTables(db)
	return nil
}

func loadCodeRuntimeConfig(configPath, workspace string) (*Config, error) {
	config, err := LoadOrSetupConfig(configPath)
	if err != nil {
		if _, fileErr := os.Stat(configPath); fileErr != nil || config == nil {
			return nil, err
		}
	}
	if config == nil {
		return nil, fmt.Errorf("configuration is nil after load")
	}
	if err := applyGatewayEnvironment(config); err != nil {
		return nil, err
	}
	config.Workspace = workspace
	if err := initWorkspace(config); err != nil {
		return nil, err
	}
	loadFeatureFlagsFromConfig(config)
	if _, err := ReloadGlobalHookSettings(config, log.New(os.Stderr, "[HookV2] ", log.LstdFlags)); err != nil {
		log.Printf("[HookV2] coding session hook settings were not applied: %v", err)
	}
	return config, nil
}

func selectCodeAgent(engine *MultiAgentEngine, requested string) (string, Agent, bool) {
	if engine == nil {
		return "", nil, false
	}
	if strings.TrimSpace(requested) != "" {
		if name, agent, found := engine.FindAgent(requested); found {
			return name, agent, true
		}
		return "", nil, false
	}
	return engine.DefaultAgent()
}

func configureCodeLLMAgent(agent *LLMAgent, state *CodeSessionState) {
	if agent == nil || state == nil {
		return
	}
	agent.workspace = state.Workspace
	agent.projectInstructions = projectctx.NewProjectInstructionLoader(OpenKittyHome())
	agent.snapshotManager = workspacetools.NewSnapshotManager(state.Workspace, state.ID)
	agent.fileHistory = workspacetools.NewFileHistoryManager(state.Workspace, state.ID)
	agent.worktreeManager = developmenttools.NewWorktreeManager(state.Workspace)
	if agent.toolRegistry != nil {
		agent.toolRegistry.Register(workspacetools.NewProjectInstructionsTool(state.Workspace, agent.projectInstructions))
		agent.toolRegistry.Register(workspacetools.NewUndoSnapshotTool(agent.snapshotManager))
	}
}

func (session *CodingSession) Run(initialPrompt string, oneShot bool) int {
	if session == nil || session.state == nil || session.agent == nil {
		fmt.Fprintln(sessionErrorWriter(session), "coding session is not initialized")
		return 1
	}
	session.printBanner()
	if strings.TrimSpace(initialPrompt) != "" {
		if err := session.runTurn(initialPrompt); err != nil {
			fmt.Fprintf(session.errors, "error: %v\n", err)
			if oneShot {
				return 1
			}
		}
		if oneShot {
			return 0
		}
	}

	scanner := bufio.NewScanner(session.input)
	scanner.Buffer(make([]byte, 64*1024), 2*1024*1024)
	for {
		fmt.Fprintf(session.output, "\nopenkitty[%s] › ", session.state.Agent)
		if !scanner.Scan() {
			if err := scanner.Err(); err != nil {
				fmt.Fprintf(session.errors, "read input: %v\n", err)
				return 1
			}
			fmt.Fprintln(session.output)
			return 0
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "/") {
			handled, exit := session.handleCommand(line)
			if handled {
				if exit {
					return 0
				}
				continue
			}
		}
		if err := session.runTurn(line); err != nil {
			fmt.Fprintf(session.errors, "error: %v\n", err)
		}
	}
}

func (session *CodingSession) printBanner() {
	fmt.Fprintf(session.output, "MACS Code\n  session: %s\n  workspace: %s\n  agent: %s\n",
		session.state.ID, session.state.Workspace, session.state.Agent)
	if session.state.Worktree != nil {
		fmt.Fprintf(session.output, "  worktree: %s (%s)\n", session.state.Worktree.Path, session.state.Worktree.Branch)
	}
	fmt.Fprintln(session.output, "Type /help for commands.")
}

func (session *CodingSession) runTurn(input string) error {
	input = strings.TrimSpace(input)
	if input == "" {
		return nil
	}
	beforeState := codingcore.CaptureCodeWorkspaceState(session.state.Workspace)
	snapshotTurn := -1
	var snapshotManager *workspacetools.SnapshotManager
	if session.llm != nil && session.llm.snapshotManager != nil {
		snapshotManager = session.llm.snapshotManager
		snapshotTurn = snapshotManager.CurrentTurn()
	}

	history := make([]map[string]interface{}, 0, len(session.state.Messages))
	start := 0
	if len(session.state.Messages) > 60 {
		start = len(session.state.Messages) - 60
	}
	for _, message := range session.state.Messages[start:] {
		history = append(history, map[string]interface{}{
			"role":    message.Role,
			"content": message.Content,
		})
	}

	taskID := fmt.Sprintf("%s-t%d", session.state.ID, session.state.Turn+1)
	ctx := context.Background()
	ctx = context.WithValue(ctx, "task_id", taskID)
	ctx = context.WithValue(ctx, "session_id", session.state.ID)
	ctx = context.WithValue(ctx, "channel", "code")
	ctx = context.WithValue(ctx, "user_id", "local")
	ctx = context.WithValue(ctx, "history_messages", history)
	ctx = projectctx.WithResolvedWorkspace(ctx, session.state.Workspace)
	if snapshotManager != nil {
		ctx = workspacetools.WithDeferredSnapshotTurn(ctx)
		defer snapshotManager.EndTurn()
	}
	if session.engine != nil {
		session.engine.SetCurrentTaskID(taskID)
	}

	fmt.Fprintf(session.output, "\n● working on turn %d\n", session.state.Turn+1)
	started := time.Now()
	response, err := session.agent.Generate(ctx, input)
	if err != nil {
		return err
	}

	afterState := codingcore.CaptureCodeWorkspaceState(session.state.Workspace)
	var snapshots []*workspacetools.FileSnapshot
	if snapshotManager != nil {
		snapshots = snapshotManager.ListSnapshots()
	}
	changeSet := codingcore.DetectCodeChangeSet(
		session.state.Workspace,
		session.state.Turn+1,
		snapshotTurn,
		beforeState,
		afterState,
		snapshots,
	)

	finalResponse := response
	var receipts []codingcore.CodeVerificationReceipt
	var terminalErr error
	if len(changeSet.Files) > 0 && !session.state.PlanMode {
		fmt.Fprintf(session.output, "  → verifying %d changed file(s)\n", len(changeSet.Files))
		verifyCtx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		receipt := codingcore.RunCodeVerification(verifyCtx, session.state.Workspace, changeSet, codingcore.CodeVerificationAuto, codingcore.CodeVerificationAuto)
		cancel()

		if receipt.Status == codingcore.CodeCheckFailed {
			receipt.RepairAttempted = true
			fmt.Fprintln(session.output, "  → verification failed; starting one bounded repair attempt")
			repairPrompt := codingcore.BuildCodingRepairPrompt(input, response, changeSet, receipt)
			repairedResponse, repairErr := session.agent.Generate(ctx, repairPrompt)
			if repairErr != nil {
				receipt.RepairError = repairErr.Error()
				terminalErr = fmt.Errorf("automatic repair failed: %w", repairErr)
			} else {
				finalResponse = repairedResponse
				afterRepairState := codingcore.CaptureCodeWorkspaceState(session.state.Workspace)
				if snapshotManager != nil {
					snapshots = snapshotManager.ListSnapshots()
				}
				changeSet = codingcore.DetectCodeChangeSet(
					session.state.Workspace,
					session.state.Turn+1,
					snapshotTurn,
					beforeState,
					afterRepairState,
					snapshots,
				)
				repairCtx, repairCancel := context.WithTimeout(context.Background(), 90*time.Second)
				repairReceipt := codingcore.RunCodeVerification(
					repairCtx,
					session.state.Workspace,
					changeSet,
					"auto_repair",
					codingcore.CodeVerificationAuto,
				)
				repairCancel()
				receipt.RepairSucceeded = repairReceipt.Status == codingcore.CodeCheckPassed
				receipts = append(receipts, receipt, repairReceipt)
				if repairReceipt.Status != codingcore.CodeCheckPassed {
					terminalErr = fmt.Errorf(
						"verification did not pass after the bounded repair attempt (status: %s)",
						repairReceipt.Status,
					)
				}
			}
		}
		if len(receipts) == 0 {
			receipts = append(receipts, receipt)
		}
	}

	if len(changeSet.Files) > 0 {
		session.state.ChangeSets = append(session.state.ChangeSets, changeSet)
		if len(session.state.ChangeSets) > 50 {
			session.state.ChangeSets = append([]codingcore.CodeChangeSet(nil), session.state.ChangeSets[len(session.state.ChangeSets)-50:]...)
		}
	}
	if len(receipts) > 0 {
		session.state.Verifications = append(session.state.Verifications, receipts...)
		if len(session.state.Verifications) > 100 {
			session.state.Verifications = append([]codingcore.CodeVerificationReceipt(nil), session.state.Verifications[len(session.state.Verifications)-100:]...)
		}
	}

	fmt.Fprintf(session.output, "\n%s\n", strings.TrimSpace(finalResponse))
	for _, receipt := range receipts {
		fmt.Fprintf(session.output, "\n%s\n", receipt.Summary(false))
	}
	fmt.Fprintf(session.output, "\n✓ completed in %s\n", time.Since(started).Round(time.Millisecond))

	now := time.Now()
	session.state.Messages = append(session.state.Messages,
		CodeSessionMessage{Role: "user", Content: input, Time: now},
		CodeSessionMessage{Role: "assistant", Content: finalResponse, Time: time.Now()},
	)
	session.state.Turn++
	session.state.UpdatedAt = time.Now()
	if err := saveCodeSession(session.state); err != nil {
		return err
	}
	return terminalErr
}

func (session *CodingSession) handleCommand(line string) (bool, bool) {
	fields := strings.Fields(line)
	command := strings.ToLower(fields[0])
	arg := ""
	if len(fields) > 1 {
		arg = strings.Join(fields[1:], " ")
	}
	switch command {
	case "/quit", "/exit", "/q":
		session.state.UpdatedAt = time.Now()
		if err := saveCodeSession(session.state); err != nil {
			fmt.Fprintf(session.errors, "save session: %v\n", err)
		}
		if session.state.Worktree != nil {
			fmt.Fprintf(session.output, "Worktree retained at %s on branch %s.\n",
				session.state.Worktree.Path, session.state.Worktree.Branch)
		}
		return true, true
	case "/help":
		fmt.Fprintln(session.output, codeCommandUsage())
	case "/instructions":
		session.showInstructions(strings.EqualFold(strings.TrimSpace(arg), "full"))
	case "/status":
		session.showGit("status", "--short", "--branch")
	case "/diff":
		if strings.EqualFold(strings.TrimSpace(arg), "stat") {
			session.showGit("diff", "--stat")
		} else {
			session.showGit("diff", "--")
		}
	case "/test":
		session.runVerificationCommand(codingcore.CodeVerificationTest, arg)
	case "/verify":
		session.runVerificationCommand(codingcore.CodeVerificationVerify, arg)
	case "/review":
		session.runReviewCommand(arg)
	case "/agents":
		if session.llm == nil || session.llm.toolRegistry == nil {
			fmt.Fprintln(session.output, "Sub-agent status is unavailable for this agent.")
			break
		}
		statusTool, ok := session.llm.toolRegistry.Get("agent_status")
		if !ok {
			fmt.Fprintln(session.output, "Sub-agent status is unavailable for this agent.")
			break
		}
		result, err := statusTool.Execute(context.Background(), map[string]interface{}{
			"include_result": strings.EqualFold(strings.TrimSpace(arg), "full"),
		})
		if err != nil {
			fmt.Fprintf(session.errors, "sub-agent status: %v\n", err)
			break
		}
		fmt.Fprintln(session.output, result)
	case "/checkpoints":
		if session.llm == nil || session.llm.snapshotManager == nil {
			fmt.Fprintln(session.output, "Checkpoints are unavailable for this agent.")
		} else {
			fmt.Fprintln(session.output, session.llm.snapshotManager.SnapshotSummary())
		}
	case "/undo":
		session.undo(arg)
	case "/plan":
		if session.llm == nil {
			fmt.Fprintln(session.output, "Plan mode is unavailable for this agent.")
			break
		}
		session.llm.EnterPlanMode()
		session.state.PlanMode = true
		session.state.UpdatedAt = time.Now()
		_ = saveCodeSession(session.state)
		fmt.Fprintln(session.output, "Plan mode enabled; mutating tools are disabled.")
	case "/execute":
		if session.llm == nil {
			fmt.Fprintln(session.output, "Plan mode is unavailable for this agent.")
			break
		}
		session.llm.ExitPlanMode()
		session.state.PlanMode = false
		session.state.UpdatedAt = time.Now()
		_ = saveCodeSession(session.state)
		fmt.Fprintln(session.output, "Execution mode enabled.")
	case "/clear":
		session.state.Messages = nil
		session.state.UpdatedAt = time.Now()
		_ = saveCodeSession(session.state)
		fmt.Fprintln(session.output, "Conversation history cleared.")
	case "/session":
		session.printBanner()
	default:
		return false, false
	}
	return true, false
}

func (session *CodingSession) runVerificationCommand(kind, arg string) {
	if session == nil || session.state == nil {
		return
	}
	arg = strings.TrimSpace(arg)
	mode := kind
	requestedPath := arg
	if strings.EqualFold(arg, "full") {
		mode = codingcore.CodeVerificationFull
		requestedPath = ""
	}
	changeSet := codingcore.CurrentCodeChangeSet(session.state.Workspace, session.state.Turn, requestedPath)
	timeout := 2 * time.Minute
	if mode == codingcore.CodeVerificationFull {
		timeout = 5 * time.Minute
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	fmt.Fprintf(session.output, "Running %s verification for %d file(s)...\n", mode, len(changeSet.Files))
	receipt := codingcore.RunCodeVerification(
		ctx,
		session.state.Workspace,
		changeSet,
		"manual_"+kind,
		mode,
	)
	session.recordVerification(receipt)
	fmt.Fprintln(session.output, receipt.Summary(true))
}

func (session *CodingSession) runReviewCommand(arg string) {
	if session == nil || session.state == nil {
		return
	}
	fields := strings.Fields(strings.TrimSpace(arg))
	params := map[string]interface{}{"scope": "unstaged"}
	requestedPath := ""
	if len(fields) > 0 {
		switch strings.ToLower(fields[0]) {
		case "staged", "unstaged":
			params["scope"] = strings.ToLower(fields[0])
		case "branch":
			params["scope"] = "branch"
			if len(fields) > 1 {
				params["base_branch"] = fields[1]
			}
		case "file":
			if len(fields) < 2 {
				fmt.Fprintln(session.output, "Usage: /review [unstaged|staged|branch [base]|file <path>]")
				return
			}
			params["scope"] = "file"
			requestedPath = strings.Join(fields[1:], " ")
			params["file"] = requestedPath
		default:
			params["scope"] = "file"
			requestedPath = strings.TrimSpace(arg)
			params["file"] = requestedPath
		}
	}

	started := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	result, err := developmenttools.NewCodeReviewTool(session.state.Workspace).Execute(ctx, params)
	if err != nil {
		fmt.Fprintf(session.errors, "code review: %v\n", err)
		return
	}
	fmt.Fprintln(session.output, result)

	status := codingcore.CodeCheckPassed
	if strings.Contains(result, "No changes to review") {
		status = codingcore.CodeCheckSkipped
	} else if strings.Contains(result, "❌ Needs work") || strings.Contains(result, "Error getting diff") {
		status = codingcore.CodeCheckFailed
	}
	finished := time.Now()
	changeSet := codingcore.CurrentCodeChangeSet(session.state.Workspace, session.state.Turn, requestedPath)
	receipt := codingcore.CodeVerificationReceipt{
		ID:           fmt.Sprintf("review-%d", started.UnixNano()),
		Turn:         session.state.Turn,
		Trigger:      "manual_review",
		Status:       status,
		ChangedFiles: codingcore.CodeChangePaths(changeSet),
		StartedAt:    started,
		FinishedAt:   finished,
		Checks: []codingcore.CodeVerificationCheck{{
			Name:     "Static code review",
			Command:  "code_review " + getString(params, "scope", "unstaged"),
			Status:   status,
			Output:   truncate(result, 12000),
			Duration: finished.Sub(started).Round(time.Millisecond),
		}},
	}
	session.recordVerification(receipt)
}

func (session *CodingSession) recordVerification(receipt codingcore.CodeVerificationReceipt) {
	if session == nil || session.state == nil {
		return
	}
	session.state.Verifications = append(session.state.Verifications, receipt)
	if len(session.state.Verifications) > 100 {
		session.state.Verifications = append(
			[]codingcore.CodeVerificationReceipt(nil),
			session.state.Verifications[len(session.state.Verifications)-100:]...,
		)
	}
	session.state.UpdatedAt = time.Now()
	if err := saveCodeSession(session.state); err != nil {
		fmt.Fprintf(session.errors, "save verification receipt: %v\n", err)
	}
}

func (session *CodingSession) showInstructions(includeContent bool) {
	if session.llm == nil || session.llm.projectInstructions == nil {
		fmt.Fprintln(session.output, "Project instruction audit is unavailable for this agent.")
		return
	}
	instructions, err := session.llm.projectInstructions.Load(session.state.Workspace, session.state.Workspace)
	if err != nil {
		fmt.Fprintf(session.errors, "load project instructions: %v\n", err)
		return
	}
	fmt.Fprintln(session.output, instructions.AuditText())
	if includeContent {
		if prompt := instructions.Prompt(); prompt != "" {
			fmt.Fprintf(session.output, "\n%s\n", prompt)
		}
	}
}

func (session *CodingSession) showGit(args ...string) {
	output := gitCommandOutput(session.state.Workspace, args...)
	if strings.TrimSpace(output) == "" {
		fmt.Fprintln(session.output, "(no changes)")
		return
	}
	fmt.Fprintln(session.output, output)
}

func (session *CodingSession) undo(arg string) {
	if session.llm == nil || session.llm.snapshotManager == nil {
		fmt.Fprintln(session.output, "Checkpoints are unavailable for this agent.")
		return
	}
	manager := session.llm.snapshotManager
	target := -1
	if strings.TrimSpace(arg) != "" {
		parsed, err := strconv.Atoi(strings.TrimSpace(arg))
		if err != nil {
			fmt.Fprintln(session.output, "Usage: /undo [turn]")
			return
		}
		target = parsed
	} else {
		turns := manager.ListTurns()
		if len(turns) > 0 {
			target = turns[len(turns)-1].Turn
		}
	}
	if target < 0 {
		fmt.Fprintln(session.output, "No completed checkpoint turn is available.")
		return
	}
	count, err := manager.RewindToTurn(target)
	if err != nil {
		fmt.Fprintf(session.errors, "undo turn %d: %v\n", target, err)
		return
	}
	keepMessages := target * 2
	if keepMessages < 0 {
		keepMessages = 0
	}
	if keepMessages < len(session.state.Messages) {
		session.state.Messages = session.state.Messages[:keepMessages]
	}
	keptChanges := session.state.ChangeSets[:0]
	for _, changeSet := range session.state.ChangeSets {
		if changeSet.Turn <= target {
			keptChanges = append(keptChanges, changeSet)
		}
	}
	session.state.ChangeSets = keptChanges
	keptReceipts := session.state.Verifications[:0]
	for _, receipt := range session.state.Verifications {
		if receipt.Turn <= target {
			keptReceipts = append(keptReceipts, receipt)
		}
	}
	session.state.Verifications = keptReceipts
	session.state.Turn = target
	session.state.UpdatedAt = time.Now()
	if err := saveCodeSession(session.state); err != nil {
		fmt.Fprintf(session.errors, "save rewound session: %v\n", err)
	}
	fmt.Fprintf(session.output, "Rewound %d file(s) to the start of turn %d.\n", count, target)
}

func (session *CodingSession) handleProgress(event ProgressEvent) {
	session.mu.Lock()
	defer session.mu.Unlock()
	switch event.Type {
	case "tool_start":
		fmt.Fprintf(session.output, "  → %s\n", firstNonEmpty(event.ToolName, event.Message))
	case "tool_end":
		if event.Status == "error" {
			fmt.Fprintf(session.errors, "  × %s\n", event.Message)
		}
	case "heartbeat":
		fmt.Fprintf(session.output, "  … %s\n", event.Message)
	}
}

func saveCodeSession(state *CodeSessionState) error {
	if state == nil {
		return fmt.Errorf("code session state is nil")
	}
	state.Version = codeSessionStateVersion
	state.UpdatedAt = time.Now()
	path := codeSessionPath(state.RootWorkspace, state.ID)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return fsutil.WriteAtomic(path, data, 0644)
}

func loadCodeSession(root, id string) (*CodeSessionState, error) {
	id = strings.TrimSpace(id)
	if strings.EqualFold(id, "latest") {
		latest, err := latestCodeSessionID(root)
		if err != nil {
			return nil, err
		}
		id = latest
	}
	path := codeSessionPath(root, id)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var state CodeSessionState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}
	if state.ID == "" {
		return nil, fmt.Errorf("session file %s has no id", path)
	}
	if state.Version > codeSessionStateVersion {
		return nil, fmt.Errorf("session version %d is newer than supported version %d", state.Version, codeSessionStateVersion)
	}
	return &state, nil
}

func latestCodeSessionID(root string) (string, error) {
	dir := filepath.Join(projectctx.NormalizeInstructionPath(root), ".openkitty", "sessions", "code")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	type candidate struct {
		id      string
		modTime time.Time
	}
	var candidates []candidate
	for _, entry := range entries {
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".json") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		candidates = append(candidates, candidate{
			id:      strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name())),
			modTime: info.ModTime(),
		})
	}
	if len(candidates) == 0 {
		return "", fmt.Errorf("no saved code sessions under %s", dir)
	}
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].modTime.After(candidates[j].modTime)
	})
	return candidates[0].id, nil
}

func codeSessionPath(root, id string) string {
	id = sanitizeCodeSessionID(id)
	return filepath.Join(projectctx.NormalizeInstructionPath(root), ".openkitty", "sessions", "code", id+".json")
}

func sanitizeCodeSessionID(id string) string {
	id = strings.TrimSpace(id)
	var sanitized strings.Builder
	for _, char := range id {
		if (char >= 'a' && char <= 'z') ||
			(char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9') ||
			char == '-' || char == '_' {
			sanitized.WriteRune(char)
		} else {
			sanitized.WriteByte('_')
		}
	}
	if sanitized.Len() == 0 {
		return "session"
	}
	return sanitized.String()
}

func newCodeSessionID(now time.Time) string {
	return fmt.Sprintf("code-%s-%04x", now.Format("20060102-150405"), uint16(now.UnixNano()))
}

func gitCommandOutput(workspace string, args ...string) string {
	command := exec.Command("git", args...)
	command.Dir = workspace
	output, err := command.CombinedOutput()
	if err != nil && len(output) == 0 {
		return err.Error()
	}
	return strings.TrimSpace(string(output))
}

func sessionErrorWriter(session *CodingSession) io.Writer {
	if session != nil && session.errors != nil {
		return session.errors
	}
	return os.Stderr
}

func shouldPauseOnWindows(args []string) bool {
	return runtime.GOOS == "windows" && len(args) <= 1
}
