package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"strconv"
	"strings"

	"golang.org/x/term"
	"shelley.exe.dev/claudetool"
	"shelley.exe.dev/cli"
	"shelley.exe.dev/db"
	"shelley.exe.dev/llm"
	"shelley.exe.dev/loop"
	"shelley.exe.dev/memory"
	"shelley.exe.dev/models"
	"shelley.exe.dev/server"
	"shelley.exe.dev/templates"
	"shelley.exe.dev/version"
)

type GlobalConfig struct {
	DBPath          string
	Debug           bool
	Model           string
	PredictableOnly bool
	ConfigPath      string
	TerminalURL     string
	DefaultModel    string
}

func main() {
	// Define global flags
	var global GlobalConfig
	defaultModelID := models.Default().ID
	flag.StringVar(&global.DBPath, "db", "shelley.db", "Path to SQLite database file")
	flag.BoolVar(&global.Debug, "debug", false, "Enable debug logging")
	flag.StringVar(&global.Model, "model", defaultModelID, "LLM model to use (use 'predictable' for testing)")
	flag.BoolVar(&global.PredictableOnly, "predictable-only", false, "Use only the predictable service, ignoring all other models")
	flag.StringVar(&global.ConfigPath, "config", "", "Path to shelley.json configuration file (optional)")
	flag.StringVar(&global.DefaultModel, "default-model", defaultModelID, "Default model for web UI")

	// Custom usage function
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "Usage: %s [global-flags] <command> [command-flags]\n\n", os.Args[0])
		fmt.Fprintf(flag.CommandLine.Output(), "Global flags:\n")
		flag.PrintDefaults()
		fmt.Fprintf(flag.CommandLine.Output(), "\nCommands:\n")
		fmt.Fprintf(flag.CommandLine.Output(), "  chat [flags]                  Start interactive CLI chat mode\n")
		fmt.Fprintf(flag.CommandLine.Output(), "  serve [flags]                 Start the web server\n")
		fmt.Fprintf(flag.CommandLine.Output(), "  memory <add|list|remove>      Manage persistent memory\n")
		fmt.Fprintf(flag.CommandLine.Output(), "  unpack-template <name> <dir>  Unpack a project template to a directory\n")
		fmt.Fprintf(flag.CommandLine.Output(), "  version                       Print version information as JSON\n")
		fmt.Fprintf(flag.CommandLine.Output(), "\nUse '%s <command> -h' for command-specific help\n", os.Args[0])
	}

	// Parse all flags first
	flag.Parse()
	args := flag.Args()

	// Apply seccomp filter early, before spawning any child processes.
	// This prevents child processes from killing shelley.
	// Turns out this doesn't work, because it blocks sudo, which we want to work.
	// if err := seccomp.BlockKillSelf(); err != nil {
	// 	slog.Info("seccomp filter not installed", "error", err)
	// }

	if len(args) == 0 {
		flag.Usage()
		os.Exit(1)
	}

	command := args[0]
	switch command {
	case "chat":
		runChat(global, args[1:])
	case "serve":
		runServe(global, args[1:])
	case "memory":
		runMemory(args[1:])
	case "unpack-template":
		runUnpackTemplate(args[1:])
	case "version":
		runVersion()
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", command)
		flag.Usage()
		os.Exit(1)
	}
}

