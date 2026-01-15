package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	crand "crypto/rand"
	"io"
	"net/http"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/term"
	"shelley.exe.dev/claudetool"
	"shelley.exe.dev/cli"
	"shelley.exe.dev/db"
	"shelley.exe.dev/llm"
	"shelley.exe.dev/loop"

	"shelley.exe.dev/models"
	"shelley.exe.dev/server"
	"shelley.exe.dev/templates"
	"shelley.exe.dev/coordinator"
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
	flag.StringVar(&global.Model, "model", "", "LLM model to use (default: from config or claude-opus-4.5)")
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

		fmt.Fprintf(flag.CommandLine.Output(), "  coord [flags]                 Start coordinator server\n")
		fmt.Fprintf(flag.CommandLine.Output(), "  dashboard [flags]             Start dashboard with coordinator management\n")
		fmt.Fprintf(flag.CommandLine.Output(), "  unpack-template <name> <dir>  Unpack a project template to a directory\n")
		fmt.Fprintf(flag.CommandLine.Output(), "  igor [flags]                  Start Igor file transfer server\n")
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

	case "coord":
		runCoord(global, args[1:])
	case "dashboard":
		runDashboard(global, args[1:])
	case "unpack-template":
		runUnpackTemplate(args[1:])
	case "igor":
		runIgor(args[1:])
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
	verbose := fs.Bool("verbose", false, "Show tool execution details")
	disableBrowser := fs.Bool("no-browser", false, "Disable browser tools (screenshots, navigation, etc.)")
	useDB := fs.Bool("sync", true, "Sync conversations with database (enables /conversations, /switch)")
	noSync := fs.Bool("no-sync", false, "Disable database sync (ephemeral conversation)")
	conversationID := fs.String("conversation", "", "Resume specific conversation by ID or slug")
	theme := fs.String("theme", "", "Color theme: dark, light, or auto (default: auto-detect)")
	fs.Parse(args)

	// Check for piped stdin
	var stdinContent string
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		data, err := io.ReadAll(os.Stdin)
		if err == nil && len(data) > 0 {
			stdinContent = string(data)
		}
	}

	// Use WARN level for chat mode to keep UI clean (unless debug is on)
	logger := setupLoggingWithLevel(global.Debug, slog.LevelWarn)

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
	// Priority: command-line flag > config file > hardcoded default
	modelID := global.Model
	if modelID == "" {
		if llmConfig.DefaultModel != "" {
			modelID = llmConfig.DefaultModel
		} else {
			modelID = models.Default().ID
		}
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

	// Generate system prompt (mark as CLI mode for Igor hint)
	server.IsCLIMode = true
	systemPrompt, err := server.GenerateSystemPrompt(wd)
	if err != nil {
		logger.Warn("Failed to generate system prompt", "error", err)
		systemPrompt = ""
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
		
		// Set up database for non-interactive mode if sync is enabled
		var database *db.DB
		if *useDB && !*noSync && global.DBPath != "" {
			var err error
			database, err = db.New(db.Config{DSN: global.DBPath})
			if err != nil {
				logger.Error("Failed to open database", "error", err)
				os.Exit(1)
			}
			defer database.Close()

			if err := database.Migrate(context.Background()); err != nil {
				logger.Error("Failed to migrate database", "error", err)
				os.Exit(1)
			}
		}
		
		runNonInteractiveChat(llmService, modelID, wd, system, fullPrompt, *yesMode, logger, database)
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
		EnableBrowser: !*disableBrowser,
		ModelManager:  manager,
		Theme:         *theme,
	}

	// Set up database if sync is enabled (default: true, unless -no-sync)
	if *useDB && !*noSync {
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
		logger.Error("-conversation requires sync (don't use -no-sync)")
		os.Exit(1)
	}

	if err := cli.Run(cliConfig); err != nil {
		logger.Error("CLI failed", "error", err)
		os.Exit(1)
	}
}

// buildUserMessageWithImages creates a user message, extracting any embedded image paths
func buildUserMessageWithImages(text string, maxImageDimension int) llm.Message {
	cleanText, imageContents, err := server.ExtractImagesFromMessage(text, maxImageDimension)
	if err != nil {
		// Fall back to plain text on error
		return llm.Message{
			Role:    llm.MessageRoleUser,
			Content: []llm.Content{{Type: llm.ContentTypeText, Text: text}},
		}
	}

	var contents []llm.Content
	if cleanText != "" {
		contents = append(contents, llm.Content{Type: llm.ContentTypeText, Text: cleanText})
	}
	contents = append(contents, imageContents...)

	if len(contents) == 0 {
		contents = append(contents, llm.Content{Type: llm.ContentTypeText, Text: text})
	}

	return llm.Message{
		Role:    llm.MessageRoleUser,
		Content: contents,
	}
}

