// Package native 是 SDK 私有的原生边界。控制参数走 JSON，截图和编码数据走二进制。
package native

import (
	"context"
	"encoding/json"
	"fmt"

	"autogov1/v2/sdk/ios/types"
)

type Result struct {
	Data          []byte
	PTS           int64
	Keyframe      bool
	Width, Height int
}

// Call 不在取消后重放写操作。原生调用已经返回的数据优先返回，避免掩盖已提交的事实。
func Call(ctx context.Context, op string, input any, output any) error {
	r, err := Raw(ctx, op, input)
	if err != nil {
		return err
	}
	if output == nil {
		return nil
	}
	if err = json.Unmarshal(r.Data, output); err != nil {
		return &types.Error{Op: op, Kind: types.ErrUnavailable, Detail: fmt.Sprintf("原生响应解析失败: %v", err)}
	}
	return nil
}
func Raw(ctx context.Context, op string, input any) (Result, error) {
	if ctx == nil {
		return Result{}, fmt.Errorf("%w: context 不能为空", types.ErrInvalid)
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	b, err := json.Marshal(input)
	if err != nil {
		return Result{}, err
	}
	return invoke(op, b)
}

func statusError(op string, code int, detail string) error {
	var kind error
	switch code {
	case 1:
		kind = types.ErrUnsupported
	case 2:
		kind = types.ErrInvalid
	case 4:
		kind = types.ErrUnknown
	case 5:
		kind = types.ErrBusy
	case 6:
		kind = types.ErrTimeout
	case 7:
		kind = types.ErrClosed
	default:
		kind = types.ErrUnavailable
	}
	return &types.Error{Op: op, Kind: kind, Detail: detail}
}
