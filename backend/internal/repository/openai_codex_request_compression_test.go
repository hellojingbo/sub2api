package repository

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/url"
	"testing"

	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/require"
)

func newCodexCompressionRequest(t *testing.T, body []byte) *http.Request {
	t.Helper()
	req := &http.Request{
		Method: http.MethodPost,
		URL:    &url.URL{Scheme: "https", Host: "chatgpt.com", Path: "/backend-api/codex/responses"},
		Header: make(http.Header),
		Body:   io.NopCloser(bytes.NewReader(body)),
	}
	return req
}

func decodeCodexZstdTestBody(t *testing.T, body io.Reader) []byte {
	t.Helper()
	decoder, err := zstd.NewReader(body)
	require.NoError(t, err)
	defer decoder.Close()
	decoded, err := io.ReadAll(decoder)
	require.NoError(t, err)
	return decoded
}

func TestCompressOpenAICodexRequestRoundTripsCanonicalBody(t *testing.T) {
	payload := []byte(`{"model":"gpt-5.4","input":[{"type":"text","text":"hello"}]}`)
	req := newCodexCompressionRequest(t, payload)

	require.NoError(t, compressOpenAICodexRequest(req))
	require.Equal(t, "zstd", req.Header.Get("Content-Encoding"))
	require.Greater(t, req.ContentLength, int64(0))
	wire, err := io.ReadAll(req.Body)
	require.NoError(t, err)
	require.NotEqual(t, payload, wire)
	require.Equal(t, payload, decodeCodexZstdTestBody(t, bytes.NewReader(wire)))

	getBody, err := req.GetBody()
	require.NoError(t, err)
	defer getBody.Close()
	require.Equal(t, payload, decodeCodexZstdTestBody(t, getBody))
}

func TestCompressOpenAICodexRequestDecodesExistingEncodingBeforeReencoding(t *testing.T) {
	payload := []byte(`{"model":"gpt-5.4","input":"gzip"}`)
	var encoded bytes.Buffer
	gzipWriter := gzip.NewWriter(&encoded)
	_, err := gzipWriter.Write(payload)
	require.NoError(t, err)
	require.NoError(t, gzipWriter.Close())

	req := newCodexCompressionRequest(t, encoded.Bytes())
	req.Header.Set("Content-Encoding", "gzip")
	require.NoError(t, compressOpenAICodexRequest(req))
	require.Equal(t, payload, decodeCodexZstdTestBody(t, req.Body))
}

func TestCompressOpenAICodexRequestTreatsIdentityAsUncompressed(t *testing.T) {
	payload := []byte(`{"model":"gpt-5.4","input":"identity"}`)
	req := newCodexCompressionRequest(t, payload)
	req.Header.Set("Content-Encoding", "identity")
	require.NoError(t, compressOpenAICodexRequest(req))
	require.Equal(t, payload, decodeCodexZstdTestBody(t, req.Body))
}

func TestCompressOpenAICodexRequestRejectsUnknownEncoding(t *testing.T) {
	req := newCodexCompressionRequest(t, []byte(`{"model":"gpt-5.4"}`))
	req.Header.Set("Content-Encoding", "compress")
	err := compressOpenAICodexRequest(req)
	require.Error(t, err)
	require.Contains(t, err.Error(), "unsupported Codex request Content-Encoding")
}

func TestCompressOpenAICodexRequestSkipsNonNativeEndpoints(t *testing.T) {
	req := newCodexCompressionRequest(t, []byte(`{"model":"gpt-5.4"}`))
	req.URL.Path = "/backend-api/codex/responses/compact"
	require.NoError(t, compressOpenAICodexRequest(req))
	require.Empty(t, req.Header.Get("Content-Encoding"))
	body, err := io.ReadAll(req.Body)
	require.NoError(t, err)
	require.JSONEq(t, `{"model":"gpt-5.4"}`, string(body))
}