func runChat(global GlobalConfig, args []string) {
	fs := flag.NewFlagSet("chat", flag.ExitOnError)
	prompt := fs.String("prompt", "", "Initial prompt to send (optional, for non-interactive mode)")
	yesMode := fs.Bool("yes", false, "Auto-accept all tool operations (no confirmations)")
	verbose := fs.Bool("verbose", false, "Show tool execution details (off by default)")
	enableBrowser := fs.Bool("browser", false, "Enable browser tools (screenshots, navigation, etc.)")
	useDB := fs.Bool("sync", false, "Sync conversations with database (enables /conversations, /switch)")
	conversationID := fs.String("conversation", "", "Resume specific conversation by ID or slug (requires -sync)")
	fs.Parse(args)

	// Check for piped stdin
	var stdinContent string
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		data, err := io.ReadAll(os.Stdin)
		if err == nil && len(data) > 0 {
			stdinContent = string(data)
		}
	}

	logger := setupLogging(global.Debug)

	// Build LLM configuration
	llmConfig := buildLLMConfig(logger, global.ConfigPath, global.TerminalURL, global.DefaultModel)

	// Create LLM service
	modelConfig := &models.Config{
		AnthropicAPIKey: llmConfig.AnthropicAPIKey,
		OpenAIAPIKey:    llmConfig.OpenAIAPIKey,
		GeminiAPIKey:    llmConfig.GeminiAPIKey,
		FireworksAPIKey: llmConfig.FireworksAPIKey,
		Gateway:         llmConfig.Gateway,
		Logger:          logger,
	}

	manager, err := models.NewManager(modelConfig, nil)
	if err != nil {
		logger.Error("Failed to create models manager", "error", err)
		os.Exit(1)
	}

	// Determine which model to use
	modelID := global.Model
	if modelID == "" {
		modelID = models.Default().ID
	}

	llmService, err := manager.GetService(modelID)
	if err != nil {
		logger.Error("Failed to get LLM service", "model", modelID, "error", err)
		fmt.Fprintf(os.Stderr, "Failed to get LLM service for model %s: %v\n", modelID, err)
		fmt.Fprintf(os.Stderr, "Available models: %s\n", strings.Join(manager.GetAvailableModels(), ", "))
		os.Exit(1)
	}

	// Get working directory
	wd, err := os.Getwd()
	if err != nil {
		wd = "/"
	}

	// Generate system prompt
	systemPrompt, err := server.GenerateSystemPrompt(wd)
	if err != nil {
		logger.Warn("Failed to generate system prompt", "error", err)
		systemPrompt = ""
	}

	// Append memory to system prompt
	memStore, err := memory.Load()
	if err != nil {
		logger.Warn("Failed to load memory", "error", err)
	} else {
		systemPrompt += memStore.ForSystemPrompt()
	}

	var system []llm.SystemContent
	if systemPrompt != "" {
		system = []llm.SystemContent{{Type: "text", Text: systemPrompt}}
	}

	// Non-interactive mode with prompt or piped input
	if *prompt != "" || stdinContent != "" {
		fullPrompt := *prompt
		if stdinContent != "" {
			if fullPrompt != "" {
				fullPrompt = stdinContent + "\n\n" + fullPrompt
			} else {
				fullPrompt = stdinContent
			}
		}
		runNonInteractiveChat(llmService, modelID, wd, system, fullPrompt, *yesMode, logger)
		return
	}

	// Interactive CLI mode
	cliConfig := cli.Config{
		Model:         modelID,
		WorkingDir:    wd,
		LLMService:    llmService,
		Logger:        logger,
		System:        system,
		Verbose:       *verbose,
		EnableBrowser: *enableBrowser,
		ModelManager:  manager,
	}

	// Set up database if sync is enabled
	if *useDB {
		database, err := db.New(db.Config{DSN: global.DBPath})
		if err != nil {
			logger.Error("Failed to open database", "error", err)
			os.Exit(1)
		}
		defer database.Close()

		if err := database.Migrate(context.Background()); err != nil {
			logger.Error("Failed to migrate database", "error", err)
			os.Exit(1)
		}

		cliConfig.DB = database

		// Resolve conversation ID if provided
		if *conversationID != "" {
			// Try to find by ID first, then by slug
			conv, err := database.GetConversationByID(context.Background(), *conversationID)
			if err != nil {
				conv, err = database.GetConversationBySlug(context.Background(), *conversationID)
				if err != nil {
					logger.Error("Conversation not found", "id", *conversationID)
					os.Exit(1)
				}
			}
			cliConfig.ConversationID = conv.ConversationID
			logger.Info("Resuming conversation", "id", conv.ConversationID)
		}
	} else if *conversationID != "" {
		logger.Error("-conversation requires -sync flag")
		os.Exit(1)
	}

	if err := cli.Run(cliConfig); err != nil {
		logger.Error("CLI failed", "error", err)
		os.Exit(1)
	}
}

