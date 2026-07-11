package main

import (
	"context"
	"embed"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/cloudwego/eino/schema"

	"MiniGoAgent/internal/adk"
	"MiniGoAgent/internal/adk/convert"
	"MiniGoAgent/internal/adk/event"
	"MiniGoAgent/internal/adk/llm"
	adktool "MiniGoAgent/internal/adk/tool"
	adktypes "MiniGoAgent/internal/adk/types"
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
	runner *adk.Runner
}

func (a *agentAdapter) Generate(ctx context.Context, msgs []*schema.Message) (*schema.Message, error) {
	adkMsgs := convert.FromEinoSlice(msgs)
	resp, err := a.runner.Run(ctx, &adktypes.Request{Messages: adkMsgs})
	if err != nil {
		return nil, err
	}
	if len(resp.Messages) == 0 {
		return &schema.Message{Role: schema.Assistant, Content: ""}, nil
	}
	return convert.ToEino(resp.Messages[len(resp.Messages)-1]), nil
}

func (a *agentAdapter) Stream(ctx context.Context, msgs []*schema.Message) (*schema.StreamReader[*schema.Message], error) {
	adkMsgs := convert.FromEinoSlice(msgs)
	events, err := a.runner.Stream(ctx, &adktypes.Request{Messages: adkMsgs})
	if err != nil {
		return nil, err
	}

	sr, sw := schema.Pipe[*schema.Message](64)
	go func() {
		defer sw.Close()
		for {
			select {
			case <-ctx.Done():
				return
			case evt, ok := <-events:
				if !ok {
					return
				}
				switch evt.Type {
				case adktypes.EventText:
					if sw.Send(&schema.Message{Role: schema.Assistant, Content: evt.Content}, nil) {
						return
					}
				case adktypes.EventReasoning:
					if sw.Send(&schema.Message{Role: schema.Assistant, ReasoningContent: evt.Content}, nil) {
						return
					}
				case adktypes.EventError:
					return
				}
			}
		}
	}()
	return sr, nil
}

func main() {
	cfg := config.Load()
	ctx := context.Background()
	tools.SetWorkspaceRoot(cfg.Server.WorkspaceRoot)

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

	br, err := llm.NewBridgeFromConfig(&cfg)
	if err != nil {
		log.Fatal("创建 LLM Bridge 失败: %v", err)
	}
	proto := br.Protocol()

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
			type telProvider interface{ GetTelemetry() *protocol.Telemetry }
			if p, ok := proto.(telProvider); ok {
				p.GetTelemetry().SetTracker(usageTracker)
				log.Info("Usage 追踪已启用: %s", cfg.Raw.UsageDBPath)
			}
			mcpServer = protocol.NewMCPServer(usageTracker)
		}
	}

	reg := adktool.NewToolRegistry()
	registerTool(reg, "terminal", "在 Windows 终端执行 shell 命令", tools.RunTerminal)
	registerTool(reg, "web_search", "在互联网上搜索信息", tools.WebSearch)
	registerTool(reg, "compress", "压缩长文本内容，保留关键信息", tools.RunCompress)
	registerTool(reg, "read_file", "读取文件内容，可指定行范围", tools.ReadFile)
	registerTool(reg, "write_file", "创建或覆写文件", tools.WriteFile)
	registerTool(reg, "edit_file", "在文件中查找替换文本", tools.EditFile)
	registerTool(reg, "glob", "按 glob 模式搜索文件", tools.GlobFiles)
	registerTool(reg, "grep", "在文件中搜索文本", tools.GrepFiles)
	registerTool(reg, "vision", "分析图片内容，支持URL和base64 data URI", tools.RunVision)

	toolNames := []string{"terminal", "web_search", "compress", "read_file", "write_file", "edit_file", "glob", "grep", "vision"}

	agent, err := adk.NewReactAgent(ctx, &adk.AgentConfig{
		Bridge:    br,
		Tools:     reg,
		ToolNames: toolNames,
		Prompt:    systemPrompt,
		MaxSteps:  cfg.Agent.MaxStep,
		// Middleware 和 Guardrails 默认 nil，不启用；如需启用需显式配置
	})
	if err != nil {
		log.Fatal("创建 Agent 失败: %v", err)
	}

	bus := event.NewBus()
	runner := adk.NewRunnerWithAgent(agent, nil, nil, nil, bus)

	frontendData, err := frontendFS.ReadFile("frontend/index.html")
	if err != nil {
		log.Fatal("读取前端文件失败: %v", err)
	}
	sessionMgr := appsession.NewManager("default")
	sessionMgr.StartCleanup(ctx, 5*time.Minute, 30*time.Minute)
	srv := server.New(&agentAdapter{runner: runner}, br, sessionMgr, frontendData, &promptProvider{})
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

	httpSrv := &http.Server{Addr: ":" + port, Handler: nil}

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
		protocol.StopHealthManager()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(shutdownCtx)
	}()

	if err := httpSrv.ListenAndServe(); err != http.ErrServerClosed {
		log.Fatal("HTTP server error: %v", err)
	}
}

func registerTool[T, D any](reg *adktool.ToolRegistry, name, desc string, fn func(ctx context.Context, input T) (D, error)) {
	t, err := adktool.NewFromFn(name, desc, fn)
	if err != nil {
		log.Fatal("注册工具 %s 失败: %v", name, err)
	}
	reg.Register(name, t)
}
