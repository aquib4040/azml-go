package uploader

import (
	"errors"
	"io"
	"testing"
	"time"
)

type dummyNetError struct {
	msg     string
	timeout bool
}

func (e *dummyNetError) Error() string   { return e.msg }
func (e *dummyNetError) Timeout() bool   { return e.timeout }
func (e *dummyNetError) Temporary() bool { return true }

func TestIsTransientUploadError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{"nil error", nil, false},
		{"io.EOF", io.EOF, true},
		{"io.ErrUnexpectedEOF", io.ErrUnexpectedEOF, true},
		{"net.Error timeout", &dummyNetError{msg: "i/o timeout", timeout: true}, true},
		{"connection reset", errors.New("read: connection reset by peer"), true},
		{"broken pipe", errors.New("write: broken pipe"), true},
		{"engine was closed", errors.New("gotd: engine was closed"), true},
		{"engine forcibly closed", errors.New("engine forcibly closed"), true},
		{"context deadline exceeded", errors.New("context deadline exceeded"), true},
		{"windows wsarecv", errors.New("wsarecv: An existing connection was forcibly closed by the remote host"), true},
		{"windows wsasend", errors.New("wsasend: An established connection was aborted"), true},
		{"rpc error 500 INTERNAL", errors.New("rpc error: code 500: INTERNAL"), true},
		{"rpc error WORKER_BUSY", errors.New("rpc error: code 500: WORKER_BUSY_TOO_LONG_RETRY"), true},
		{"rpc call fail", errors.New("RPC_CALL_FAIL: telegram connection closed"), true},
		{"fatal permission denied", errors.New("permission denied"), false},
		{"fatal file not found", errors.New("no such file or directory"), false},
		{"fatal file part invalid", errors.New("FILE_PART_0_MISSING"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isTransientUploadError(tt.err)
			if got != tt.expected {
				t.Errorf("isTransientUploadError(%v) = %v; want %v", tt.err, got, tt.expected)
			}
		})
	}
}

func TestUploadProgressTracker(t *testing.T) {
	var reportedCurr, reportedTotal int64
	var reportedSpeed float64
	var reportedETA string

	tracker := &uploadProgressTracker{
		uploadStart: time.Now().Add(-2 * time.Second),
		onProgress: func(curr, total int64, speed float64, eta string) {
			reportedCurr = curr
			reportedTotal = total
			reportedSpeed = speed
			reportedETA = eta
		},
	}

	tracker.Report(10*1024*1024, 20*1024*1024)

	if reportedCurr != 10*1024*1024 {
		t.Errorf("expected reportedCurr %d, got %d", 10*1024*1024, reportedCurr)
	}
	if reportedTotal != 20*1024*1024 {
		t.Errorf("expected reportedTotal %d, got %d", 20*1024*1024, reportedTotal)
	}
	if reportedSpeed <= 0 {
		t.Errorf("expected speed > 0, got %f", reportedSpeed)
	}
	if reportedETA == "" || reportedETA == "-" {
		t.Errorf("expected valid ETA, got %s", reportedETA)
	}
}

func TestGetChannelID(t *testing.T) {
	if id := getChannelID(-1001234567890); id != 1234567890 {
		t.Errorf("expected 1234567890, got %d", id)
	}
	if id := getChannelID(-12345); id != 12345 {
		t.Errorf("expected 12345, got %d", id)
	}
	if id := getChannelID(99999); id != 99999 {
		t.Errorf("expected 99999, got %d", id)
	}
}

func TestMediaExtChecks(t *testing.T) {
	videoExts := []string{".mp4", ".mkv", ".webm", ".avi", ".mov", ".flv", ".ts"}
	for _, ext := range videoExts {
		if !isVideoExt(ext) {
			t.Errorf("expected %s to be recognized as video", ext)
		}
	}

	nonVideo := []string{".zip", ".tar.gz", ".pdf", ".txt"}
	for _, ext := range nonVideo {
		if isVideoExt(ext) {
			t.Errorf("expected %s NOT to be recognized as video", ext)
		}
	}
}
