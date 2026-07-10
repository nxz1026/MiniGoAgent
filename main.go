package main

import (
	"context"
	"embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/flow/agent/react"
	"github.com/cloudwego/eino/schema"

	"MiniGoAgent/internal/config"
	appsession "MiniGoAgent/internal/session"
	"MiniGoAgent/protocol"
	"MiniGoAgent/tools"
	"MiniGoAgent/tools/log"
	"MiniGoAgent/tools/sessionlog"
)

//go:embed frontend/index.html
var frontendFS embed.FS

// ---------- ChatModel (adapter: eino ← protocol) ----------

type chatModel struct {
	proto protocol.Protocol
	tools []*schema.ToolInfo
	model string
}

func (m *chatModel) StatsLine() string {
	type telProvider interface{ GetTelemetry() *protocol.Telemetry }
	p, ok := m.proto.(telProvider)
	if !ok {
		return m.model
	}
	return p.GetTelemetry().FormatLine(" · ")
}

func (m *chatModel) StatsJSON() string {
	type telProvider interface{ GetTelemetry() *protocol.Telemetry }
	p, ok := m.proto.(telProvider)
	if !ok {
		return ""
	}
	data, _ := json.Marshal(p.GetTelemetry().FormatMap())
	return string(data)
}

func toProtoMsg(m *schema.Message) protocol.Message {
	pm := protocol.Message{
		Role:             protocol.Role(m.Role),
		Content:          m.Content,
		ReasoningContent: m.ReasoningContent,
		ToolCallID:       m.ToolCallID,
		Name:             m.Name,
	}
	if len(m.UserInputMultiContent) > 0 {
		pm.MultiContent = make([]map[string]any, 0, len(m.UserInputMultiContent))
		for _, part := range m.UserInputMultiContent {
			item := map[string]any{"type": string(part.Type)}
			if part.Text != "" {
				item["text"] = part.Text
			}
			if part.Image != nil {
				img := map[string]any{}
				if part.Image.URL != nil {
					img["url"] = *part.Image.URL
				}
				if part.Image.Base64Data != nil {
					img["url"] = "data:" + part.Image.MIMEType + ";base64," + *part.Image.Base64Data
				}
				item["image_url"] = img
			}
			pm.MultiContent = append(pm.MultiContent, item)
		}
	}
	for _, tc := range m.ToolCalls {
		pm.ToolCalls = append(pm.ToolCalls, protocol.ToolCall{
			ID: tc.ID, Type: tc.Type, Name: tc.Function.Name, Arguments: tc.Function.Arguments,
		})
	}
	return pm
}

func toProtoMsgs(msgs []*schema.Message) []protocol.Message {
	out := make([]protocol.Message, len(msgs))
	for i, m := range msgs {
		out[i] = toProtoMsg(m)
	}
	return out
}

func toProtoTools(tools []*schema.ToolInfo) []protocol.ToolSchema {
	out := make([]protocol.ToolSchema, len(tools))
	for i, t := range tools {
		params := json.RawMessage(`{"type":"object","properties":{}}`)
		if js, err := t.ParamsOneOf.ToJSONSchema(); err == nil && js != nil {
			if b, err := json.Marshal(js); err == nil {
				params = json.RawMessage(b)
			}
		}
		out[i] = protocol.ToolSchema{Name: t.Name, Description: t.Desc, Parameters: params}
	}
	return out
}

func fromProtoResp(resp *protocol.Response) *schema.Message {
	msg := &schema.Message{Role: schema.Assistant, Content: resp.Content, ReasoningContent: resp.ReasoningContent}
	for _, tc := range resp.ToolCalls {
		msg.ToolCalls = append(msg.ToolCalls, schema.ToolCall{
			ID: tc.ID, Type: tc.Type,
			Function: schema.FunctionCall{Name: tc.Name, Arguments: tc.Arguments},
		})
	}
	return msg
}

func (m *chatModel) Generate(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	common := model.GetCommonOptions(nil, opts...)
	req := protocol.Request{
		Messages:  toProtoMsgs(input),
		Tools:     toProtoTools(m.tools),
		MaxTokens: common.MaxTokens,
		Stop:      common.Stop,
	}
	if common.Temperature != nil {
		v := float64(*common.Temperature)
		req.Temperature = &v
	}
	if common.TopP != nil {
		v := float64(*common.TopP)
		req.TopP = &v
	}

	resp, err := m.proto.Chat(ctx, req)
	if err != nil {
		return nil, err
	}
	if resp.Content == "" && len(resp.ToolCalls) == 0 {
		return nil, fmt.Errorf("API返回空消息")
	}
	return fromProtoResp(resp), nil
}

