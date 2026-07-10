package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cloudwego/eino/schema"

	"MiniGoAgent/internal/session"
	"MiniGoAgent/protocol"
	"MiniGoAgent/tools"
	"MiniGoAgent/tools/log"
	"MiniGoAgent/tools/sessionlog"
)

type ModelInfo interface {
	Model() string
	StatsLine() string
	StatsJSON() string
}

type Server struct {
	agent    AgentRunner
	model    ModelInfo
	sessions *session.Manager
	frontend []byte
	prompt   PromptProvider
}

type chatReq struct {
	Message string `json:"message"`
}

type chatResp struct {
	Reply string `json:"reply"`
	Stats any    `json:"stats,omitempty"`
}

type visionReq struct {
	Image  string `json:"image"`
	Prompt string `json:"prompt"`
}

func New(agent AgentRunner, modelInfo ModelInfo, mgr *session.Manager, frontendHTML []byte, prompt PromptProvider) *Server {
	return &Server{agent: agent, model: modelInfo, sessions: mgr, frontend: frontendHTML, prompt: prompt}
}

func (s *Server) SaveHistory() {
	sessionlog.CloseAll()
	if err := s.sessions.Save(session.DefaultHistoryFile); err != nil {
		log.Warn("保存历史文件失败: %v", err)
	} else {
		log.Info("已保存 %d 个会话到 %s", s.sessions.Count(), session.DefaultHistoryFile)
	}
}

func (s *Server) LoadHistory() {
	n, err := s.sessions.Load(session.DefaultHistoryFile)
	if err != nil {
		if os.IsNotExist(err) {
			return
		}
		log.Warn("读取历史文件失败: %v", err)
		return
	}
	log.Info("已恢复 %d 个会话的历史", n)
}

func (s *Server) ServeFrontend(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(s.frontend)
}

func (s *Server) HandleChatStream(w http.ResponseWriter, r *http.Request) {
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
		select {
		case <-r.Context().Done():
			return
		default:
		}
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

	if statsJSON := s.model.StatsJSON(); statsJSON != "" {
		fmt.Fprintf(w, "data: {\"stats\":%s}\n\n", statsJSON)
		flusher.Flush()
	}

	fmt.Fprintf(w, "data: [DONE]\n\n")
	flusher.Flush()

	s.sessions.Append(sid, userMsg, &schema.Message{Role: schema.Assistant, Content: fullContent.String()})
	s.logAssistantResponse(sid, fullContent.String())
}

func (s *Server) HandleChat(w http.ResponseWriter, r *http.Request) {
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

	if fp := extractLocalImagePath(req.Message); fp != "" {
		data, err := os.ReadFile(fp)
		if err == nil {
			b64 := base64.StdEncoding.EncodeToString(data)
			req.Message = "data:image/jpeg;base64," + b64
		}
	}

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

func (s *Server) HandleVision(w http.ResponseWriter, r *http.Request) {
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

func (s *Server) HandleVisionNativeStream(w http.ResponseWriter, r *http.Request) {
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

	if !supportsVision(s.model.Model()) {
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

	if statsJSON := s.model.StatsJSON(); statsJSON != "" {
		fmt.Fprintf(w, "data: %s\n\n", statsJSON)
		flusher.Flush()
	}
}

func (s *Server) handleVisionFromChat(w http.ResponseWriter, r *http.Request, sid, img, prompt string) {
	ctx := s.injectLogCtx(r.Context(), sid)
	if supportsVision(s.model.Model()) {
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

func (s *Server) logUserMessage(sid, text string) {
	if sl := sessionlog.Open(sid); sl != nil {
		sl.LogUser(text)
	}
}

func (s *Server) logAssistantResponse(sid, content string) {
	if sl := sessionlog.Get(sid); sl != nil {
		snippet := content
		if len(snippet) > 200 {
			snippet = snippet[:200]
		}
		sl.WriteAssistantResponse("", snippet, content, s.model.StatsLine())
	}
}

func (s *Server) injectLogCtx(ctx context.Context, sid string) context.Context {
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

func (s *Server) getStatsMap() any {
	jsonStr := s.model.StatsJSON()
	if jsonStr == "" {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(jsonStr), &m); err != nil {
		return nil
	}
	return m
}

func (s *Server) sessionID(r *http.Request) string {
	if id := r.URL.Query().Get("session"); id != "" {
		return id
	}
	if c, err := r.Cookie("session"); err == nil && c.Value != "" {
		return c.Value
	}
	return s.sessions.DefaultSession()
}

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
	path, err := tools.ValidatePath(trimmed)
	if err != nil {
		return ""
	}
	ext := strings.ToLower(filepath.Ext(path))
	for _, e := range imgExts {
		if ext == e {
			if _, err := os.Stat(path); err == nil {
				return path
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
