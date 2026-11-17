package http2

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"testing"

	http "github.com/Mag1cFall/fhttp"
	"github.com/Mag1cFall/fhttp/http2/hpack"
)

// TestTransportPriorityDependencies 断言 PriorityDependencies 按 Chrome 网络栈选择 HEADERS 帧依赖：同权重仍打开的流中最晚创建的一条，没有时取权重更高的最近一级，结束的流不再被依赖
func TestTransportPriorityDependencies(t *testing.T) {
	steps := []struct {
		weight uint8
		parent uint32
		// finish 非零时服务器在回应本请求前结束该流
		finish uint32
	}{
		{weight: 109, parent: 0},
		{weight: 219, parent: 0},
		{weight: 219, parent: 3},
		{weight: 109, parent: 1},
		{weight: 255, parent: 0, finish: 5},
		{weight: 219, parent: 3},
		{weight: 109, parent: 7},
	}
	ct := newClientTester(t)
	ct.tr.PriorityDependencies = true
	ct.client = func() error {
		bodies := make([]io.ReadCloser, 0, len(steps))
		defer func() {
			for _, body := range bodies {
				_ = body.Close()
			}
		}()
		for _, step := range steps {
			ctx := WithHeaderPriority(context.Background(), PriorityParam{Exclusive: true, Weight: step.weight})
			req, _ := http.NewRequestWithContext(ctx, "GET", "https://dummy.tld/", nil)
			res, err := ct.tr.RoundTrip(req)
			if err != nil {
				return err
			}
			bodies = append(bodies, res.Body)
		}
		return nil
	}
	ct.server = func() error {
		ct.greet()
		var block bytes.Buffer
		_ = hpack.NewEncoder(&block).WriteField(hpack.HeaderField{Name: ":status", Value: "200"})
		for index, step := range steps {
			headers, err := ct.firstHeaders()
			if err != nil {
				return err
			}
			want := PriorityParam{StreamDep: step.parent, Exclusive: true, Weight: step.weight}
			if headers.StreamID != uint32(2*index+1) || headers.Priority != want {
				return fmt.Errorf("请求 %d 为流 %d、优先级 %+v，应为流 %d、优先级 %+v", index, headers.StreamID, headers.Priority, 2*index+1, want)
			}
			if step.finish != 0 {
				if err := ct.fr.WriteData(step.finish, true, nil); err != nil {
					return err
				}
			}
			if err := ct.fr.WriteHeaders(HeadersFrameParam{StreamID: headers.StreamID, EndHeaders: true, BlockFragment: block.Bytes()}); err != nil {
				return err
			}
		}
		return nil
	}
	ct.run()
}
