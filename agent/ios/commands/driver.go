// Package commands 将云控公共命令映射到 iOS SDK，绝不调用安卓 Agent/SDK。
package commands

import (
	"autogov1/v2/agent/ios/runtime"
	"autogov1/v2/sdk/ios/types"
	"context"
	"errors"
)

type Backend interface {
	Invoke(context.Context, string, any, any) error
	Info(context.Context) (runtime.Info, error)
	Healthy(context.Context) error
	Screenshot(context.Context) ([]byte, error)
	OpenVideo(context.Context, runtime.VideoOptions) (runtime.VideoSource, error)
}
type Driver struct{ Backend }

func New(b Backend) *Driver {
	return &Driver{Backend: &serialized{Backend: b, gate: make(chan struct{}, 1)}}
}
func (d *Driver) Commands() []string     { return append([]string(nil), commands...) }
func (d *Driver) Unknown(err error) bool { return errors.Is(err, types.ErrUnknown) }

// 同一驱动的普通请求串行，取消等待不会重放动作；健康探测与视频不占此队列。
type serialized struct {
	Backend
	gate chan struct{}
}

func (s *serialized) enter(ctx context.Context) (func(), error) {
	select {
	case s.gate <- struct{}{}:
		if err := ctx.Err(); err != nil {
			<-s.gate
			return nil, err
		}
		return func() { <-s.gate }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
func (s *serialized) Invoke(ctx context.Context, n string, a any, o any) error {
	done, e := s.enter(ctx)
	if e != nil {
		return e
	}
	defer done()
	return s.Backend.Invoke(ctx, n, a, o)
}
func (s *serialized) Info(ctx context.Context) (runtime.Info, error) {
	done, e := s.enter(ctx)
	if e != nil {
		return runtime.Info{}, e
	}
	defer done()
	return s.Backend.Info(ctx)
}
func (s *serialized) Screenshot(ctx context.Context) ([]byte, error) {
	done, e := s.enter(ctx)
	if e != nil {
		return nil, e
	}
	defer done()
	return s.Backend.Screenshot(ctx)
}