func (m *chatModel) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	common := model.GetCommonOptions(nil, opts...)
	req := protocol.Request{
		Messages:  toProtoMsgs(input),
		Tools:     toProtoTools(m.tools),
		MaxTokens: common.MaxTokens,
		Stop:      common.Stop,
	}
	if common.Temperature != nil {
		v := float64(*common.Temperature)
		req.Temperature = &v
	}
	if common.TopP != nil {
		v := float64(*common.TopP)
		req.TopP = &v
	}

	ch, err := m.proto.Stream(ctx, req)
	if err != nil {
		return nil, err
	}

	sr, sw := schema.Pipe[*schema.Message](64)
	go func() {
		defer sw.Close()
		for chunk := range ch {
			switch chunk.Type {
			case protocol.ChunkText:
				sw.Send(&schema.Message{Role: schema.Assistant, Content: chunk.Text}, nil)
			case protocol.ChunkError:
				var interrupted *protocol.StreamInterruptedError
				if chunk.Error != nil && errors.As(chunk.Error, &interrupted) {
					sw.Send(&schema.Message{Role: schema.Assistant, Content: "[流中断，正在恢复...]"}, nil)
					resp, err := m.forwardChat(ctx, req)
					if err != nil {
						return
					}
					if resp.Content != "" {
						sw.Send(&schema.Message{Role: schema.Assistant, Content: resp.Content}, nil)
					}
				}
				return
			}
		}
	}()
	return sr, nil
}

func (m *chatModel) forwardChat(ctx context.Context, req protocol.Request) (*protocol.Response, error) {
	return m.proto.Chat(ctx, req)
}

func (m *chatModel) WithTools(tools []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return &chatModel{proto: m.proto, model: m.model, tools: tools}, nil
}

// ---------- HTTP Server ----------

type chatServer struct {
	agent    *react.Agent
	llm      *chatModel
	sessions *appsession.Manager
}

type chatReq struct {
	Message string `json:"message"`
}
type chatResp struct {
	Reply string `json:"reply"`
	Stats any    `json:"stats,omitempty"`
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
	llm := &chatModel{proto: proto, model: cfg.OpenAI.Model}

	terminalTool, _ := utils.InferTool("terminal", "在 Windows 终端执行 shell 命令", tools.RunTerminal)
	searchTool, _ := utils.InferTool("web_search", "在互联网上搜索信息", tools.WebSearch)
	compressTool, _ := utils.InferTool("compress", "压缩长文本内容，保留关键信息", tools.RunCompress)
	readFileTool, _ := utils.InferTool("read_file", "读取文件内容，可指定行范围", tools.ReadFile)
	writeFileTool, _ := utils.InferTool("write_file", "创建或覆写文件", tools.WriteFile)
	editFileTool, _ := utils.InferTool("edit_file", "在文件中查找替换文本", tools.EditFile)
	globTool, _ := utils.InferTool("glob", "按 glob 模式搜索文件", tools.GlobFiles)
	grepTool, _ := utils.InferTool("grep", "在文件中搜索文本", tools.GrepFiles)
	visionTool, _ := utils.InferTool("vision", "分析图片内容，支持URL和base64 data URI", tools.RunVision)

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
			return append([]*schema.Message{schema.SystemMessage(
				"你是密米尔，一个沉稳睿智的 AI 助手。思考步骤和调用工具时使用中文，但在输出最终回答时，请结合用户提问所使用的语言（中文或英文），使用对应的语言输出。不要同时输出中英文混合的内容。")}, input...)
		},
	})
	if err != nil {
		log.Fatal("创建 Agent 失败: %v", err)
	}

	srv := &chatServer{agent: agent, llm: llm, sessions: appsession.NewManager("default")}
	srv.loadHistory()
	http.HandleFunc("/", srv.serveFrontend)
	http.HandleFunc("/api/chat", srv.handleChat)
	http.HandleFunc("/api/chat/stream", srv.handleChatStream)
	http.HandleFunc("/api/vision", srv.handleVision)
	http.HandleFunc("/api/vision/native", srv.handleVisionNativeStream)
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
		srv.saveHistory()
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