func runNonInteractiveChat(llmService llm.Service, modelID, workingDir string, system []llm.SystemContent, prompt string, yesMode bool, logger *slog.Logger, database *db.DB) {
	ctx := context.Background()

	// Create toolset
	toolSetConfig := claudetool.ToolSetConfig{
		WorkingDir:     workingDir,
		ModelID:        modelID,
		SkipPermission: yesMode,
	}
	toolSet := claudetool.NewToolSet(ctx, toolSetConfig)
	defer toolSet.Cleanup()

	// Create conversation in database if available
	var conversationID string
	if database != nil {
		cwdPtr := &workingDir
		conv, err := database.CreateConversation(ctx, nil, true, cwdPtr)
		if err != nil {
			logger.Error("Failed to create conversation", "error", err)
		} else {
			conversationID = conv.ConversationID
			logger.Info("Created conversation", "id", conversationID)
		}
	}

	// Helper to get message type for database
	getMessageType := func(message llm.Message) db.MessageType {
		switch message.Role {
		case llm.MessageRoleUser:
			return db.MessageTypeUser
		case llm.MessageRoleAssistant:
			return db.MessageTypeAgent
		default:
			return db.MessageTypeUser
		}
	}

	// Create message recorder that prints to stdout and saves to DB
	recordMessage := func(ctx context.Context, message llm.Message, usage llm.Usage) error {
		// Print to stdout
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
		
		// Save to database if available
		if database != nil && conversationID != "" {
			_, err := database.CreateMessage(ctx, db.CreateMessageParams{
				ConversationID: conversationID,
				Type:           getMessageType(message),
				LLMData:        message,
				UsageData:      usage,
			})
			if err != nil {
				logger.Error("Failed to save message to database", "error", err)
			}
		}
		return nil
	}

	// Create and run loop
	loopInstance := loop.NewLoop(loop.Config{
		LLM:           llmService,
		History:       []llm.Message{},
		Tools:         toolSet.Tools(),
		RecordMessage: recordMessage,
		Logger:        logger,
		System:        system,
		WorkingDir:    workingDir,
		GetWorkingDir: toolSet.WorkingDir().Get,
	})

	// Queue the prompt, extracting any embedded images
	userMsg := buildUserMessageWithImages(prompt, llmService.MaxImageDimension())
	loopInstance.QueueUserMessage(userMsg)
	
	// Save the user message to database
	if database != nil && conversationID != "" {
		_, err := database.CreateMessage(ctx, db.CreateMessageParams{
			ConversationID: conversationID,
			Type:           db.MessageTypeUser,
			LLMData:        userMsg,
		})
		if err != nil {
			logger.Error("Failed to save user message", "error", err)
		}
	}

	// Process turns until done
	for {
		if err := loopInstance.ProcessOneTurn(ctx); err != nil {
			logger.Error("Failed to process turn", "error", err)
			os.Exit(1)
		}

		// Check if we're done (by checking if the last response ended the turn)
		history := loopInstance.GetHistory()
		if len(history) > 0 {
			lastMsg := history[len(history)-1]
			if lastMsg.Role == llm.MessageRoleAssistant && lastMsg.EndOfTurn {
				break
			}
		}
	}
	
	// Print conversation ID for reference
	if conversationID != "" {
		fmt.Fprintf(os.Stderr, "\n[Conversation: %s]\n", conversationID)
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
	return setupLoggingWithLevel(debug, slog.LevelInfo)
}

func setupLoggingWithLevel(debug bool, defaultLevel slog.Level) *slog.Logger {
	logLevel := defaultLevel
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

	// If no explicit config path provided, check the default location
	if configPath == "" {
		if home, err := os.UserHomeDir(); err == nil {
			defaultPath := filepath.Join(home, ".config", "shelley", "shelley.json")
			if _, err := os.Stat(defaultPath); err == nil {
				configPath = defaultPath
			}
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

func runIgor(args []string) {
	fs := flag.NewFlagSet("igor", flag.ExitOnError)
	port := fs.Int("port", 8099, "Port to listen on")
	dir := fs.String("dir", "", "Upload directory (default: ~/uploads)")
	fs.Parse(args)

	uploadDir := *dir
	if uploadDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error getting home dir: %v\n", err)
			os.Exit(1)
		}
		uploadDir = filepath.Join(home, "uploads")
	}

	if err := os.MkdirAll(uploadDir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "Error creating upload dir: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Igor awaits at :%d\n", *port)
	fmt.Printf("Files directory: %s\n", uploadDir)
	fmt.Printf("Open http://localhost:%d/\n", *port)

	server.RunIgor(*port, uploadDir)
}

func runCoord(global GlobalConfig, args []string) {
	fs := flag.NewFlagSet("coord", flag.ExitOnError)
	port := fs.Int("port", 8080, "HTTP server port")
	dbPath := fs.String("db", "coordinator.db", "SQLite database path")
	workerPrefix := fs.String("prefix", "wk", "Worker VM name prefix")
	maxWorkers := fs.Int("max-workers", 10, "Maximum workers allowed")
	shelleyBin := fs.String("shelley-bin", "", "Path to shelley binary (default: this binary)")
	coordHost := fs.String("host", "", "Coordinator hostname (auto-detected)")
	apiToken := fs.String("token", "", "API token (auto-generated if empty)")
	gitLogging := fs.Bool("git-log", true, "Enable git logging of completed tasks")
	gitToken := fs.String("git-token", "", "GitHub/GitLab token for worker git push (env: GITHUB_TOKEN)")
	gitUser := fs.String("git-user", "", "Git username for HTTPS auth (default: token owner)")
	shelleyDB := fs.String("shelley-db", "", "Path to main shelley DB for syncing conversations (enables viewing worker chats in main UI)")

	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "Usage: shelley coord [flags]\n\n")
		fmt.Fprintf(fs.Output(), "Start the coordinator server for distributed task execution.\n\n")
		fmt.Fprintf(fs.Output(), "Flags:\n")
		fs.PrintDefaults()
	}
	fs.Parse(args)

	// Auto-detect shelley binary path
	if *shelleyBin == "" {
		exe, err := os.Executable()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error getting executable path: %v\n", err)
			os.Exit(1)
		}
		*shelleyBin = exe
	}

	// Auto-detect hostname
	if *coordHost == "" {
		hostname, _ := os.Hostname()
		*coordHost = hostname + ".exe.xyz"
	}

	// Auto-generate token if not provided
	if *apiToken == "" {
		b := make([]byte, 16)
		if _, err := io.ReadFull(cryptoRand, b); err != nil {
			fmt.Fprintf(os.Stderr, "Error generating token: %v\n", err)
			os.Exit(1)
		}
		*apiToken = fmt.Sprintf("%x", b)
	}

	// Use environment variable for git token if not provided
	if *gitToken == "" {
		*gitToken = os.Getenv("GITHUB_TOKEN")
	}

	config := coordinator.Config{
		Port:         *port,
		DBPath:       *dbPath,
		WorkerPrefix: *workerPrefix,
		MaxWorkers:   *maxWorkers,
		ShelleyBin:   *shelleyBin,
		CoordHost:    *coordHost,
		APIToken:     *apiToken,
		GitLogging:   *gitLogging,
		GitToken:     *gitToken,
		GitUser:      *gitUser,
		ShelleyDB:    *shelleyDB,
	}

	coord, err := coordinator.New(config)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create coordinator: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Shelley Coordinator starting on port %d\n", *port)
	fmt.Printf("Database: %s\n", *dbPath)
	fmt.Printf("Host: %s\n", *coordHost)
	fmt.Printf("Git logging: %v\n", *gitLogging)
	fmt.Println()
	fmt.Println("=== API TOKEN ===")
	fmt.Println(*apiToken)
	fmt.Println("=================")

	// Set up HTTP routes
	mux := http.NewServeMux()
	mux.HandleFunc("/", coord.HandleIndex)
	mux.HandleFunc("/api/enqueue", coord.HandleEnqueue)
	mux.HandleFunc("/api/tasks", coord.HandleListTasks)
	mux.HandleFunc("/api/task", coord.HandleGetTask)
	mux.HandleFunc("/api/workers", coord.HandleListWorkers)
	mux.HandleFunc("/api/scale", coord.HandleScale)
	mux.HandleFunc("/api/drain", coord.HandleDrain)
	mux.HandleFunc("/api/stats", coord.HandleStats)
	mux.HandleFunc("/api/next-task", coord.HandleNextTask)
	mux.HandleFunc("/api/complete", coord.HandleComplete)
	mux.HandleFunc("/api/worker-shutdown", coord.HandleWorkerShutdown)
	mux.HandleFunc("/api/groups", coord.HandleListGroups)
	mux.HandleFunc("/api/group", coord.HandleGetGroup)
	mux.HandleFunc("/api/group/create", coord.HandleCreateGroup)
	mux.HandleFunc("/api/group/tasks", coord.HandleGetGroupTasks)
	mux.HandleFunc("/shelley-bin", coord.HandleShelleyBinary)
	mux.HandleFunc("/api/sync-conversation", coord.HandleSyncConversation)

	addr := fmt.Sprintf(":%d", *port)
	if err := http.ListenAndServe(addr, mux); err != nil {
		fmt.Fprintf(os.Stderr, "Server error: %v\n", err)
		os.Exit(1)
	}
}

