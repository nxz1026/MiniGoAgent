package main

import (
	"context"
	"embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/flow/agent/react"
	"github.com/cloudwego/eino/schema"

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
				return
			}
		}
	}()
	return sr, nil
}

func (m *chatModel) WithTools(tools []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return &chatModel{proto: m.proto, model: m.model, tools: tools}, nil
}

// ---------- HTTP Server ----------

type chatServer struct {
	agent          *react.Agent
	llm            *chatModel
	sessions       map[string][]*schema.Message
	defaultSession string
	mu             sync.Mutex
}

type chatReq struct {
	Message string `json:"message"`
}
type chatResp struct {
	Reply string `json:"reply"`
	Stats any    `json:"stats,omitempty"`
}

func main() {
	loadEnv()
	ctx := context.Background()

	apiKey := os.Getenv("OPENAI_API_KEY")
	baseURL := os.Getenv("OPENAI_BASE_URL")
	if apiKey == "" || baseURL == "" {
		log.Fatal("请设置 OPENAI_API_KEY 和 OPENAI_BASE_URL")
	}

	proto, err := protocol.New("openai", protocol.Config{
		APIKey:  apiKey,
		BaseURL: baseURL,
		Model:   getEnv("OPENAI_MODEL", "deepseek-v4-flash"),
	})
	if err != nil {
		log.Fatal("创建 Protocol 失败: %v", err)
	}
	llm := &chatModel{proto: proto, model: getEnv("OPENAI_MODEL", "deepseek-v4-flash")}

	terminalTool, _ := utils.InferTool("terminal", "在 Windows 终端执行 shell 命令", tools.RunTerminal)
	searchTool, _ := utils.InferTool("web_search", "在互联网上搜索信息", tools.WebSearch)
	compressTool, _ := utils.InferTool("compress", "压缩长文本内容，保留关键信息", tools.RunCompress)
	readFileTool, _ := utils.InferTool("read_file", "读取文件内容，可指定行范围", tools.ReadFile)
	writeFileTool, _ := utils.InferTool("write_file", "创建或覆写文件", tools.WriteFile)
	editFileTool, _ := utils.InferTool("edit_file", "在文件中查找替换文本", tools.EditFile)
	globTool, _ := utils.InferTool("glob", "按 glob 模式搜索文件", tools.GlobFiles)
	grepTool, _ := utils.InferTool("grep", "在文件中搜索文本", tools.GrepFiles)

	agent, err := react.NewAgent(ctx, &react.AgentConfig{
		ToolCallingModel:   llm,
		ToolsConfig:        compose.ToolsNodeConfig{Tools: []tool.BaseTool{terminalTool, searchTool, compressTool, readFileTool, writeFileTool, editFileTool, globTool, grepTool}},
		MaxStep:            12,
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

	srv := &chatServer{agent: agent, llm: llm, sessions: map[string][]*schema.Message{}, defaultSession: "default"}
	srv.loadHistory()
	http.HandleFunc("/", srv.serveFrontend)
	http.HandleFunc("/api/chat", srv.handleChat)
	http.HandleFunc("/api/chat/stream", srv.handleChatStream)
	http.HandleFunc("/api/vision", srv.handleVision)

	port := getEnv("PORT", "8080")
	log.Info("MiniGoAgent UI: http://localhost:%s", port)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		<-ctx.Done()
		log.Info("收到退出信号，正在保存历史...")
		srv.saveHistory()
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
	json.NewDecoder(r.Body).Decode(&req)

	s.mu.Lock()
	sid := s.sessionID(r)
	msgs := s.messages(sid)
	msgs = append(msgs, schema.UserMessage(req.Message))
	s.mu.Unlock()

	// session log
	if sl := sessionlog.Open(sid); sl != nil {
		sl.LogUser(req.Message)
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", 500)
		return
	}

	ctx := context.WithValue(context.Background(), protocol.CtxLogf, func(f string, a ...any) {
		log.Debug(f, a...)
		if sl := sessionlog.Get(sid); sl != nil {
			sl.LogRaw(f, a)
		}
	})
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

	s.mu.Lock()
	msgs = s.messages(sid)
	msgs = append(msgs, &schema.Message{Role: schema.Assistant, Content: fullContent.String()})
	s.sessions[sid] = msgs
	s.mu.Unlock()

	// session log assistant response
	if sl := sessionlog.Get(sid); sl != nil {
		snippet := fullContent.String()
		if len(snippet) > 200 {
			snippet = snippet[:200]
		}
		sl.WriteAssistantResponse("", snippet, fullContent.String(), s.llm.StatsLine())
	}
}

func (s *chatServer) sessionID(r *http.Request) string {
	if id := r.URL.Query().Get("session"); id != "" {
		return id
	}
	if c, err := r.Cookie("session"); err == nil && c.Value != "" {
		return c.Value
	}
	return s.defaultSession
}

func (s *chatServer) messages(sid string) []*schema.Message {
	if msgs, ok := s.sessions[sid]; ok {
		return msgs
	}
	return nil
}

func (s *chatServer) handleChat(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "method not allowed", 405)
		return
	}
	var req chatReq
	json.NewDecoder(r.Body).Decode(&req)

	s.mu.Lock()
	defer s.mu.Unlock()

	sid := s.sessionID(r)
	msgs := s.messages(sid)

	// session log
	if sl := sessionlog.Open(sid); sl != nil {
		sl.LogUser(req.Message)
	}

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
		s.handleVisionInternal(w, sid, req.Message, "描述这张图片")
		return
	}

	msgs = append(msgs, schema.UserMessage(req.Message))
	ctx := context.WithValue(context.Background(), protocol.CtxLogf, func(f string, a ...any) {
		log.Debug(f, a...)
		if sl := sessionlog.Get(sid); sl != nil {
			sl.LogRaw(f, a)
		}
	})
	result, err := s.agent.Generate(ctx, msgs)
	if err != nil {
		json.NewEncoder(w).Encode(chatResp{Reply: "错误: " + err.Error()})
		return
	}
	msgs = append(msgs, result)
	s.sessions[sid] = msgs

	// stats
	statsLine := s.llm.StatsLine()
	if statsLine == "" {
		statsLine = s.llm.model
	}

	// session log assistant
	if sl := sessionlog.Get(sid); sl != nil {
		snippet := result.Content
		if len(snippet) > 200 {
			snippet = snippet[:200]
		}
		sl.WriteAssistantResponse("", snippet, result.Content, statsLine)
	}

	type statsProvider interface{ GetTelemetry() *protocol.Telemetry }
	var statsMap any
	if p, ok := s.llm.proto.(statsProvider); ok {
		statsMap = p.GetTelemetry().FormatMap()
	}
	json.NewEncoder(w).Encode(chatResp{Reply: result.Content, Stats: statsMap})
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
	json.NewDecoder(r.Body).Decode(&req)

	s.mu.Lock()
	defer s.mu.Unlock()
	sid := s.sessionID(r)
	s.handleVisionInternal(w, sid, req.Image, req.Prompt)
}

