package openai

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
)

const codexRequestMaxDecompressedBody = 64 << 20

type codexRequestCloser func()

func (f codexRequestCloser) Close() error {
	f()
	return nil
}

func isCodexResponsesRequest(req *http.Request) bool {
	if req == nil || req.URL == nil || req.Body == nil || req.Body == http.NoBody {
		return false
	}
	return req.Method == http.MethodPost &&
		strings.EqualFold(req.URL.Hostname(), "chatgpt.com") &&
		req.URL.EscapedPath() == "/backend-api/codex/responses"
}

// CompressCodexResponsesRequest encodes the final native Codex Responses JSON
// body as zstd. It first decodes a pre-existing request Content-Encoding so a
// caller cannot accidentally send nested encodings or stale length metadata.
func CompressCodexResponsesRequest(req *http.Request) error {
	if !isCodexResponsesRequest(req) {
		return nil
	}

	raw, err := io.ReadAll(io.LimitReader(req.Body, codexRequestMaxDecompressedBody+1))
	closeErr := req.Body.Close()
	if err != nil {
		return fmt.Errorf("read Codex request body for compression: %w", err)
	}
	if closeErr != nil {
		return fmt.Errorf("close Codex request body before compression: %w", closeErr)
	}
	if int64(len(raw)) > codexRequestMaxDecompressedBody {
		return fmt.Errorf("Codex request body exceeds %d bytes before compression", codexRequestMaxDecompressedBody)
	}

	encoding := strings.ToLower(strings.TrimSpace(req.Header.Get("Content-Encoding")))
	decoded, err := decodeCodexRequestBody(encoding, raw)
	if err != nil {
		return err
	}
	if int64(len(decoded)) > codexRequestMaxDecompressedBody {
		return fmt.Errorf("decoded Codex request body exceeds %d bytes", codexRequestMaxDecompressedBody)
	}

	var compressed bytes.Buffer
	encoder, err := zstd.NewWriter(&compressed, zstd.WithEncoderLevel(zstd.SpeedDefault))
	if err != nil {
		return fmt.Errorf("create zstd Codex request encoder: %w", err)
	}
	if _, err := encoder.Write(decoded); err != nil {
		_ = encoder.Close()
		return fmt.Errorf("compress Codex request body with zstd: %w", err)
	}
	if err := encoder.Close(); err != nil {
		return fmt.Errorf("finish zstd Codex request body: %w", err)
	}

	wireBody := append([]byte(nil), compressed.Bytes()...)
	req.Body = io.NopCloser(bytes.NewReader(wireBody))
	req.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(wireBody)), nil
	}
	req.ContentLength = int64(len(wireBody))
	req.TransferEncoding = nil
	req.Header.Del("Content-Length")
	req.Header.Set("Content-Encoding", "zstd")
	return nil
}

func decodeCodexRequestBody(encoding string, raw []byte) ([]byte, error) {
	if encoding == "" || encoding == "identity" {
		return raw, nil
	}
	if strings.Contains(encoding, ",") {
		return nil, fmt.Errorf("unsupported multiple Codex request Content-Encoding values: %q", encoding)
	}

	var reader io.Reader
	var closer io.Closer
	switch encoding {
	case "gzip", "x-gzip":
		gzipReader, err := gzip.NewReader(bytes.NewReader(raw))
		if err != nil {
			return nil, fmt.Errorf("decode Codex gzip request body: %w", err)
		}
		reader, closer = gzipReader, gzipReader
	case "deflate":
		zlibReader, err := zlib.NewReader(bytes.NewReader(raw))
		if err == nil {
			reader, closer = zlibReader, zlibReader
		} else {
			rawReader := flate.NewReader(bytes.NewReader(raw))
			reader, closer = rawReader, rawReader
		}
	case "br":
		reader = brotli.NewReader(bytes.NewReader(raw))
	case "zstd":
		zstdReader, err := zstd.NewReader(bytes.NewReader(raw))
		if err != nil {
			return nil, fmt.Errorf("decode Codex zstd request body: %w", err)
		}
		reader, closer = zstdReader, codexRequestCloser(zstdReader.Close)
	default:
		return nil, fmt.Errorf("unsupported Codex request Content-Encoding %q", encoding)
	}
	if closer != nil {
		defer func() { _ = closer.Close() }()
	}
	decoded, err := io.ReadAll(io.LimitReader(reader, codexRequestMaxDecompressedBody+1))
	if err != nil {
		return nil, fmt.Errorf("read decoded Codex request body: %w", err)
	}
	if int64(len(decoded)) > codexRequestMaxDecompressedBody {
		return nil, fmt.Errorf("decoded Codex request body exceeds %d bytes", codexRequestMaxDecompressedBody)
	}
	return decoded, nil
}