var cryptoRand io.Reader = nil
func runDashboard(global GlobalConfig, args []string) {
	fs := flag.NewFlagSet("dashboard", flag.ExitOnError)
	port := fs.Int("port", 8080, "Dashboard HTTP server port")
	coordPort := fs.Int("coord-port", 8081, "Coordinator internal port")
	dbPath := fs.String("db", "coordinator.db", "SQLite database path")
	workerPrefix := fs.String("prefix", "wk", "Worker VM name prefix")
	maxWorkers := fs.Int("max-workers", 10, "Maximum workers allowed")
	shelleyBin := fs.String("shelley-bin", "", "Path to shelley binary (default: this binary)")
	coordHost := fs.String("host", "", "Coordinator hostname (auto-detected)")
	apiToken := fs.String("token", "", "API token (auto-generated if empty)")
	autoStart := fs.Bool("auto-start", false, "Automatically start coordinator on dashboard startup")
	gitTokenDash := fs.String("git-token", "", "GitHub/GitLab token for worker git push (env: GITHUB_TOKEN)")
	gitUserDash := fs.String("git-user", "", "Git username for HTTPS auth (default: token owner)")
	shelleyDBDash := fs.String("shelley-db", "/home/exedev/.config/shelley/shelley.db", "Path to main shelley DB for syncing conversations")

	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "Usage: shelley dashboard [flags]\n\n")
		fmt.Fprintf(fs.Output(), "Start the dashboard server with coordinator management.\n\n")
		fmt.Fprintf(fs.Output(), "Flags:\n")
		fs.PrintDefaults()
	}
	fs.Parse(args)

	// Auto-detect shelley binary path
	if *shelleyBin == "" {
		exe, err := os.Executable()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error getting executable path: %v\n", err)
			os.Exit(1)
		}
		*shelleyBin = exe
	}

	// Auto-detect hostname
	if *coordHost == "" {
		hostname, _ := os.Hostname()
		*coordHost = hostname + ".exe.xyz"
	}

	// Auto-generate token if not provided
	if *apiToken == "" {
		b := make([]byte, 16)
		if _, err := io.ReadFull(cryptoRand, b); err != nil {
			fmt.Fprintf(os.Stderr, "Error generating token: %v\n", err)
			os.Exit(1)
		}
		*apiToken = fmt.Sprintf("%x", b)
	}

	// Use environment variable for git token if not provided
	if *gitTokenDash == "" {
		*gitTokenDash = os.Getenv("GITHUB_TOKEN")
	}

	config := coordinator.DashboardConfig{
		Port:         *port,
		CoordPort:    *coordPort,
		CoordDBPath:  *dbPath,
		ShelleyBin:   *shelleyBin,
		MaxWorkers:   *maxWorkers,
		WorkerPrefix: *workerPrefix,
		CoordHost:    *coordHost,
		APIToken:     *apiToken,
		GitToken:     *gitTokenDash,
		GitUser:      *gitUserDash,
		ShelleyDB:    *shelleyDBDash,
	}

	dash := coordinator.NewDashboard(config)

	if *autoStart {
		if err := dash.Start(); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to auto-start coordinator: %v\n", err)
		}
	}

	if err := dash.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Dashboard error: %v\n", err)
		os.Exit(1)
	}
}

func init() {
	cryptoRand = cryptoRandReader{}
}

type cryptoRandReader struct{}

func (cryptoRandReader) Read(b []byte) (int, error) {
	return io.ReadFull(crand.Reader, b)
}