func (s *chatServer) serveFrontend(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	data, err := frontendFS.ReadFile("frontend/index.html")
	if err != nil {
		log.Error("读取前端文件失败: %v", err)
		http.Error(w, "internal error", 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(data)
}

func (s *chatServer) handleChatStream(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "method not allowed", 405)
		return
	}
	var req chatReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}

	sid := s.sessionID(r)
	userMsg := schema.UserMessage(req.Message)
	msgs := s.sessions.SnapshotWith(sid, userMsg)

	s.logUserMessage(sid, req.Message)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", 500)
		return
	}

	ctx := s.injectLogCtx(r.Context(), sid)
	stream, err := s.agent.Stream(ctx, msgs)
	if err != nil {
		fmt.Fprintf(w, "data: {\"error\":%q}\n\n", err.Error())
		flusher.Flush()
		return
	}
	defer stream.Close()

	var fullContent strings.Builder
	for {
		chunk, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			break
		}
		var toolCallIDs []string
		for _, tc := range chunk.ToolCalls {
			toolCallIDs = append(toolCallIDs, tc.ID)
		}
		data, _ := json.Marshal(map[string]any{
			"content":    chunk.Content,
			"tool_calls": toolCallIDs,
		})
		fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()
		fullContent.WriteString(chunk.Content)
	}

	// stats
	if statsJSON := s.llm.StatsJSON(); statsJSON != "" {
		fmt.Fprintf(w, "data: {\"stats\":%s}\n\n", statsJSON)
		flusher.Flush()
	}

	fmt.Fprintf(w, "data: [DONE]\n\n")
	flusher.Flush()

	s.sessions.Append(sid, userMsg, &schema.Message{Role: schema.Assistant, Content: fullContent.String()})

	s.logAssistantResponse(sid, fullContent.String())
}

func (s *chatServer) logUserMessage(sid, text string) {
	if sl := sessionlog.Open(sid); sl != nil {
		sl.LogUser(text)
	}
}

func (s *chatServer) logAssistantResponse(sid, content string) {
	if sl := sessionlog.Get(sid); sl != nil {
		snippet := content
		if len(snippet) > 200 {
			snippet = snippet[:200]
		}
		sl.WriteAssistantResponse("", snippet, content, s.llm.StatsLine())
	}
}

func (s *chatServer) injectLogCtx(ctx context.Context, sid string) context.Context {
	ctx = context.WithValue(ctx, protocol.CtxLogf, func(f string, a ...any) {
		msg := protocol.RedactString(fmt.Sprint(fmt.Sprintf(f, a...)))
		log.Info("%s", msg)
		if sl := sessionlog.Get(sid); sl != nil {
			sl.LogRaw(msg, nil)
		}
	})
	ctx = context.WithValue(ctx, protocol.CtxSessionID, sid)
	return ctx
}

func (s *chatServer) getStatsMap() any {
	type statsProvider interface{ GetTelemetry() *protocol.Telemetry }
	if p, ok := s.llm.proto.(statsProvider); ok {
		return p.GetTelemetry().FormatMap()
	}
	return nil
}

func (s *chatServer) sessionID(r *http.Request) string {
	if id := r.URL.Query().Get("session"); id != "" {
		return id
	}
	if c, err := r.Cookie("session"); err == nil && c.Value != "" {
		return c.Value
	}
	return s.sessions.DefaultSession()
}

func (s *chatServer) handleChat(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "method not allowed", 405)
		return
	}
	var req chatReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}

	sid := s.sessionID(r)

	s.logUserMessage(sid, req.Message)

	// detect local image path in message
	if fp := extractLocalImagePath(req.Message); fp != "" {
		data, err := os.ReadFile(fp)
		if err == nil {
			b64 := base64.StdEncoding.EncodeToString(data)
			req.Message = "data:image/jpeg;base64," + b64
		}
	}

	// route to vision if it's an image
	if strings.HasPrefix(req.Message, "data:image/") {
		s.handleVisionFromChat(w, r, sid, req.Message, "描述这张图片")
		return
	}

	userMsg := schema.UserMessage(req.Message)
	msgs := s.sessions.SnapshotWith(sid, userMsg)
	ctx := s.injectLogCtx(r.Context(), sid)
	result, err := s.agent.Generate(ctx, msgs)
	if err != nil {
		json.NewEncoder(w).Encode(chatResp{Reply: "错误: " + err.Error()})
		return
	}
	s.sessions.Append(sid, userMsg, result)

	s.logAssistantResponse(sid, result.Content)

	json.NewEncoder(w).Encode(chatResp{Reply: result.Content, Stats: s.getStatsMap()})
}

type visionReq struct {
	Image  string `json:"image"`
	Prompt string `json:"prompt"`
}

