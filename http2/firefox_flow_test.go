package http2

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	http "github.com/Mag1cFall/fhttp"
	"github.com/Mag1cFall/fhttp/http2/hpack"
)

// firefoxFlowTester 返回按 Firefox 152 设置流控的 clientTester
func firefoxFlowTester(t *testing.T) *clientTester {
	ct := newClientTester(t)
	ct.tr.Settings = map[SettingID]uint32{SettingInitialWindowSize: 131072}
	ct.tr.SettingsOrder = []SettingID{SettingInitialWindowSize}
	ct.tr.ConnectionFlow = 12517377
	ct.tr.InitialStreamID = 3
	ct.tr.StreamWindowUpdate = 12451840
	ct.tr.WindowUpdateThreshold = 4 << 20
	return ct
}

// TestTransportFirefoxFlowControl 断言请求流在 HEADERS 后立即开到 12 MiB，未确认字节到 4 MiB 才归还
func TestTransportFirefoxFlowControl(t *testing.T) {
	for _, test := range []struct {
		method   string
		response int
		updates  int
	}{
		{"GET", 2_000_000, 0},
		{"POST", 2_000_000, 0},
		{"GET", 5_000_000, 2},
	} {
		t.Run(fmt.Sprintf("%s-%d", test.method, test.response), func(t *testing.T) {
			ct := firefoxFlowTester(t)
			ct.client = func() error {
				var body io.Reader
				if test.method == "POST" {
					body = strings.NewReader("[]")
				}
				req, _ := http.NewRequest(test.method, "https://dummy.tld/", body)
				res, err := ct.tr.RoundTrip(req)
				if err != nil {
					return err
				}
				defer res.Body.Close()
				n, err := io.Copy(io.Discard, res.Body)
				if err != nil || n != int64(test.response) {
					return fmt.Errorf("read %d bytes, %v", n, err)
				}
				return nil
			}
			ct.server = func() error {
				ct.greet()
				var headers *HeadersFrame
				for headers == nil {
					f, err := ct.readFrame()
					if err != nil {
						return err
					}
					switch f := f.(type) {
					case *HeadersFrame:
						headers = f
					case *SettingsFrame, *WindowUpdateFrame:
					default:
						return fmt.Errorf("frame before HEADERS: %v", f)
					}
				}
				if headers.StreamID != 3 || headers.StreamEnded() != (test.method == "GET") {
					return fmt.Errorf("HEADERS stream %d end %v", headers.StreamID, headers.StreamEnded())
				}
				f, err := ct.readFrame()
				if err != nil {
					return err
				}
				if update, ok := f.(*WindowUpdateFrame); !ok || update.StreamID != 3 || update.Increment != 12451840 {
					return fmt.Errorf("frame after HEADERS: %v", f)
				}
				if test.method == "POST" {
					f, err := ct.readNonSettingsFrame()
					if err != nil {
						return err
					}
					if data, ok := f.(*DataFrame); !ok || string(data.Data()) != "[]" || !data.StreamEnded() {
						return fmt.Errorf("request body frame: %v", f)
					}
				}
				var buf bytes.Buffer
				hpack.NewEncoder(&buf).WriteField(hpack.HeaderField{Name: ":status", Value: "200"})
				ct.fr.WriteHeaders(HeadersFrameParam{StreamID: 3, EndHeaders: true, BlockFragment: buf.Bytes()})
				chunk := make([]byte, 16384)
				for sent := 0; sent < test.response; sent += len(chunk) {
					size := min(len(chunk), test.response-sent)
					if err := ct.fr.WriteData(3, sent+size == test.response, chunk[:size]); err != nil {
						return err
					}
				}
				var updates []*WindowUpdateFrame
				for {
					f, err := readFrameTimeout(ct.fr, 500*time.Millisecond)
					if err != nil {
						break
					}
					if update, ok := f.(*WindowUpdateFrame); ok {
						updates = append(updates, update)
					}
				}
				if len(updates) != test.updates {
					return fmt.Errorf("got %d WINDOW_UPDATE after the response, want %d", len(updates), test.updates)
				}
				for _, update := range updates {
					if update.Increment < 4<<20 || update.StreamID != 0 && update.Increment > 4<<20+32768 {
						return fmt.Errorf("WINDOW_UPDATE stream %d increment %d", update.StreamID, update.Increment)
					}
				}
				return nil
			}
			ct.run()
		})
	}
}
