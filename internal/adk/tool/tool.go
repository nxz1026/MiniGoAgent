package tool

import (
	"context"
	"errors"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/cloudwego/eino/schema"
)

var ErrToolNotFound = errors.New("tool not found")

type CheckFunc func(ctx context.Context) bool

type Tool struct {
	invokable tool.InvokableTool
	checkFn   CheckFunc
}

func NewFromFn[T, D any](name, desc string, fn func(ctx context.Context, input T) (D, error), opts ...utils.Option) (*Tool, error) {
	it, err := utils.InferTool(name, desc, fn, opts...)
	if err != nil {
		return nil, err
	}
	return &Tool{invokable: it}, nil
}

func NewFromInvokable(it tool.InvokableTool) *Tool {
	return &Tool{invokable: it}
}

func (t *Tool) WithCheck(fn CheckFunc) *Tool {
	t.checkFn = fn
	return t
}

func (t *Tool) Name() string {
	info, err := t.invokable.Info(context.Background())
	if err != nil {
		return ""
	}
	return info.Name
}

func (t *Tool) Description() string {
	info, err := t.invokable.Info(context.Background())
	if err != nil {
		return ""
	}
	return info.Desc
}

func (t *Tool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return t.invokable.Info(ctx)
}

func (t *Tool) InvokableRun(ctx context.Context, args string, opts ...tool.Option) (string, error) {
	return t.invokable.InvokableRun(ctx, args, opts...)
}

func (t *Tool) Check(ctx context.Context) bool {
	if t.checkFn == nil {
		return true
	}
	return t.checkFn(ctx)
}

func (t *Tool) ToEinoTool() tool.BaseTool {
	return t.invokable
}