func (s *chatServer) handleVision(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "method not allowed", 405)
		return
	}
	var req visionReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}

	sid := s.sessionID(r)

	ctx := s.injectLogCtx(r.Context(), sid)
	visionResult, err := tools.RunVision(ctx, tools.VisionInput{ImageURL: req.Image, Prompt: req.Prompt})
	if err != nil {
		json.NewEncoder(w).Encode(chatResp{Reply: "Vision 错误: " + err.Error()})
		return
	}
	userMsg := schema.UserMessage("用户上传了一张图片，提问：" + req.Prompt + "\n\n图片分析结果：\n" + visionResult)
	msgs := s.sessions.SnapshotWith(sid, userMsg)
	result, err := s.agent.Generate(ctx, msgs)
	if err != nil {
		json.NewEncoder(w).Encode(chatResp{Reply: "错误: " + err.Error()})
		return
	}
	s.sessions.Append(sid, userMsg, result)
	json.NewEncoder(w).Encode(chatResp{Reply: result.Content, Stats: s.getStatsMap()})
}

func (s *chatServer) handleVisionFromChat(w http.ResponseWriter, r *http.Request, sid, img, prompt string) {
	ctx := s.injectLogCtx(r.Context(), sid)
	if supportsVision(s.llm.model) {
		dataURL, err := imageToDataURL(ctx, img)
		if err != nil {
			json.NewEncoder(w).Encode(chatResp{Reply: "图片处理失败: " + err.Error()})
			return
		}
		textPart := schema.MessageInputPart{
			Type: schema.ChatMessagePartTypeText,
			Text: prompt,
		}
		imagePart := schema.MessageInputPart{
			Type: schema.ChatMessagePartTypeImageURL,
			Image: &schema.MessageInputImage{
				MessagePartCommon: schema.MessagePartCommon{URL: strPtr(dataURL)},
			},
		}
		userMsg := &schema.Message{
			Role:                  schema.User,
			Content:               "[图片] " + prompt,
			UserInputMultiContent: []schema.MessageInputPart{textPart, imagePart},
		}
		msgs := s.sessions.SnapshotWith(sid, userMsg)
		result, err := s.agent.Generate(ctx, msgs)
		if err != nil {
			json.NewEncoder(w).Encode(chatResp{Reply: "错误: " + err.Error()})
			return
		}
		s.sessions.Append(sid, userMsg, result)
		json.NewEncoder(w).Encode(chatResp{Reply: result.Content, Stats: s.getStatsMap()})
		return
	}
	visionResult, err := tools.RunVision(ctx, tools.VisionInput{ImageURL: img, Prompt: prompt})
	if err != nil {
		json.NewEncoder(w).Encode(chatResp{Reply: "Vision 错误: " + err.Error()})
		return
	}
	userMsg := schema.UserMessage("用户上传了一张图片，提问：" + prompt + "\n\n图片分析结果：\n" + visionResult)
	msgs := s.sessions.SnapshotWith(sid, userMsg)
	result, err := s.agent.Generate(ctx, msgs)
	if err != nil {
		json.NewEncoder(w).Encode(chatResp{Reply: "错误: " + err.Error()})
		return
	}
	s.sessions.Append(sid, userMsg, result)
	json.NewEncoder(w).Encode(chatResp{Reply: result.Content, Stats: s.getStatsMap()})
}

func (s *chatServer) handleVisionNativeStream(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "method not allowed", 405)
		return
	}
	var req visionReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}

	sid := s.sessionID(r)
	msgs := s.sessions.SnapshotWith(sid)

	s.logUserMessage(sid, "[图片] "+req.Prompt)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", 500)
		return
	}

	ctx := s.injectLogCtx(r.Context(), sid)

	if !supportsVision(s.llm.model) {
		visionResult, err := tools.RunVision(ctx, tools.VisionInput{ImageURL: req.Image, Prompt: req.Prompt})
		if err != nil {
			fmt.Fprintf(w, "data: {\"error\":%q}\n\n", err.Error())
			flusher.Flush()
			return
		}
		data, _ := json.Marshal(map[string]any{"content": "图片分析结果：\n" + visionResult})
		fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()

		s.sessions.Append(sid,
			schema.UserMessage("[图片] "+req.Prompt+"\n\n图片分析结果：\n"+visionResult),
			&schema.Message{Role: schema.Assistant, Content: visionResult},
		)
		return
	}

	dataURL, err := imageToDataURL(ctx, req.Image)
	if err != nil {
		fmt.Fprintf(w, "data: {\"error\":%q}\n\n", err.Error())
		flusher.Flush()
		return
	}

	textPart := schema.MessageInputPart{
		Type: schema.ChatMessagePartTypeText,
		Text: req.Prompt,
	}
	imagePart := schema.MessageInputPart{
		Type: schema.ChatMessagePartTypeImageURL,
		Image: &schema.MessageInputImage{
			MessagePartCommon: schema.MessagePartCommon{URL: strPtr(dataURL)},
		},
	}
	userMsg := &schema.Message{
		Role:                  schema.User,
		Content:               "[图片] " + req.Prompt,
		UserInputMultiContent: []schema.MessageInputPart{textPart, imagePart},
	}
	msgs = append(msgs, userMsg)

	stream, err := s.agent.Stream(ctx, msgs)
	if err != nil {
		fmt.Fprintf(w, "data: {\"error\":%q}\n\n", err.Error())
		flusher.Flush()
		return
	}
	defer stream.Close()

	var fullContent strings.Builder
	for {
		chunk, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			break
		}
		data, _ := json.Marshal(map[string]any{"content": chunk.Content})
		fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()
		fullContent.WriteString(chunk.Content)
	}

	resultMsg := &schema.Message{Role: schema.Assistant, Content: fullContent.String()}
	s.sessions.Append(sid, userMsg, resultMsg)

	if statsJSON := s.llm.StatsJSON(); statsJSON != "" {
		fmt.Fprintf(w, "data: %s\n\n", statsJSON)
		flusher.Flush()
	}
}

