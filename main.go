package main

import (
	"context"
	"embed"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/flow/agent/react"
	"github.com/cloudwego/eino/schema"

	"MiniGoAgent/internal/config"
	"MiniGoAgent/internal/server"
	appsession "MiniGoAgent/internal/session"
	"MiniGoAgent/protocol"
	"MiniGoAgent/tools"
	"MiniGoAgent/tools/log"
)

//go:embed frontend/index.html
var frontendFS embed.FS

const systemPrompt = "你是密米尔，一个沉稳睿智的 AI 助手。思考步骤和调用工具时使用中文，但在输出最终回答时，请结合用户提问所使用的语言（中文或英文），使用对应的语言输出。不要同时输出中英文混合的内容。"

type promptProvider struct{}

func (p *promptProvider) SystemPrompt() string {
	return systemPrompt
}

type agentAdapter struct {
	agent *react.Agent
}

func (a *agentAdapter) Generate(ctx context.Context, msgs []*schema.Message) (*schema.Message, error) {
	return a.agent.Generate(ctx, msgs)
}

func (a *agentAdapter) Stream(ctx context.Context, msgs []*schema.Message) (*schema.StreamReader[*schema.Message], error) {
	return a.agent.Stream(ctx, msgs)
}

func main() {
	cfg := config.Load()
	ctx := context.Background()

	apiKey := cfg.OpenAI.APIKey
	baseURL := cfg.OpenAI.BaseURL
	if apiKey == "" || baseURL == "" {
		log.Fatal("请设置 OPENAI_API_KEY 和 OPENAI_BASE_URL")
	}
	if err := protocol.ValidateBaseURL(baseURL); err != nil {
		log.Fatal("BaseURL 校验失败: %v", err)
	}
	if err := protocol.ValidateProxyEnv(); err != nil {
		log.Warn("Proxy 环境变量异常: %v", err)
	}

	proto, err := protocol.New("openai", protocol.Config{
		APIKey:             apiKey,
		APIKeys:            cfg.OpenAI.APIKeys,
		BaseURL:            baseURL,
		Model:              cfg.OpenAI.Model,
		StreamTimeout:      cfg.OpenAI.StreamTimeout,
		RateLimitRPM:       cfg.OpenAI.RateLimitRPM,
		RateLimitTPM:       cfg.OpenAI.RateLimitTPM,
		ContextWarnPct:     cfg.Agent.ContextWarnPct,
		ContextCompressPct: cfg.Agent.ContextCompressPct,
		MaxReconnect:       cfg.Agent.MaxReconnect,
		FallbackModel:      cfg.OpenAI.FallbackModel,
		FallbackBaseURL:    cfg.OpenAI.FallbackBaseURL,
	})
	if err != nil {
		log.Fatal("创建 Protocol 失败: %v", err)
	}
	if cfg.Raw.RawLog {
		if p, ok := proto.(interface{ GetEventBus() *protocol.EventBus }); ok {
			rawLog := protocol.NewRawLogProcessor(cfg.Raw.RawLogDir)
			p.GetEventBus().Subscribe("raw", rawLog)
			log.Info("RAW 日志已启用: %s", cfg.Raw.RawLogDir)
		}
	}
	var mcpServer *protocol.MCPServer
	var usageTracker *protocol.UsageTracker
	if cfg.Raw.UsageDB {
		var utErr error
		usageTracker, utErr = protocol.NewUsageTracker(cfg.Raw.UsageDBPath)
		if utErr != nil {
			log.Warn("Usage 数据库初始化失败: %v", utErr)
		} else {
			type statsProvider interface{ GetTelemetry() *protocol.Telemetry }
			if p, ok := proto.(statsProvider); ok {
				p.GetTelemetry().SetTracker(usageTracker)
				log.Info("Usage 追踪已启用: %s", cfg.Raw.UsageDBPath)
			}
			mcpServer = protocol.NewMCPServer(usageTracker)
		}
	}
	llm := server.NewChatModel(proto, cfg.OpenAI.Model)

	terminalTool, _ := utils.InferTool("terminal", "在 Windows 终端执行 shell 命令", tools.RunTerminal)
	searchTool, _ := utils.InferTool("web_search", "在互联网上搜索信息", tools.WebSearch)
	compressTool, _ := utils.InferTool("compress", "压缩长文本内容，保留关键信息", tools.RunCompress)
	readFileTool, _ := utils.InferTool("read_file", "读取文件内容，可指定行范围", tools.ReadFile)
	writeFileTool, _ := utils.InferTool("write_file", "创建或覆写文件", tools.WriteFile)
	editFileTool, _ := utils.InferTool("edit_file", "在文件中查找替换文本", tools.EditFile)
	globTool, _ := utils.InferTool("glob", "按 glob 模式搜索文件", tools.GlobFiles)
	grepTool, _ := utils.InferTool("grep", "在文件中搜索文本", tools.GrepFiles)
	visionTool, _ := utils.InferTool("vision", "分析图片内容，支持URL和base64 data URI", tools.RunVision)

	pp := &promptProvider{}
	agent, err := react.NewAgent(ctx, &react.AgentConfig{
		ToolCallingModel:   llm,
		ToolsConfig:        compose.ToolsNodeConfig{Tools: []tool.BaseTool{terminalTool, searchTool, compressTool, readFileTool, writeFileTool, editFileTool, globTool, grepTool, visionTool}},
		MaxStep:            cfg.Agent.MaxStep,
		ToolReturnDirectly: map[string]struct{}{"compress": {}},
		MessageRewriter: func(ctx context.Context, msgs []*schema.Message) []*schema.Message {
			if len(msgs) <= 6 {
				return msgs
			}
			return msgs[len(msgs)-6:]
		},
		MessageModifier: func(ctx context.Context, input []*schema.Message) []*schema.Message {
			return append([]*schema.Message{schema.SystemMessage(pp.SystemPrompt())}, input...)
		},
	})
	if err != nil {
		log.Fatal("创建 Agent 失败: %v", err)
	}

	frontendData, err := frontendFS.ReadFile("frontend/index.html")
	if err != nil {
		log.Fatal("读取前端文件失败: %v", err)
	}
	srv := server.New(&agentAdapter{agent: agent}, llm, appsession.NewManager("default"), frontendData, pp)
	srv.LoadHistory()
	http.HandleFunc("/", srv.ServeFrontend)
	http.HandleFunc("/api/chat", srv.HandleChat)
	http.HandleFunc("/api/chat/stream", srv.HandleChatStream)
	http.HandleFunc("/api/vision", srv.HandleVision)
	http.HandleFunc("/api/vision/native", srv.HandleVisionNativeStream)
	if mcpServer != nil {
		http.Handle("/mcp", mcpServer)
		log.Info("MCP Server 已启用: ws://localhost:%s/mcp", cfg.Server.Port)
	}

	port := cfg.Server.Port
	log.Info("MiniGoAgent UI: http://localhost:%s", port)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		<-ctx.Done()
		log.Info("收到退出信号，正在保存历史...")
		srv.SaveHistory()
		if usageTracker != nil {
			if err := usageTracker.Close(); err != nil {
				log.Warn("关闭 Usage 数据库失败: %v", err)
			}
		}
		os.Exit(0)
	}()

	if err := http.ListenAndServe(":"+port, nil); err != nil {
		log.Fatal("HTTP server error: %v", err)
	}
}
