package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

type SearchInput struct {
	Query string `json:"query" jsonschema:"required" jsonschema_description:"搜索关键词"`
}

func WebSearch(ctx context.Context, input SearchInput) (string, error) {
	apiKey := os.Getenv("FIRECRAWL_API_KEY")
	apiURL := "https://api.firecrawl.dev/v2/search"

	reqBody := map[string]any{
		"query": input.Query,
		"limit": 5,
	}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("序列化请求失败: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("创建请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("API 请求失败: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("读取响应失败: %w", err)
	}
	var result struct {
		Success bool `json:"success"`
		Data    struct {
			Web []struct {
				URL         string `json:"url"`
				Title       string `json:"title"`
				Description string `json:"description"`
			} `json:"web"`
		} `json:"data"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("解析响应失败: %w", err)
	}
	if !result.Success || len(result.Data.Web) == 0 {
		return "未找到结果", nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, "找到 %d 条结果:\n\n", len(result.Data.Web))
	for i, r := range result.Data.Web {
		fmt.Fprintf(&b, "%d. %s\n", i+1, r.Title)
		fmt.Fprintf(&b, "   URL: %s\n", r.URL)
		if r.Description != "" {
			fmt.Fprintf(&b, "   摘要: %s\n", r.Description)
		}
		fmt.Fprintln(&b)
	}
	output := b.String()
	if len(output) > 2000 {
		compressed, err := RunCompress(ctx, CompressInput{
			Content:     output,
			Instruction: "压缩以下搜索结果，保留所有标题和 URL，对每个结果用一句话概括，保持语言不变：",
		})
		if err == nil {
			return compressed, nil
		}
	}
	return output, nil
}