func runNonInteractiveChat(llmService llm.Service, modelID, workingDir string, system []llm.SystemContent, prompt string, yesMode bool, logger *slog.Logger) {
	ctx := context.Background()

	// Create toolset
	toolSetConfig := claudetool.ToolSetConfig{
		WorkingDir:     workingDir,
		ModelID:        modelID,
		SkipPermission: yesMode,
	}
	toolSet := claudetool.NewToolSet(ctx, toolSetConfig)
	defer toolSet.Cleanup()

	// Create message recorder that prints to stdout
	recordMessage := func(ctx context.Context, message llm.Message, usage llm.Usage) error {
		for _, content := range message.Content {
			switch content.Type {
			case llm.ContentTypeText:
				if message.Role == llm.MessageRoleAssistant && content.Text != "" {
					fmt.Println(content.Text)
				}
			case llm.ContentTypeToolUse:
				fmt.Printf("[Tool: %s]\n", content.ToolName)
			case llm.ContentTypeToolResult:
				if content.ToolError {
					fmt.Printf("[Tool Error]\n")
				}
			}
		}
		return nil
	}

	// Create and run loop
	loop := loop.NewLoop(loop.Config{
		LLM:           llmService,
		History:       []llm.Message{},
		Tools:         toolSet.Tools(),
		RecordMessage: recordMessage,
		Logger:        logger,
		System:        system,
		WorkingDir:    workingDir,
		GetWorkingDir: toolSet.WorkingDir().Get,
	})

	// Queue the prompt
	userMsg := llm.Message{
		Role:    llm.MessageRoleUser,
		Content: []llm.Content{{Type: llm.ContentTypeText, Text: prompt}},
	}
	loop.QueueUserMessage(userMsg)

	// Process turns until done
	for {
		if err := loop.ProcessOneTurn(ctx); err != nil {
			logger.Error("Failed to process turn", "error", err)
			os.Exit(1)
		}

		// Check if we're done (by checking if the last response ended the turn)
		history := loop.GetHistory()
		if len(history) > 0 {
			lastMsg := history[len(history)-1]
			if lastMsg.Role == llm.MessageRoleAssistant && lastMsg.EndOfTurn {
				break
			}
		}
	}
}

func runServe(global GlobalConfig, args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	port := fs.String("port", "9000", "Port to listen on")
	systemdActivation := fs.Bool("systemd-activation", false, "Use systemd socket activation (listen on fd from systemd)")
	requireHeader := fs.String("require-header", "", "Require this header on all API requests (e.g., X-Exedev-Userid)")
	fs.Parse(args)

	logger := setupLogging(global.Debug)

	database := setupDatabase(global.DBPath, logger)
	defer database.Close()

	// Set the database path for system prompt generation
	server.DBPath = global.DBPath

	// Build LLM configuration
	llmConfig := buildLLMConfig(logger, global.ConfigPath, global.TerminalURL, global.DefaultModel)

	// Create request history for debugging
	llmHistory := models.NewLLMRequestHistory(10)

	// Initialize LLM service manager
	llmManager := server.NewLLMServiceManager(llmConfig, llmHistory)

	// Log available models
	availableModels := llmManager.GetAvailableModels()
	logger.Info("Available models", "models", strings.Join(availableModels, ", "))

	toolSetConfig := setupToolSetConfig(llmManager)

	// Create server
	svr := server.NewServer(database, llmManager, toolSetConfig, logger, global.PredictableOnly, llmConfig.TerminalURL, llmConfig.DefaultModel, *requireHeader, llmConfig.Links)

	var err error
	if *systemdActivation {
		listener, listenerErr := systemdListener()
		if listenerErr != nil {
			logger.Error("Failed to get systemd listener", "error", listenerErr)
			os.Exit(1)
		}
		logger.Info("Using systemd socket activation")
		err = svr.StartWithListener(listener)
	} else {
		err = svr.Start(*port)
	}

	if err != nil {
		logger.Error("Server failed", "error", err)
		os.Exit(1)
	}
}

func setupLogging(debug bool) *slog.Logger {
	logLevel := slog.LevelInfo
	if debug {
		logLevel = slog.LevelDebug
	}
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: logLevel,
	}))
	slog.SetDefault(logger)
	return logger
}

func setupDatabase(dbPath string, logger *slog.Logger) *db.DB {
	database, err := db.New(db.Config{DSN: dbPath})
	if err != nil {
		logger.Error("Failed to initialize database", "error", err)
		os.Exit(1)
	}

	// Run database migrations
	if err := database.Migrate(context.Background()); err != nil {
		logger.Error("Failed to run database migrations", "error", err)
		os.Exit(1)
	}
	logger.Debug("Database migrations completed successfully")
	return database
}

