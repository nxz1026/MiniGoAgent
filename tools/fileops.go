package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type ReadFileInput struct {
	Path   string `json:"path" jsonschema:"required" jsonschema_description:"文件路径"`
	Offset int    `json:"offset" jsonschema_description:"起始行号（从1开始，默认1）"`
	Limit  int    `json:"limit" jsonschema_description:"最多读取行数（默认全部）"`
}

func ReadFile(ctx context.Context, input ReadFileInput) (string, error) {
	data, err := os.ReadFile(input.Path)
	if err != nil {
		return "", fmt.Errorf("读取文件失败: %w", err)
	}
	lines := strings.Split(string(data), "\n")
	start := 0
	if input.Offset > 1 {
		start = input.Offset - 1
	}
	if start >= len(lines) {
		return "", fmt.Errorf("起始行 %d 超出文件长度 %d", input.Offset, len(lines))
	}
	if input.Limit > 0 && start+input.Limit < len(lines) {
		lines = lines[start : start+input.Limit]
	} else {
		lines = lines[start:]
	}
	var b strings.Builder
	for i, line := range lines {
		fmt.Fprintf(&b, "%d: %s\n", start+i+1, line)
	}
	return b.String(), nil
}

type WriteFileInput struct {
	Path    string `json:"path" jsonschema:"required" jsonschema_description:"文件路径"`
	Content string `json:"content" jsonschema:"required" jsonschema_description:"写入内容"`
}

func WriteFile(ctx context.Context, input WriteFileInput) (string, error) {
	dir := filepath.Dir(input.Path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("创建目录失败: %w", err)
	}
	if err := os.WriteFile(input.Path, []byte(input.Content), 0644); err != nil {
		return "", fmt.Errorf("写入文件失败: %w", err)
	}
	return fmt.Sprintf("已写入 %d 字节到 %s", len(input.Content), input.Path), nil
}

type EditFileInput struct {
	Path    string `json:"path" jsonschema:"required" jsonschema_description:"文件路径"`
	OldText string `json:"old_text" jsonschema:"required" jsonschema_description:"要替换的原文"`
	NewText string `json:"new_text" jsonschema:"required" jsonschema_description:"替换后的新内容"`
}

func EditFile(ctx context.Context, input EditFileInput) (string, error) {
	data, err := os.ReadFile(input.Path)
	if err != nil {
		return "", fmt.Errorf("读取文件失败: %w", err)
	}
	content := string(data)
	n := strings.Count(content, input.OldText)
	if n == 0 {
		return "", fmt.Errorf("在 %s 中未找到匹配的原文", input.Path)
	}
	content = strings.ReplaceAll(content, input.OldText, input.NewText)
	if err := os.WriteFile(input.Path, []byte(content), 0644); err != nil {
		return "", fmt.Errorf("写入文件失败: %w", err)
	}
	return fmt.Sprintf("已在 %s 中替换 %d 处", input.Path, n), nil
}

type GlobInput struct {
	Pattern string `json:"pattern" jsonschema:"required" jsonschema_description:"glob 匹配模式，如 **/*.go"`
	Root    string `json:"root" jsonschema_description:"搜索根目录，默认当前目录"`
}

func GlobFiles(ctx context.Context, input GlobInput) (string, error) {
	root := input.Root
	if root == "" {
		root = "."
	}
	matches, err := filepath.Glob(filepath.Join(root, input.Pattern))
	if err != nil {
		return "", fmt.Errorf("glob 匹配失败: %w", err)
	}
	if len(matches) == 0 {
		return "未匹配到任何文件", nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "找到 %d 个文件:\n", len(matches))
	for _, m := range matches {
		rel, _ := filepath.Rel(root, m)
		fmt.Fprintf(&b, "  %s\n", rel)
	}
	return b.String(), nil
}

type GrepInput struct {
	Pattern string `json:"pattern" jsonschema:"required" jsonschema_description:"搜索模式（支持正则）"`
	Root    string `json:"root" jsonschema_description:"搜索根目录，默认当前目录"`
	Include string `json:"include" jsonschema_description:"只匹配特定后缀，如 .go"`
}

func GrepFiles(ctx context.Context, input GrepInput) (string, error) {
	root := input.Root
	if root == "" {
		root = "."
	}
	var results []string
	filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if input.Include != "" && !strings.HasSuffix(path, input.Include) {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		lines := strings.Split(string(data), "\n")
		for i, line := range lines {
			if strings.Contains(line, input.Pattern) {
				rel, _ := filepath.Rel(root, path)
				results = append(results, fmt.Sprintf("%s:%d: %s", rel, i+1, strings.TrimSpace(line)))
			}
		}
		if len(results) > 200 {
			return fmt.Errorf("结果太多，已截断")
		}
		return nil
	})
	if len(results) == 0 {
		return "未找到匹配", nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "找到 %d 处匹配:\n", len(results))
	for _, r := range results {
		fmt.Fprintf(&b, "  %s\n", r)
	}
	return b.String(), nil
}
