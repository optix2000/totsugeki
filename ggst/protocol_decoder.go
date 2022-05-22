package ggst

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"

	"github.com/vmihailenco/msgpack/v5"
)

func ParseReq(r *http.Request, v interface{}) error {
	req, err := hex.DecodeString(strings.TrimRight(r.FormValue("data"), "\x00")) // Clean up input
	if err != nil {
		fmt.Println(err)
	}

	err = msgpack.Unmarshal(req, &v)
	if err != nil {
		return err
	}
	return nil
}

// BufferedResponseWriter is a wrapper around http.ResponseWriter that buffers the response for later use.
type BufferedResponseWriter struct {
	HttpHeader http.Header
	StatusCode int
	Body       bytes.Buffer
}

func (b *BufferedResponseWriter) Header() http.Header {
	return b.HttpHeader
}

func (b *BufferedResponseWriter) WriteHeader(code int) {
	b.StatusCode = code
}

func (b *BufferedResponseWriter) Write(data []byte) (int, error) {
	return b.Body.Write(data)
}