// runUnpackTemplate unpacks a project template to a directory
func runUnpackTemplate(args []string) {
	fs := flag.NewFlagSet("unpack-template", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "Usage: shelley unpack-template <template-name> <directory>\n\n")
		fmt.Fprintf(fs.Output(), "Unpacks a project template to the specified directory.\n\n")
		fmt.Fprintf(fs.Output(), "Available templates:\n")
		names, err := templates.List()
		if err != nil {
			fmt.Fprintf(fs.Output(), "  (error listing templates: %v)\n", err)
		} else if len(names) == 0 {
			fmt.Fprintf(fs.Output(), "  (no templates available)\n")
		} else {
			for _, name := range names {
				fmt.Fprintf(fs.Output(), "  %s\n", name)
			}
		}
	}
	fs.Parse(args)

	if fs.NArg() < 2 {
		fs.Usage()
		os.Exit(1)
	}

	templateName := fs.Arg(0)
	destDir := fs.Arg(1)

	// Verify template exists
	names, err := templates.List()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error listing templates: %v\n", err)
		os.Exit(1)
	}
	found := false
	for _, name := range names {
		if name == templateName {
			found = true
			break
		}
	}
	if !found {
		fmt.Fprintf(os.Stderr, "Error: template %q not found\n", templateName)
		fmt.Fprintf(os.Stderr, "Available templates: %s\n", strings.Join(names, ", "))
		os.Exit(1)
	}

	// Create destination directory if it doesn't exist
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "Error creating directory %q: %v\n", destDir, err)
		os.Exit(1)
	}

	// Unpack the template
	if err := templates.Unpack(templateName, destDir); err != nil {
		fmt.Fprintf(os.Stderr, "Error unpacking template: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Template %q unpacked to %s\n", templateName, destDir)
}

