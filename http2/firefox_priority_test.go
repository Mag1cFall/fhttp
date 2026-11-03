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

// TestTransportRequestHeaderPriority 断言请求上下文的 HEADERS 帧优先级只作用于该请求，其余请求沿用 Transport.HeaderPriority
func TestTransportRequestHeaderPriority(t *testing.T) {
	ct := newClientTester(t)
	ct.tr.HeaderPriority = &PriorityParam{Weight: 41}
	want := []uint8{21, 41}
	ct.client = func() error {
		for _, ctx := range []context.Context{WithHeaderPriority(context.Background(), PriorityParam{Weight: want[0]}), context.Background()} {
			req, _ := http.NewRequestWithContext(ctx, "GET", "https://dummy.tld/", nil)
			res, err := ct.tr.RoundTrip(req)
			if err != nil {
				return err
			}
			_, _ = io.Copy(io.Discard, res.Body)
			_ = res.Body.Close()
		}
		return nil
	}
	ct.server = func() error {
		ct.greet()
		for index := 0; index < len(want); {
			f, err := ct.readNonSettingsFrame()
			if err != nil {
				return err
			}
			headers, ok := f.(*HeadersFrame)
			if !ok {
				continue
			}
			if headers.Priority != (PriorityParam{Weight: want[index]}) {
				return fmt.Errorf("请求 %d 的 HEADERS 优先级为 %+v，应为权重 %d", index, headers.Priority, want[index])
			}
			var block bytes.Buffer
			_ = hpack.NewEncoder(&block).WriteField(hpack.HeaderField{Name: ":status", Value: "200"})
			if err := ct.fr.WriteHeaders(HeadersFrameParam{StreamID: headers.StreamID, EndHeaders: true, EndStream: true, BlockFragment: block.Bytes()}); err != nil {
				return err
			}
			index++
		}
		return nil
	}
	ct.run()
}