func (s *chatServer) saveHistory() {
	sessionlog.CloseAll()
	if err := s.sessions.Save(appsession.DefaultHistoryFile); err != nil {
		log.Warn("保存历史文件失败: %v", err)
	} else {
		log.Info("已保存 %d 个会话到 %s", s.sessions.Count(), appsession.DefaultHistoryFile)
	}
}

func (s *chatServer) loadHistory() {
	n, err := s.sessions.Load(appsession.DefaultHistoryFile)
	if err != nil {
		if os.IsNotExist(err) {
			return
		}
		log.Warn("读取历史文件失败: %v", err)
		return
	}
	log.Info("已恢复 %d 个会话的历史", n)
}

// ---------- helpers ----------

var imgExts = []string{".png", ".jpg", ".jpeg", ".gif", ".webp", ".bmp", ".svg"}

func extractLocalImagePath(input string) string {
	trimmed := strings.TrimSpace(input)
	if idx := strings.LastIndexAny(trimmed, " \t\n\r"); idx >= 0 {
		trimmed = trimmed[idx+1:]
		trimmed = strings.TrimSpace(trimmed)
	}
	if trimmed == "" {
		return ""
	}
	ext := strings.ToLower(filepath.Ext(trimmed))
	for _, e := range imgExts {
		if ext == e {
			if _, err := os.Stat(trimmed); err == nil {
				return trimmed
			}
		}
	}
	return ""
}

func imageToDataURL(ctx context.Context, img string) (string, error) {
	if strings.HasPrefix(img, "data:") {
		return img, nil
	}
	if strings.HasPrefix(img, "http://") || strings.HasPrefix(img, "https://") {
		data, err := fetchImageDataURL(ctx, img)
		if err != nil {
			return "", fmt.Errorf("download image: %w", err)
		}
		return data, nil
	}
	localPath := extractLocalImagePath(img)
	if localPath == "" {
		return "", fmt.Errorf("invalid image path: %s", img)
	}
	data, err := os.ReadFile(localPath)
	if err != nil {
		return "", fmt.Errorf("read image: %w", err)
	}
	ext := strings.ToLower(filepath.Ext(localPath))
	mime := "image/png"
	switch ext {
	case ".jpg", ".jpeg":
		mime = "image/jpeg"
	case ".gif":
		mime = "image/gif"
	case ".webp":
		mime = "image/webp"
	case ".bmp":
		mime = "image/bmp"
	case ".svg":
		mime = "image/svg+xml"
	}
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data), nil
}

func fetchImageDataURL(ctx context.Context, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64)")
	client := protocol.NewHTTPClient(30 * time.Second)
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	ct := resp.Header.Get("Content-Type")
	if ct == "" {
		ct = http.DetectContentType(data)
	}
	return "data:" + ct + ";base64," + base64.StdEncoding.EncodeToString(data), nil
}

func supportsVision(model string) bool {
	lower := strings.ToLower(model)
	visionModels := []string{
		"gpt-4o", "gpt-4o-mini", "gpt-4-turbo",
		"claude-3", "claude-3-5", "claude-sonnet-4", "claude-opus-4",
		"deepseek-vl", "deepseek-vl2",
		"qwen-vl", "qwen2-vl", "qwq",
		"step-2",
		"gemini",
	}
	for _, vm := range visionModels {
		if strings.Contains(lower, vm) {
			return true
		}
	}
	return false
}

func strPtr(s string) *string {
	return &s
}
