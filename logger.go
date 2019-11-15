package logger

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"crypto/rand"
	"fmt"
	"io"
	"net"
	"os"
	"path"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

type (
	LoggingConfiguration struct {
		GraylogAddress string
		AppName        string
		Hostname       string
	}

	// implement io.Writer
	writer struct {
		conn             net.Conn
		chunkSize        int
		chunkDataSize    int
		compressionType  int
		compressionLevel int
	}

	// implement io.WriteCloser.
	writeCloser struct {
		bytes.Buffer
	}
)

const (
	// MaxChunkCount maximal chunk per message count.
	// See http://docs.graylog.org/en/2.4/pages/gelf.html.
	MaxChunkCount = 128

	// DefaultChunkSize is default WAN chunk size.
	DefaultChunkSize = 1420

	// CompressionNone don't use compression.
	CompressionNone = 0

	// CompressionGzip use gzip compression.
	CompressionGzip = 1

	// CompressionZlib use zlib compression.
	CompressionZlib = 2
)

var (
	// chunkedMagicBytes chunked message magic bytes.
	// See http://docs.graylog.org/en/2.4/pages/gelf.html.
	chunkedMagicBytes = []byte{0x1e, 0x0f}
)

// New creates new apilog.
func New(configuration LoggingConfiguration) (*zap.Logger, error) {
	loggerConf := zap.NewProductionConfig()
	loggerConf.EncoderConfig = zapcore.EncoderConfig{
		TimeKey:        "timestamp",
		NameKey:        "_logger",
		MessageKey:     "short_message",
		StacktraceKey:  "full_message",
		CallerKey:      "_caller",
		LevelKey:       "level_name",
		LineEnding:     zapcore.DefaultLineEnding,
		EncodeName:     zapcore.FullNameEncoder,
		EncodeTime:     zapcore.EpochTimeEncoder,
		EncodeLevel:    zapcore.CapitalLevelEncoder,
		EncodeCaller:   zapcore.ShortCallerEncoder,
		EncodeDuration: zapcore.SecondsDurationEncoder,
	}
	loggerConf.DisableStacktrace = true
	loggerConf.DisableCaller = true

	var err error

	corewrap := func(core zapcore.Core) zapcore.Core {
		if configuration.GraylogAddress != "" {
			var w = &writer{
				chunkSize:        DefaultChunkSize,
				chunkDataSize:    DefaultChunkSize - 12, // chunk size - chunk header size
				compressionType:  CompressionGzip,
				compressionLevel: gzip.BestCompression,
			}

			if w.conn, err = net.DialTimeout("udp", configuration.GraylogAddress, 15*time.Second); err != nil {
				fmt.Println("could not connect with graylog, falling back to stdout")
				return core
			}

			core = zapcore.NewCore(
				zapcore.NewJSONEncoder(loggerConf.EncoderConfig),
				zapcore.AddSync(w),
				zap.NewAtomicLevel(),
			)
		}

		return core
	}

	return loggerConf.Build(
		zap.WrapCore(corewrap),
		zap.Fields(
			zap.Int("pid", os.Getpid()),
			zap.String("app_name", configuration.AppName),
			zap.String("host", configuration.Hostname),
			zap.String("exe", path.Base(os.Args[0])),
			zap.String("version", "1.1"), // GELF version
		),
	)
}

// Close implementation of io.WriteCloser.
func (*writeCloser) Close() error {
	return nil
}

// Write implements io.Writer.
func (w *writer) Write(buf []byte) (n int, err error) {
	var (
		cw   io.WriteCloser
		cBuf bytes.Buffer
	)

	switch w.compressionType {
	case CompressionNone:
		cw = &writeCloser{cBuf}
	case CompressionGzip:
		cw, err = gzip.NewWriterLevel(&cBuf, w.compressionLevel)
	case CompressionZlib:
		cw, err = zlib.NewWriterLevel(&cBuf, w.compressionLevel)
	}

	if err != nil {
		return 0, err
	}

	if n, err = cw.Write(buf); err != nil {
		return n, err
	}

	_ = cw.Close()

	var cBytes = cBuf.Bytes()
	if count := w.chunkCount(cBytes); count > 1 {
		return w.writeChunked(count, cBytes)
	}

	if n, err = w.conn.Write(cBytes); err != nil {
		return n, err
	}

	if n != len(cBytes) {
		return n, fmt.Errorf("writed %d bytes but should %d bytes", n, len(cBytes))
	}

	return n, nil
}

// chunkCount calculate the number of GELF chunks.
func (w *writer) chunkCount(b []byte) int {
	lenB := len(b)
	if lenB <= w.chunkSize {
		return 1
	}

	return len(b)/w.chunkDataSize + 1
}

// writeChunked send message by chunks.
func (w *writer) writeChunked(count int, cBytes []byte) (n int, err error) {
	if count > MaxChunkCount {
		return 0, fmt.Errorf("need %d chunks but shold be later or equal to %d", count, MaxChunkCount)
	}

	var (
		cBuf = bytes.NewBuffer(
			make([]byte, 0, w.chunkSize),
		)
		nChunks   = uint8(count)
		messageID = make([]byte, 8)
	)

	if n, err = io.ReadFull(rand.Reader, messageID); err != nil || n != 8 {
		return 0, fmt.Errorf("rand.Reader: %d/%s", n, err)
	}

	var (
		off       int
		chunkLen  int
		bytesLeft = len(cBytes)
	)

	for i := uint8(0); i < nChunks; i++ {
		off = int(i) * w.chunkDataSize
		chunkLen = w.chunkDataSize
		if chunkLen > bytesLeft {
			chunkLen = bytesLeft
		}

		cBuf.Reset()
		cBuf.Write(chunkedMagicBytes)
		cBuf.Write(messageID)
		cBuf.WriteByte(i)
		cBuf.WriteByte(nChunks)
		cBuf.Write(cBytes[off : off+chunkLen])

		if n, err = w.conn.Write(cBuf.Bytes()); err != nil {
			return len(cBytes) - bytesLeft + n, err
		}

		if n != len(cBuf.Bytes()) {
			n = len(cBytes) - bytesLeft + n
			return n, fmt.Errorf("writed %d bytes but should %d bytes", n, len(cBytes))
		}

		bytesLeft -= chunkLen
	}

	if bytesLeft != 0 {
		return len(cBytes) - bytesLeft, fmt.Errorf("error: %d bytes left after sending", bytesLeft)
	}

	return len(cBytes), nil
}