func (s *chatServer) handleVisionInternal(w http.ResponseWriter, sid, img, prompt string) {
	visionResult, err := tools.RunVision(context.Background(), tools.VisionInput{ImageURL: img, Prompt: prompt})
	if err != nil {
		json.NewEncoder(w).Encode(chatResp{Reply: "Vision 错误: " + err.Error()})
		return
	}
	userMsg := "用户上传了一张图片，提问：" + prompt + "\n\n图片分析结果：\n" + visionResult
	msg := schema.UserMessage(userMsg)
	msgs := s.messages(sid)
	msgs = append(msgs, msg)
	ctx := context.WithValue(context.Background(), protocol.CtxLogf, func(f string, a ...any) {
		log.Debug(f, a...)
		if sl := sessionlog.Get(sid); sl != nil {
			sl.LogRaw(f, a)
		}
	})
	result, err := s.agent.Generate(ctx, msgs)
	if err != nil {
		json.NewEncoder(w).Encode(chatResp{Reply: "错误: " + err.Error()})
		return
	}
	msgs = append(msgs, result)
	s.sessions[sid] = msgs
	type statsProvider interface{ GetTelemetry() *protocol.Telemetry }
	var statsMap any
	if p, ok := s.llm.proto.(statsProvider); ok {
		statsMap = p.GetTelemetry().FormatMap()
	}
	json.NewEncoder(w).Encode(chatResp{Reply: result.Content, Stats: statsMap})
}

// ---------- history persistence ----------

const historyFile = "history.json"

func (s *chatServer) saveHistory() {
	sessionlog.CloseAll()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.sessions) == 0 {
		return
	}
	data, err := json.Marshal(s.sessions)
	if err != nil {
		log.Warn("保存历史失败: %v", err)
		return
	}
	if err := os.WriteFile(historyFile, data, 0644); err != nil {
		log.Warn("保存历史文件失败: %v", err)
	} else {
		log.Info("已保存 %d 个会话到 %s", len(s.sessions), historyFile)
	}
}

func (s *chatServer) loadHistory() {
	data, err := os.ReadFile(historyFile)
	if err != nil {
		return
	}
	var sessions map[string][]*schema.Message
	if err := json.Unmarshal(data, &sessions); err != nil {
		log.Warn("读取历史文件失败: %v", err)
		return
	}
	for k, v := range sessions {
		s.sessions[k] = v
	}
	log.Info("已恢复 %d 个会话的历史", len(sessions))
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

func loadEnv() {
	data, err := os.ReadFile(".env")
	if err != nil {
		return
	}
	text := strings.ReplaceAll(string(data), "\r", "")
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if ok {
			os.Setenv(strings.TrimSpace(k), strings.TrimSpace(v))
		}
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