// runMemory handles the memory subcommand
func runMemory(args []string) {
	if len(args) == 0 {
		fmt.Fprintf(os.Stderr, "Usage: shelley memory <add|list|remove> [args]\n")
		fmt.Fprintf(os.Stderr, "\nSubcommands:\n")
		fmt.Fprintf(os.Stderr, "  add <text>     Add a memory entry\n")
		fmt.Fprintf(os.Stderr, "  list           List all memory entries\n")
		fmt.Fprintf(os.Stderr, "  remove <index> Remove a memory entry by index\n")
		os.Exit(1)
	}

	store, err := memory.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading memory: %v\n", err)
		os.Exit(1)
	}

	switch args[0] {
	case "add":
		if len(args) < 2 {
			fmt.Fprintf(os.Stderr, "Usage: shelley memory add <text>\n")
			os.Exit(1)
		}
		text := strings.Join(args[1:], " ")
		store.Add(text)
		if err := store.Save(); err != nil {
			fmt.Fprintf(os.Stderr, "Error saving memory: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("Memory added.")

	case "list":
		if len(store.Entries) == 0 {
			fmt.Println("No memories stored.")
			return
		}
		for i, entry := range store.Entries {
			fmt.Printf("%d: %s\n", i, entry.Text)
		}

	case "remove":
		if len(args) < 2 {
			fmt.Fprintf(os.Stderr, "Usage: shelley memory remove <index>\n")
			os.Exit(1)
		}
		index, err := strconv.Atoi(args[1])
		if err != nil {
			fmt.Fprintf(os.Stderr, "Invalid index: %s\n", args[1])
			os.Exit(1)
		}
		if err := store.Remove(index); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		if err := store.Save(); err != nil {
			fmt.Fprintf(os.Stderr, "Error saving memory: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("Memory removed.")

	default:
		fmt.Fprintf(os.Stderr, "Unknown memory subcommand: %s\n", args[0])
		os.Exit(1)
	}
}

// runVersion prints version information as JSON
func runVersion() {
	info := version.GetInfo()
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(info); err != nil {
		fmt.Fprintf(os.Stderr, "Error encoding version: %v\n", err)
		os.Exit(1)
	}
}

func setupToolSetConfig(llmProvider claudetool.LLMServiceProvider) claudetool.ToolSetConfig {
	wd, err := os.Getwd()
	if err != nil {
		// Fallback to "/" if we can't get working directory
		wd = "/"
	}
	return claudetool.ToolSetConfig{
		WorkingDir:       wd,
		LLMProvider:      llmProvider,
		EnableJITInstall: claudetool.EnableBashToolJITInstall,
		EnableBrowser:    true,
	}
}

// buildLLMConfig constructs LLMConfig from environment variables and optional config file
func buildLLMConfig(logger *slog.Logger, configPath, terminalURL, defaultModel string) *server.LLMConfig {
	llmCfg := &server.LLMConfig{
		AnthropicAPIKey: os.Getenv("ANTHROPIC_API_KEY"),
		OpenAIAPIKey:    os.Getenv("OPENAI_API_KEY"),
		GeminiAPIKey:    os.Getenv("GEMINI_API_KEY"),
		FireworksAPIKey: os.Getenv("FIREWORKS_API_KEY"),
		TerminalURL:     terminalURL,
		DefaultModel:    defaultModel,
		Logger:          logger,
	}

	// Check for gateway from environment variable
	if gateway := os.Getenv("LLM_GATEWAY"); gateway != "" {
		gateway = strings.TrimSuffix(gateway, "/")
		llmCfg.Gateway = gateway
		logger.Info("Using LLM gateway from environment", "gateway", gateway)

		// When using a gateway, default all API keys to "implicit" unless otherwise set
		if llmCfg.AnthropicAPIKey == "" {
			llmCfg.AnthropicAPIKey = "implicit"
		}
		if llmCfg.OpenAIAPIKey == "" {
			llmCfg.OpenAIAPIKey = "implicit"
		}
		if llmCfg.GeminiAPIKey == "" {
			llmCfg.GeminiAPIKey = "implicit"
		}
		if llmCfg.FireworksAPIKey == "" {
			llmCfg.FireworksAPIKey = "implicit"
		}
	}

	if configPath != "" {
		data, err := os.ReadFile(configPath)
		if err != nil {
			if !os.IsNotExist(err) {
				logger.Warn("Failed to read config file", "path", configPath, "error", err)
			}
			return llmCfg
		}

		var cfg struct {
			LLMGateway   string        `json:"llm_gateway"`
			TerminalURL  string        `json:"terminal_url"`
			DefaultModel string        `json:"default_model"`
			Links        []server.Link `json:"links"`
		}
		if err := json.Unmarshal(data, &cfg); err != nil {
			logger.Warn("Failed to parse config file", "path", configPath, "error", err)
			return llmCfg
		}

		// Config file gateway overrides environment variable
		if cfg.LLMGateway != "" {
			gateway := strings.TrimSuffix(cfg.LLMGateway, "/")
			if llmCfg.Gateway != gateway {
				llmCfg.Gateway = gateway
				logger.Info("Using LLM gateway from config", "gateway", gateway)

				// When using a gateway, default all API keys to "implicit" unless otherwise set
				if llmCfg.AnthropicAPIKey == "" {
					llmCfg.AnthropicAPIKey = "implicit"
				}
				if llmCfg.OpenAIAPIKey == "" {
					llmCfg.OpenAIAPIKey = "implicit"
				}
				if llmCfg.GeminiAPIKey == "" {
					llmCfg.GeminiAPIKey = "implicit"
				}
				if llmCfg.FireworksAPIKey == "" {
					llmCfg.FireworksAPIKey = "implicit"
				}
			}
		}

		// Override terminal URL from config file if present and not already set via flag
		if cfg.TerminalURL != "" && llmCfg.TerminalURL == "" {
			llmCfg.TerminalURL = cfg.TerminalURL
			logger.Info("Using terminal URL from config", "url", cfg.TerminalURL)
		}

		// Override default model from config file if present and not already set via flag
		if cfg.DefaultModel != "" && llmCfg.DefaultModel == "" {
			llmCfg.DefaultModel = cfg.DefaultModel
			logger.Info("Using default model from config", "model", cfg.DefaultModel)
		}

		// Load links from config file if present
		if len(cfg.Links) > 0 {
			llmCfg.Links = cfg.Links
			logger.Info("Loaded links from config", "count", len(cfg.Links))
		}
	}

	return llmCfg
}

// systemdListener returns a net.Listener from systemd socket activation.
// Systemd passes file descriptors starting at fd 3, with LISTEN_FDS indicating the count.
func systemdListener() (net.Listener, error) {
	// Check LISTEN_PID matches our PID (optional but recommended)
	pidStr := os.Getenv("LISTEN_PID")
	if pidStr != "" {
		pid, err := strconv.Atoi(pidStr)
		if err != nil {
			return nil, fmt.Errorf("invalid LISTEN_PID: %w", err)
		}
		if pid != os.Getpid() {
			return nil, fmt.Errorf("LISTEN_PID %d does not match current PID %d", pid, os.Getpid())
		}
	}

	// Get the number of file descriptors passed
	fdsStr := os.Getenv("LISTEN_FDS")
	if fdsStr == "" {
		return nil, fmt.Errorf("LISTEN_FDS not set; not running under systemd socket activation")
	}
	nfds, err := strconv.Atoi(fdsStr)
	if err != nil {
		return nil, fmt.Errorf("invalid LISTEN_FDS: %w", err)
	}
	if nfds < 1 {
		return nil, fmt.Errorf("LISTEN_FDS=%d; expected at least 1", nfds)
	}

	// Systemd passes file descriptors starting at fd 3
	const listenFDsStart = 3
	fd := listenFDsStart

	// Create a file from the descriptor
	f := os.NewFile(uintptr(fd), "systemd-socket")
	if f == nil {
		return nil, fmt.Errorf("failed to create file from fd %d", fd)
	}

	// Create a listener from the file
	listener, err := net.FileListener(f)
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("failed to create listener from fd %d: %w", fd, err)
	}

	// Close the original file; the listener now owns the descriptor
	f.Close()

	return listener, nil
}
